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

/**
 * Decodes text the way an editor would guess: a byte-order mark decides,
 * then UTF-8 if the bytes are valid UTF-8, else Windows-1252.
 */
export function decodeText(buf: ArrayBuffer): { text: string; encoding: string } {
  const b = new Uint8Array(buf);
  if (b[0] === 0xef && b[1] === 0xbb && b[2] === 0xbf) return { text: new TextDecoder("utf-8").decode(b.subarray(3)), encoding: "UTF-8" };
  if (b[0] === 0xff && b[1] === 0xfe) return { text: new TextDecoder("utf-16le").decode(b.subarray(2)), encoding: "UTF-16" };
  if (b[0] === 0xfe && b[1] === 0xff) return { text: new TextDecoder("utf-16be").decode(b.subarray(2)), encoding: "UTF-16" };
  try {
    return { text: new TextDecoder("utf-8", { fatal: true }).decode(b), encoding: "UTF-8" };
  } catch {
    // A cut-off multi-byte character at the end of a partial read is not
    // a reason to give up on UTF-8.
    if (b.length > 4) {
      try {
        return { text: new TextDecoder("utf-8", { fatal: true }).decode(b.subarray(0, b.length - 3)), encoding: "UTF-8" };
      } catch {
        // fall through
      }
    }
    return { text: new TextDecoder("windows-1252").decode(b), encoding: "Windows-1252" };
  }
}

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
