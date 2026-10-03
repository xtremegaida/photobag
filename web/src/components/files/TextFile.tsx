import { ActionIcon, Alert, Anchor, Button, Group, Loader, Modal, ScrollArea, SegmentedControl, Stack, Text, Tooltip } from "@mantine/core";
import { useDebouncedCallback, useHotkeys, useLocalStorage } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconCopy, IconDeviceFloppy, IconLayoutColumns, IconPencil, IconTextWrap } from "@tabler/icons-react";
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { useBlocker, useLocation, useNavigate } from "react-router";
import { ApiError, errorMessage } from "../../api/client";
import { createFile, fileContentUrl, fk, filesRoute, joinPath, saveFileContent, textKey } from "../../api/files";
import { invalidateTopics } from "../../api/hooks";
import type { FileListing, FileNode } from "../../api/types";
import {
  decodeText,
  encodeText,
  encodingLabels,
  lineEnding,
  type LineEnding,
  splitExt,
  type TextEncoding,
  withLineEnding,
} from "../../lib/files";
import { formatBytes, plural } from "../../lib/format";
import { Confirm } from "../Confirm";
import type { EditorHandle, EditorStatus } from "./CodeEditor";
import { NameDialog } from "./dialogs";
import { FileActions, FileHeader, parentPathOf } from "./FileHeader";
import classes from "./Files.module.css";

const CodeEditor = lazy(() => import("./CodeEditor"));
const MarkdownNote = lazy(() => import("./Markdown"));

/** How much of a text file is shown in the browser; larger files cannot be edited here. */
const TEXT_LIMIT = 2 << 20;

function useText(node: FileNode) {
  return useQuery({
    // Keyed by content, outside the "files" topic: a change elsewhere does
    // not refetch it, a replaced file does.
    queryKey: textKey(node.id, node.sha256 ?? ""),
    queryFn: async () => {
      const res = await fetch(fileContentUrl(node), {
        credentials: "same-origin",
        headers: node.size > TEXT_LIMIT ? { Range: `bytes=0-${TEXT_LIMIT - 1}` } : {},
      });
      if (!res.ok) throw new Error(res.statusText || `HTTP ${res.status}`);
      return decodeText(await res.arrayBuffer());
    },
    staleTime: Infinity,
    // A new version arriving must not take the editor away meanwhile.
    placeholderData: keepPreviousData,
  });
}

/** "notes (copy).md" */
const copyName = (name: string) => {
  const [stem, ext] = splitExt(name);
  return `${stem} (copy)${ext}`;
};

const eolLabel = (eol: LineEnding) => (eol === "\r\n" ? "CRLF" : "LF");

/** What an edit started from, and how the text is to be written back. */
interface Session {
  text: string;
  /** The version the next save replaces (it changes with each save). */
  sha: string;
  encoding: TextEncoding;
  eol: LineEnding;
}

