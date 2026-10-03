// The text editor of the files space (CodeMirror), loaded on demand: it
// shows a text file read-only, and edits it.
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { markdown as markdownSupport, markdownLanguage } from "@codemirror/lang-markdown";
import {
  bracketMatching,
  foldGutter,
  foldKeymap,
  HighlightStyle,
  indentOnInput,
  LanguageDescription,
  syntaxHighlighting,
} from "@codemirror/language";
import { languages } from "@codemirror/language-data";
import { highlightSelectionMatches, search, searchKeymap } from "@codemirror/search";
import { Compartment, EditorSelection, EditorState, type Extension, Prec, Text } from "@codemirror/state";
import {
  crosshairCursor,
  drawSelection,
  dropCursor,
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  highlightSpecialChars,
  keymap,
  lineNumbers,
  rectangularSelection,
} from "@codemirror/view";
import { tags as t } from "@lezer/highlight";
import { useComputedColorScheme } from "@mantine/core";
import { type Ref, useEffect, useImperativeHandle, useLayoutEffect, useRef } from "react";

export interface EditorHandle {
  /** The text, with LF line breaks. */
  text(): string;
  /** Takes the text as it is now as saved: it no longer counts as a change. */
  markSaved(): void;
  focus(): void;
}

export interface EditorStatus {
  lines: number;
  line: number;
  column: number;
}

export interface CodeEditorProps {
  /** The text to show. A new value replaces the text unless it has unsaved changes. */
  value: string;
  /** The file's name, which decides the syntax highlighting. */
  fileName: string;
  markdown?: boolean;
  readOnly: boolean;
  wrap: boolean;
  handle?: Ref<EditorHandle>;
  /** Whether the text differs from the saved text. */
  onDirty?: (dirty: boolean) => void;
  /** The text changed. */
  onChange?: () => void;
  onStatus?: (s: EditorStatus) => void;
  /** Ctrl+S (⌘S) in the editor. */
  onSave?: () => void;
}

/** Colours from Mantine's variables, so light and dark schemes both work. */
const highlight = HighlightStyle.define([
  { tag: t.heading, fontWeight: "bold", color: "var(--mantine-color-blue-text)" },
  { tag: t.strong, fontWeight: "bold" },
  { tag: t.emphasis, fontStyle: "italic" },
  { tag: t.strikethrough, textDecoration: "line-through" },
  { tag: [t.link, t.url], color: "var(--mantine-color-anchor)" },
  { tag: t.link, textDecoration: "underline" },
  { tag: t.monospace, color: "var(--mantine-color-orange-text)" },
  { tag: t.quote, color: "var(--mantine-color-dimmed)", fontStyle: "italic" },
  { tag: [t.processingInstruction, t.meta, t.contentSeparator], color: "var(--mantine-color-dimmed)" },
  { tag: [t.keyword, t.operatorKeyword, t.modifier, t.controlKeyword, t.definitionKeyword, t.moduleKeyword], color: "var(--mantine-color-violet-text)" },
  { tag: [t.string, t.special(t.string), t.regexp, t.character], color: "var(--mantine-color-green-text)" },
  { tag: [t.number, t.bool, t.null, t.atom, t.unit], color: "var(--mantine-color-orange-text)" },
  { tag: [t.comment, t.lineComment, t.blockComment, t.docComment], color: "var(--mantine-color-dimmed)", fontStyle: "italic" },
  { tag: [t.function(t.variableName), t.function(t.propertyName), t.macroName], color: "var(--mantine-color-blue-text)" },
  { tag: [t.typeName, t.className, t.namespace, t.labelName], color: "var(--mantine-color-teal-text)" },
  { tag: [t.propertyName, t.attributeName], color: "var(--mantine-color-cyan-text)" },
  { tag: [t.tagName, t.angleBracket], color: "var(--mantine-color-red-text)" },
  { tag: [t.escape, t.special(t.variableName)], color: "var(--mantine-color-pink-text)" },
  { tag: t.invalid, color: "var(--mantine-color-red-text)" },
]);

