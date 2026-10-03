// Helpers for the files space: gathering dropped files and folders,
// decoding text, and resolving the relative links of notes.

/** A file to upload and its path below the destination folder. */
export interface UploadEntry {
  file: File;
  path: string;
}

/** What a drop or a file picker brought: files, plus folders with nothing in them. */
export interface Gathered {
  files: UploadEntry[];
  dirs: string[];
}

/** Names left out of uploaded folders, as imports from disk leave them out. */
export function isClutter(name: string): boolean {
  const n = name.toLowerCase();
  return n.startsWith(".") || n === "thumbs.db" || n === "desktop.ini" || n === "ehthumbs.db";
}

function readEntries(reader: FileSystemDirectoryReader): Promise<FileSystemEntry[]> {
  return new Promise((resolve, reject) => reader.readEntries(resolve, reject));
}

async function walkEntry(entry: FileSystemEntry, prefix: string, out: Gathered, top: boolean) {
  if (!top && isClutter(entry.name)) return;
  if (entry.isFile) {
    const file = await new Promise<File>((resolve, reject) => (entry as FileSystemFileEntry).file(resolve, reject));
    out.files.push({ file, path: prefix + entry.name });
    return;
  }
  if (!entry.isDirectory) return;
  const reader = (entry as FileSystemDirectoryEntry).createReader();
  const kids: FileSystemEntry[] = [];
  // readEntries returns the listing in batches until it returns none.
  for (let batch = await readEntries(reader); batch.length; batch = await readEntries(reader)) kids.push(...batch);
  const before = out.files.length + out.dirs.length;
  for (const k of kids) await walkEntry(k, prefix + entry.name + "/", out, false);
  if (out.files.length + out.dirs.length === before) out.dirs.push(prefix + entry.name);
}

/**
 * Collects what was dropped, folders included. It must be called from the
 * drop handler itself: the entries are only reachable during the event.
 */
export function gatherDrop(dt: DataTransfer): Promise<Gathered> {
  const entries = [...dt.items]
    .filter((i) => i.kind === "file")
    .map((i) => i.webkitGetAsEntry?.())
    .filter((e): e is FileSystemEntry => !!e);
  const plain = [...dt.files];
  return (async () => {
    const out: Gathered = { files: [], dirs: [] };
    if (entries.length === 0) {
      out.files = plain.map((file) => ({ file, path: file.name }));
      return out;
    }
    for (const e of entries) await walkEntry(e, "", out, true);
    return out;
  })();
}

/** Files from an <input type=file>; a folder picker supplies relative paths. */
export function gatherInput(list: FileList | File[]): Gathered {
  const files: UploadEntry[] = [];
  for (const file of list) {
    const path = file.webkitRelativePath || file.name;
    if (path.split("/").slice(1).some(isClutter)) continue;
    files.push({ file, path });
  }
  return { files, dirs: [] };
}

/** Whether a drag carries files from outside the page. */
export const hasOsFiles = (dt: DataTransfer | null) => !!dt && [...dt.types].includes("Files");

/** How a text file's bytes were written, so an edit is saved the same way. */
export type TextEncoding = "utf-8" | "utf-8-bom" | "utf-16le" | "utf-16be" | "windows-1252";

export const encodingLabels: Record<TextEncoding, string> = {
  "utf-8": "UTF-8",
  "utf-8-bom": "UTF-8 with BOM",
  "utf-16le": "UTF-16 LE",
  "utf-16be": "UTF-16 BE",
  "windows-1252": "Windows-1252",
};

export interface DecodedText {
  text: string;
  encoding: TextEncoding;
}

/**
 * Decodes text the way an editor would guess: a byte-order mark decides,
 * then UTF-8 if the bytes are valid UTF-8, else Windows-1252.
 */
export function decodeText(buf: ArrayBuffer): DecodedText {
  const b = new Uint8Array(buf);
  const bomless = { ignoreBOM: true };
  if (b[0] === 0xef && b[1] === 0xbb && b[2] === 0xbf) {
    return { text: new TextDecoder("utf-8", bomless).decode(b.subarray(3)), encoding: "utf-8-bom" };
  }
  if (b[0] === 0xff && b[1] === 0xfe) return { text: new TextDecoder("utf-16le", bomless).decode(b.subarray(2)), encoding: "utf-16le" };
  if (b[0] === 0xfe && b[1] === 0xff) return { text: new TextDecoder("utf-16be", bomless).decode(b.subarray(2)), encoding: "utf-16be" };
  try {
    return { text: new TextDecoder("utf-8", { fatal: true }).decode(b), encoding: "utf-8" };
  } catch {
    // A cut-off multi-byte character at the end of a partial read is not
    // a reason to give up on UTF-8.
    if (b.length > 4) {
      try {
        return { text: new TextDecoder("utf-8", { fatal: true }).decode(b.subarray(0, b.length - 3)), encoding: "utf-8" };
      } catch {
        // fall through
      }
    }
    return { text: new TextDecoder("windows-1252").decode(b), encoding: "windows-1252" };
  }
}

