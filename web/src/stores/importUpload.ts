import { notifications } from "@mantine/notifications";
import { create } from "zustand";
import { api, errorMessage } from "../api/client";
import type { ImportOptions, Job, UploadOffer, UploadPlan } from "../api/types";
import { describeDrop, type UploadEntry } from "../lib/files";

/** The bytes the server needs to tell an image's format (imaging.SniffLen). */
const SNIFF_LEN = 32;
const PARALLEL = 3;

export type ImportUploadPhase = "idle" | "checking" | "uploading" | "starting";

interface ImportUpload {
  phase: ImportUploadPhase;
  /** What is being uploaded ("Holiday and 2 more"). */
  title: string;
  /** The files being sent, how many are through, and how many failed. */
  files: number;
  sent: number;
  failed: number;
  bytes: number;
  loaded: number;
  /** Files the server did not want (not images, and the like). */
  leftOut: number;
  /** Uploads the files and imports them; resolves to the import job once it is queued. */
  start: (entries: UploadEntry[], options: ImportOptions) => Promise<Job | null>;
  cancel: () => void;
}

const idle = { phase: "idle", title: "", files: 0, sent: 0, failed: 0, bytes: 0, loaded: 0, leftOut: 0 } as const;

let cancelled = false;
const running = new Set<XMLHttpRequest>();

function toBase64(buf: ArrayBuffer): string {
  return btoa(String.fromCharCode(...new Uint8Array(buf)));
}

/** The first bytes of each file, which tell the server whether it is an image. */
async function heads(entries: UploadEntry[]): Promise<string[]> {
  const out: string[] = [];
  for (let i = 0; i < entries.length && !cancelled; i += 64) {
    const batch = entries.slice(i, i + 64).map((e) =>
      e.file
        .slice(0, SNIFF_LEN)
        .arrayBuffer()
        .then(toBase64, () => ""),
    );
    out.push(...(await Promise.all(batch)));
  }
  return out;
}

function send(id: string, e: UploadEntry, progress: (loaded: number) => void): Promise<boolean> {
  return new Promise((resolve) => {
    const q = new URLSearchParams({ path: e.path, modified: String(e.file.lastModified || Date.now()) });
    const xhr = new XMLHttpRequest();
    running.add(xhr);
    const done = (ok: boolean) => {
      running.delete(xhr);
      resolve(ok);
    };
    xhr.open("PUT", `/api/imports/uploads/${id}/files?${q}`);
    xhr.upload.onprogress = (ev) => progress(ev.loaded);
    xhr.onload = () => done(xhr.status >= 200 && xhr.status < 300);
    xhr.onerror = () => done(false);
    xhr.onabort = () => done(false);
    xhr.send(e.file);
  });
}

/**
 * Uploads files from this browser for an import, a few at a time, then
 * starts the import. It carries on while the user goes elsewhere.
 */
export const useImportUpload = create<ImportUpload>((set, get) => ({
  ...idle,
  start: async (entries, options) => {
    if (get().phase !== "idle") return null;
    cancelled = false;
    set({ ...idle, phase: "checking", title: describeDrop(entries), files: entries.length });
    let id = "";
    try {
      const head = await heads(entries);
      if (cancelled) return null;
      const files: UploadOffer[] = entries.map((e, i) => ({ path: e.path, size: e.file.size, head: head[i] }));
      const plan = await api.post<UploadPlan>("/api/imports/uploads", { files });
      id = plan.id;
      const skip = new Set(plan.skip.map((s) => s.path));
      const todo = entries.filter((e) => !skip.has(e.path));
      set({
        phase: "uploading",
        files: todo.length,
        leftOut: plan.skip.length,
        bytes: todo.reduce((n, e) => n + e.file.size, 0),
      });

      const loaded = new Array<number>(todo.length).fill(0);
      let total = 0;
      const progress = (i: number, n: number) => {
        total += n - loaded[i];
        loaded[i] = n;
        set({ loaded: total });
      };
      let next = 0;
      const worker = async () => {
        while (!cancelled && next < todo.length) {
          const i = next++;
          const ok = await send(id, todo[i], (n) => progress(i, n));
          progress(i, todo[i].file.size);
          set((s) => (ok ? { sent: s.sent + 1 } : { failed: s.failed + 1 }));
        }
      };
      await Promise.all(Array.from({ length: PARALLEL }, worker));
      if (cancelled) return null;

      set({ phase: "starting" });
      const job = await api.post<Job>(`/api/imports/uploads/${id}/import`, { options });
      id = "";
      return job;
    } catch (e) {
      notifications.show({ color: "red", title: "Import not started", message: errorMessage(e) });
      return null;
    } finally {
      // Thrown away unless the import took them.
      if (id) api.del(`/api/imports/uploads/${id}`).catch(() => {});
      set(idle);
    }
  },
  cancel: () => {
    cancelled = true;
    for (const xhr of running) xhr.abort();
  },
}));

// Closing the page would end the upload.
window.addEventListener("beforeunload", (e) => {
  if (useImportUpload.getState().phase !== "idle") e.preventDefault();
});