const theme = EditorView.theme({
  "&": {
    flex: 1,
    minHeight: 0,
    fontSize: "var(--mantine-font-size-sm)",
    color: "var(--mantine-color-text)",
    backgroundColor: "transparent",
  },
  "&.cm-focused": { outline: "none" },
  ".cm-scroller": { overflow: "auto", fontFamily: "var(--mantine-font-family-monospace)", lineHeight: "1.55" },
  ".cm-content": { padding: "var(--mantine-spacing-xs) 0", caretColor: "var(--mantine-color-text)" },
  ".cm-line": { padding: "0 var(--mantine-spacing-md) 0 var(--mantine-spacing-xs)" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--mantine-color-text)", borderLeftWidth: "2px" },
  ".cm-gutters": {
    backgroundColor: "var(--mantine-color-body)",
    color: "var(--mantine-color-dimmed)",
    borderRight: "1px solid var(--mantine-color-default-border)",
  },
  ".cm-activeLine, .cm-activeLineGutter": { backgroundColor: "color-mix(in srgb, var(--mantine-color-default-hover) 70%, transparent)" },
  "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection": {
    backgroundColor: "color-mix(in srgb, var(--mantine-primary-color-filled) 30%, transparent) !important",
  },
  ".cm-selectionMatch": { backgroundColor: "color-mix(in srgb, var(--mantine-color-yellow-5) 20%, transparent)" },
  ".cm-searchMatch": { backgroundColor: "color-mix(in srgb, var(--mantine-color-yellow-5) 35%, transparent)" },
  ".cm-searchMatch.cm-searchMatch-selected": { backgroundColor: "color-mix(in srgb, var(--mantine-color-orange-5) 55%, transparent)" },
  ".cm-matchingBracket": { backgroundColor: "color-mix(in srgb, var(--mantine-color-teal-5) 30%, transparent)", outline: "none" },
  ".cm-foldPlaceholder": { backgroundColor: "var(--mantine-color-default-hover)", border: "none", color: "var(--mantine-color-dimmed)" },
  ".cm-panels": { backgroundColor: "var(--mantine-color-body)", color: "var(--mantine-color-text)" },
  ".cm-panels.cm-panels-top": { borderBottom: "1px solid var(--mantine-color-default-border)" },
  ".cm-panel.cm-search": { padding: "6px 10px", fontFamily: "var(--mantine-font-family)" },
  ".cm-panel.cm-search input, .cm-panel.cm-search button, .cm-panel.cm-search label": { fontSize: "var(--mantine-font-size-xs)" },
  ".cm-textfield": {
    backgroundColor: "var(--mantine-color-default)",
    border: "1px solid var(--mantine-color-default-border)",
    borderRadius: "var(--mantine-radius-sm)",
    color: "var(--mantine-color-text)",
  },
  ".cm-button": {
    backgroundImage: "none",
    backgroundColor: "var(--mantine-color-default)",
    border: "1px solid var(--mantine-color-default-border)",
    borderRadius: "var(--mantine-radius-sm)",
    color: "var(--mantine-color-text)",
  },
  // Viewing: no caret or current line, which would suggest typing works.
  "&.pb-readonly .cm-cursorLayer": { display: "none" },
  "&.pb-readonly .cm-activeLine, &.pb-readonly .cm-activeLineGutter": { backgroundColor: "transparent" },
});

/** The search panel's words, capitalised as elsewhere in the app. */
const phrases = {
  Find: "Find",
  next: "Next",
  previous: "Previous",
  all: "All",
  "match case": "Match case",
  regexp: "Regular expression",
  "by word": "Whole words",
  replace: "Replace",
  "replace all": "Replace all",
  close: "Close",
};

const MARKDOWN = markdownSupport({ base: markdownLanguage, codeLanguages: languages });

/** Syntax highlighting for a file (loaded on demand, by name). */
async function languageFor(fileName: string, markdown: boolean): Promise<Extension> {
  if (markdown) return MARKDOWN;
  const d = LanguageDescription.matchFilename(languages, fileName);
  return d ? d.load() : [];
}

const asDoc = (s: string) => Text.of(s.split(/\r\n?|\n/));

function editMode(readOnly: boolean): Extension {
  return readOnly ? [EditorState.readOnly.of(true), EditorView.editorAttributes.of({ class: "pb-readonly" })] : [];
}