let cp1252: Map<string, number> | undefined;

/** Text as Windows-1252 bytes, or null if it has characters that has not. */
function toWindows1252(text: string): Uint8Array<ArrayBuffer> | null {
  if (!cp1252) {
    // Every byte decodes to its own character, so decoding them all gives
    // the way back.
    const chars = new TextDecoder("windows-1252").decode(Uint8Array.from({ length: 256 }, (_, i) => i));
    cp1252 = new Map([...chars].map((c, i) => [c, i]));
  }
  const out = new Uint8Array(text.length);
  for (let i = 0; i < text.length; i++) {
    const b = cp1252.get(text[i]);
    if (b === undefined) return null;
    out[i] = b;
  }
  return out;
}

function toUTF16(text: string, littleEndian: boolean): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(2 + text.length * 2);
  const v = new DataView(out.buffer);
  v.setUint16(0, 0xfeff, littleEndian);
  for (let i = 0; i < text.length; i++) v.setUint16(2 + i * 2, text.charCodeAt(i), littleEndian);
  return out;
}

/**
 * Encodes text as it was read. Text Windows-1252 cannot hold is stored as
 * UTF-8 instead; the encoding returned says which was used.
 */
export function encodeText(text: string, encoding: TextEncoding): { bytes: Uint8Array<ArrayBuffer>; encoding: TextEncoding } {
  switch (encoding) {
    case "utf-16le":
    case "utf-16be":
      return { bytes: toUTF16(text, encoding === "utf-16le"), encoding };
    case "windows-1252": {
      const bytes = toWindows1252(text);
      if (bytes) return { bytes, encoding };
      break;
    }
    case "utf-8-bom": {
      const body = new TextEncoder().encode(text);
      const bytes = new Uint8Array(body.length + 3);
      bytes.set([0xef, 0xbb, 0xbf]);
      bytes.set(body, 3);
      return { bytes, encoding };
    }
  }
  return { bytes: new TextEncoder().encode(text), encoding: "utf-8" };
}

export type LineEnding = "\n" | "\r\n";

/** The line ending a text mostly uses (LF when it has no line breaks). */
export function lineEnding(text: string): LineEnding {
  const crlf = text.match(/\r\n/g)?.length ?? 0;
  const lf = (text.match(/\n/g)?.length ?? 0) - crlf;
  return crlf > lf ? "\r\n" : "\n";
}

/** Text with LF line breaks (as an editor holds it) given another ending. */
export const withLineEnding = (text: string, eol: LineEnding) => (eol === "\n" ? text : text.replace(/\n/g, eol));

/**
 * Resolves a link in a note at notePath ("docs/guide.md") against the
 * note's folder. It returns the bag path it points to, or null for links
 * that leave the files space (other sites, anchors, absolute paths).
 */
export function resolveNoteLink(notePath: string, href: string): string | null {
  if (!href || /^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith("//") || href.startsWith("#") || href.startsWith("/")) {
    return null;
  }
  const clean = href.split(/[?#]/)[0];
  let target: string;
  try {
    target = decodeURIComponent(clean);
  } catch {
    target = clean;
  }
  const parts = notePath.split("/").slice(0, -1);
  for (const seg of target.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") {
      if (!parts.length) return null;
      parts.pop();
    } else parts.push(seg);
  }
  return parts.join("/");
}

/** Splits a name into its stem and extension (".tar.gz" counts as ".gz"). */
export function splitExt(name: string): [string, string] {
  const i = name.lastIndexOf(".");
  if (i <= 0) return [name, ""];
  return [name.slice(0, i), name.slice(i)];
}

/**
 * Names what was dropped by its top-level names: "Holiday", "Holiday and
 * beach.jpg", "Holiday, beach.jpg and 3 more".
 */
export function describeDrop(entries: UploadEntry[]): string {
  const tops = [...new Set(entries.map((e) => e.path.split("/")[0]).filter(Boolean))];
  if (tops.length <= 2) return tops.join(" and ") || "files";
  return `${tops[0]}, ${tops[1]} and ${tops.length - 2} more`;
}