/** A text or Markdown file: shown read-only, and edited in place. */
export function TextFile({ listing, path }: { listing: FileListing; path: string }) {
  const node = listing.node!;
  const markdown = node.kind === "markdown";
  const qc = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const { data, error, isLoading, isPlaceholderData } = useText(node);
  const cut = node.size > TEXT_LIMIT;

  const [wrap, setWrap] = useLocalStorage({ key: "pb-files-wrap", defaultValue: true });
  const [split, setSplit] = useLocalStorage({ key: "pb-files-preview", defaultValue: true });
  const [mode, setMode] = useState<"rendered" | "source">("rendered");
  const [session, setSession] = useState<Session | null>(null);
  const editing = session !== null;
  const [generation, setGeneration] = useState(0); // remounts the editor
  const [dirty, setDirtyState] = useState(false);
  const dirtyRef = useRef(false);
  const setDirty = (d: boolean) => {
    dirtyRef.current = d;
    setDirtyState(d);
  };
  const [status, setStatus] = useState<EditorStatus>();
  const [preview, setPreview] = useState("");
  const editor = useRef<EditorHandle>(null);
  const [saving, setSaving] = useState(false);
  const [copying, setCopying] = useState(false);
  const [copyError, setCopyError] = useState<string>();
  const [conflict, setConflict] = useState<{ close: boolean } | null>(null);
  const [discarding, setDiscarding] = useState(false);

  // Editing starts from the current version, not one shown while it loads.
  const canEdit = !!data && !cut && !isPlaceholderData;
  const startEditing = () => {
    if (!data || !canEdit) return;
    setSession({ text: data.text, sha: node.sha256 ?? "", encoding: data.encoding, eol: lineEnding(data.text) });
    setPreview(data.text);
  };
  const stopEditing = () => {
    setSession(null);
    setDirty(false);
  };

  // A new file opens ready for typing.
  const wantsEdit = useRef(!!(location.state as { edit?: boolean } | null)?.edit);
  useEffect(() => {
    if (!wantsEdit.current || !canEdit) return;
    wantsEdit.current = false;
    startEditing();
    navigate(location.pathname, { replace: true, state: null });
  });

  // Unsaved changes are not left behind without asking.
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirtyRef.current && currentLocation.pathname !== nextLocation.pathname);
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const refreshPreview = useDebouncedCallback(() => setPreview(editor.current?.text() ?? ""), 250);

  /** The editor's text as the file's bytes. */
  const encoded = (s: Session) => {
    const text = withLineEnding(editor.current?.text() ?? "", s.eol);
    return { text, ...encodeText(text, s.encoding) };
  };

  const saved = (n: FileNode, text: string, encoding: TextEncoding, was: TextEncoding) => {
    qc.setQueryData(textKey(n.id, n.sha256 ?? ""), { text, encoding });
    if (encoding !== was) {
      notifications.show({
        color: "yellow",
        title: `Saved as ${encodingLabels[encoding]}`,
        message: `The text has characters ${encodingLabels[was]} cannot hold.`,
      });
    }
  };

  const save = async (close: boolean, force = false) => {
    if (!session || saving) return;
    const { text, bytes, encoding } = encoded(session);
    setSaving(true);
    try {
      const n = await saveFileContent(node.id, bytes, force ? undefined : session.sha);
      editor.current?.markSaved();
      saved(n, text, encoding, session.encoding);
      // Show the saved version straight away; the refetch confirms it.
      qc.setQueryData<FileListing>(fk.at(path), (l) => (l ? { ...l, node: n } : l));
      invalidateTopics(qc, ["files"]);
      setConflict(null);
      if (close) stopEditing();
      else setSession({ ...session, sha: n.sha256 ?? "", encoding });
    } catch (e) {
      if (e instanceof ApiError && e.status === 412) setConflict({ close });
      else notifications.show({ color: "red", title: "Not saved", message: errorMessage(e) });
    } finally {
      setSaving(false);
    }
  };

  const saveCopy = async (name: string) => {
    if (!session) return;
    const { text, bytes, encoding } = encoded(session);
    setCopyError(undefined);
    setSaving(true);
    try {
      const copy = (await createFile(node.parentId, name, bytes)).node!;
      editor.current?.markSaved();
      saved(copy, text, encoding, session.encoding);
      invalidateTopics(qc, ["files"]);
      setCopying(false);
      setConflict(null);
      notifications.show({ message: `Saved as “${copy.name}”. “${node.name}” is as it was.` });
      navigate(filesRoute(joinPath([parentPathOf(listing), copy.name])));
    } catch (e) {
      setCopyError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const cancel = () => {
    if (dirtyRef.current) setDiscarding(true);
    else stopEditing();
  };
  const discard = () => {
    setDiscarding(false);
    stopEditing();
    setGeneration((g) => g + 1);
  };

  const dialog = copying || !!conflict || discarding || blocker.state === "blocked";
  useHotkeys([
    ["e", () => !editing && !dialog && canEdit && startEditing()],
    ["mod+S", () => editing && !dialog && save(false)],
  ]);

  // Someone else changed the file while it was being edited.
  const changedElsewhere = editing && !!node.sha256 && node.sha256 !== session.sha;
  const showSource = editing || !markdown || mode === "source";
  const showPreview = editing && markdown && split;

  let body;
  if (!data && !editing) {
    body = isLoading ? <Loader m="md" /> : error ? <Alert color="red" m="md">{errorMessage(error)}</Alert> : null;
  } else if (!showSource) {
    body = (
      <ScrollArea style={{ flex: 1 }}>
        <Suspense fallback={<Loader m="md" />}>
          <MarkdownNote text={data!.text} notePath={path} />
        </Suspense>
      </ScrollArea>
    );
  } else {
    body = (
      <div className={classes.split}>
        <div className={classes.code}>
          <Suspense fallback={<Loader m="md" />}>
            <CodeEditor
              key={generation}
              value={editing ? session.text : data!.text}
              fileName={node.name}
              markdown={markdown}
              readOnly={!editing}
              wrap={wrap}
              handle={editor}
              onDirty={setDirty}
              onStatus={setStatus}
              onChange={() => showPreview && refreshPreview()}
              onSave={() => editing && save(false)}
            />
          </Suspense>
        </div>
        {showPreview && (
          <ScrollArea className={classes.livePreview}>
            <Suspense fallback={<Loader m="md" />}>
              <MarkdownNote text={preview} notePath={path} />
            </Suspense>
          </ScrollArea>
        )}
      </div>
    );
  }

  const encoding = session?.encoding ?? data?.encoding;
  const eol = session?.eol ?? (data ? lineEnding(data.text) : undefined);

  return (
    <div className={classes.viewer}>
      <FileHeader listing={listing}>
        {editing ? (
          <>
            <Button variant="default" onClick={cancel}>
              {dirty ? "Cancel" : "Done"}
            </Button>
            <Button variant="default" leftSection={<IconCopy size={16} />} onClick={() => setCopying(true)}>
              Save as copy…
            </Button>
            <Tooltip label="Ctrl+S saves and carries on editing">
              <Button leftSection={<IconDeviceFloppy size={16} />} disabled={!dirty} loading={saving} onClick={() => save(true)}>
                Save
              </Button>
            </Tooltip>
          </>
        ) : (
          <>
            <Tooltip label={cut ? `Files over ${formatBytes(TEXT_LIMIT)} cannot be edited here` : "Edit (E)"}>
              <Button leftSection={<IconPencil size={16} />} disabled={!canEdit} onClick={startEditing}>
                Edit
              </Button>
            </Tooltip>
            <FileActions listing={listing} />
          </>
        )}
      </FileHeader>
      <Group gap="xs" px="md" py={6} justify="space-between" wrap="nowrap" className={classes.textBar}>
        <Text size="xs" c="dimmed" truncate>
          {cut && !editing && `Showing the first ${formatBytes(TEXT_LIMIT)} of ${formatBytes(node.size)} · `}
          {editing && status && `Line ${status.line}, column ${status.column} · `}
          {showSource && status && `${plural(status.lines, "line")} · `}
          {encoding && encodingLabels[encoding]}
          {eol && ` · ${eolLabel(eol)}`}
          {editing && dirty && (
            <Text span size="xs" c="orange" fw={600}>
              {" "}
              · Unsaved changes
            </Text>
          )}
        </Text>
        <Group gap="xs" wrap="nowrap">
          {markdown && !editing && (
            <SegmentedControl
              size="xs"
              value={mode}
              onChange={(v) => setMode(v as "rendered" | "source")}
              data={[
                { value: "rendered", label: "Formatted" },
                { value: "source", label: "Source" },
              ]}
            />
          )}
          {markdown && editing && (
            <Tooltip label={split ? "Hide the formatted note" : "Show the formatted note beside the text"}>
              <Button
                size="compact-xs"
                variant={split ? "light" : "subtle"}
                color={split ? undefined : "gray"}
                leftSection={<IconLayoutColumns size={14} />}
                onClick={() => {
                  if (!split) setPreview(editor.current?.text() ?? "");
                  setSplit(!split);
                }}
              >
                Preview
              </Button>
            </Tooltip>
          )}
          {showSource && (
            <Tooltip label={wrap ? "Don't wrap long lines" : "Wrap long lines"}>
              <ActionIcon variant={wrap ? "light" : "subtle"} color="gray" onClick={() => setWrap(!wrap)} aria-label="Wrap long lines">
                <IconTextWrap size={16} />
              </ActionIcon>
            </Tooltip>
          )}
        </Group>
      </Group>
      {changedElsewhere && (
        <Alert color="orange" py={6} px="md" radius={0}>
          <Text size="sm">
            “{node.name}” was changed elsewhere since you began editing. Saving will ask before replacing that version.{" "}
            <Anchor component="button" size="sm" onClick={() => (dirtyRef.current ? setDiscarding(true) : discard())}>
              Drop your changes and show it
            </Anchor>
          </Text>
        </Alert>
      )}
      <div className={classes.viewer}>{body}</div>

      <NameDialog
        opened={copying}
        title="Save as a copy"
        initial={copyName(node.name)}
        confirm="Save copy"
        requireChange={false}
        loading={saving}
        error={copyError}
        description={`In the same folder. “${node.name}” stays as it was.`}
        onSubmit={saveCopy}
        onClose={() => {
          setCopying(false);
          setCopyError(undefined);
        }}
      />
      <Modal opened={!!conflict} onClose={() => setConflict(null)} title="The file has changed">
        <Stack>
          <Text size="sm">
            Since you began editing, “{node.name}” was changed elsewhere (in another tab, or by an upload or import). Saving
            now replaces that version with yours.
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setConflict(null)}>
              Cancel
            </Button>
            <Button
              variant="default"
              onClick={() => {
                setConflict(null);
                setCopying(true);
              }}
            >
              Save as copy…
            </Button>
            <Button color="orange" loading={saving} onClick={() => save(conflict?.close ?? false, true)}>
              Replace it
            </Button>
          </Group>
        </Stack>
      </Modal>
      <Confirm
        opened={discarding}
        title="Discard your changes?"
        confirm="Discard changes"
        cancel="Keep editing"
        onClose={() => setDiscarding(false)}
        onConfirm={discard}
      >
        <Text size="sm">What you changed in “{node.name}” since it was last saved will be lost.</Text>
      </Confirm>
      <Confirm
        opened={blocker.state === "blocked"}
        title="Leave without saving?"
        confirm="Discard changes"
        cancel="Keep editing"
        onClose={() => blocker.reset?.()}
        onConfirm={() => blocker.proceed?.()}
      >
        <Text size="sm">“{node.name}” has changes that are not saved yet. They will be lost.</Text>
      </Confirm>
    </div>
  );
}