export default function CodeEditor({
  value,
  fileName,
  markdown = false,
  readOnly,
  wrap,
  handle,
  onDirty,
  onChange,
  onStatus,
  onSave,
}: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const saved = useRef(Text.empty);
  const dirty = useRef(false);
  const parts = useRef({ edit: new Compartment(), wrap: new Compartment(), lang: new Compartment(), dark: new Compartment() });
  const dark = useComputedColorScheme("dark") === "dark";
  // The editor is made once; it calls whatever the latest callbacks are.
  const calls = useRef({ onDirty, onChange, onStatus, onSave });
  useLayoutEffect(() => {
    calls.current = { onDirty, onChange, onStatus, onSave };
  });
  const initial = useRef({ value, readOnly, wrap, dark });

  useEffect(() => {
    const p = parts.current;
    const init = initial.current;
    const status = (s: EditorState) => {
      const head = s.selection.main.head;
      const line = s.doc.lineAt(head);
      calls.current.onStatus?.({ lines: s.doc.lines, line: line.number, column: head - line.from + 1 });
    };
    const doc = asDoc(init.value);
    saved.current = doc;
    const v = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc,
        extensions: [
          Prec.highest(
            keymap.of([
              {
                key: "Mod-s",
                preventDefault: true,
                run: () => {
                  calls.current.onSave?.();
                  return true;
                },
              },
            ]),
          ),
          lineNumbers(),
          foldGutter(),
          highlightActiveLineGutter(),
          highlightSpecialChars(),
          history(),
          drawSelection(),
          dropCursor(),
          EditorState.allowMultipleSelections.of(true),
          indentOnInput(),
          syntaxHighlighting(highlight),
          bracketMatching(),
          rectangularSelection(),
          crosshairCursor(),
          highlightActiveLine(),
          highlightSelectionMatches(),
          search({ top: true }),
          keymap.of([...defaultKeymap, ...searchKeymap, ...historyKeymap, ...foldKeymap, indentWithTab]),
          theme,
          p.edit.of(editMode(init.readOnly)),
          p.wrap.of(init.wrap ? EditorView.lineWrapping : []),
          p.lang.of([]),
          p.dark.of(EditorView.darkTheme.of(init.dark)),
          EditorView.contentAttributes.of({ "aria-label": "File text" }),
          EditorState.phrases.of(phrases),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) {
              const d = !u.state.doc.eq(saved.current);
              if (d !== dirty.current) {
                dirty.current = d;
                calls.current.onDirty?.(d);
              }
              calls.current.onChange?.();
            }
            if (u.docChanged || u.selectionSet) status(u.state);
          }),
        ],
      }),
    });
    view.current = v;
    status(v.state);
    if (!init.readOnly) v.focus();
    return () => {
      v.destroy();
      view.current = null;
    };
  }, []);

  // A new version of the file (saved here, or changed elsewhere while it
  // was only being viewed).
  useEffect(() => {
    const v = view.current;
    if (!v || dirty.current) return;
    const doc = asDoc(value);
    if (doc.eq(v.state.doc)) return;
    saved.current = doc;
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: doc } });
  }, [value]);

  useEffect(() => {
    const v = view.current;
    if (!v || v.state.readOnly === readOnly) return;
    v.dispatch({ effects: parts.current.edit.reconfigure(editMode(readOnly)) });
    if (readOnly) return;
    // Start typing where the reader was looking, not at a caret left
    // somewhere off screen.
    const head = v.state.selection.main.head;
    if (!v.visibleRanges.some((r) => head >= r.from && head <= r.to)) {
      const top = v.lineBlockAtHeight(v.scrollDOM.getBoundingClientRect().top - v.documentTop + 1);
      v.dispatch({ selection: EditorSelection.cursor(top.from) });
    }
    v.focus();
  }, [readOnly]);

  useEffect(() => {
    view.current?.dispatch({ effects: parts.current.wrap.reconfigure(wrap ? EditorView.lineWrapping : []) });
  }, [wrap]);

  useEffect(() => {
    view.current?.dispatch({ effects: parts.current.dark.reconfigure(EditorView.darkTheme.of(dark)) });
  }, [dark]);

  useEffect(() => {
    let live = true;
    languageFor(fileName, markdown).then(
      (ext) => live && view.current?.dispatch({ effects: parts.current.lang.reconfigure(ext) }),
      () => undefined, // no highlighting, then
    );
    return () => {
      live = false;
    };
  }, [fileName, markdown]);

  useImperativeHandle(
    handle,
    () => ({
      text: () => view.current?.state.doc.toString() ?? "",
      markSaved: () => {
        if (!view.current) return;
        saved.current = view.current.state.doc;
        if (dirty.current) {
          dirty.current = false;
          calls.current.onDirty?.(false);
        }
      },
      focus: () => view.current?.focus(),
    }),
    [],
  );

  return <div ref={host} style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }} />;
}
