import { create } from "zustand";
import type { FilePutResult } from "../api/types";
import type { UploadEntry } from "../lib/files";

export type UploadStatus = "queued" | "uploading" | "done" | "skipped" | "failed" | "cancelled";

export interface UploadItem {
  key: number;
  path: string;
  size: number;
  loaded: number;
  status: UploadStatus;
  outcome?: string;
  error?: string;
}

interface Uploads {
  items: UploadItem[];
  /** Where the latest uploads went, for the panel's title. */
  dest: string;
  enqueue: (entries: UploadEntry[], parent: number, conflict: string, dest: string) => void;
  cancel: () => void;
  clear: () => void;
}

interface Task {
  key: number;
  entry: UploadEntry;
  parent: number;
  conflict: string;
}

const PARALLEL = 3;
let nextKey = 1;
const queue: Task[] = [];
const running = new Map<number, XMLHttpRequest>();

/** Uploads to the bag's files, a few at a time, with progress. */
export const useUploads = create<Uploads>((set, get) => {
  const update = (key: number, patch: Partial<UploadItem>) =>
    set((s) => ({ items: s.items.map((it) => (it.key === key ? { ...it, ...patch } : it)) }));

  const start = (t: Task) => {
    const q = new URLSearchParams({
      path: t.entry.path,
      conflict: t.conflict,
      modified: String(t.entry.file.lastModified || Date.now()),
    });
    if (t.parent) q.set("parent", String(t.parent));
    const xhr = new XMLHttpRequest();
    running.set(t.key, xhr);
    xhr.open("PUT", "/api/files/upload?" + q.toString());
    xhr.upload.onprogress = (e) => update(t.key, { loaded: e.loaded });
    const finish = (patch: Partial<UploadItem>) => {
      running.delete(t.key);
      update(t.key, patch);
      pump();
    };
    xhr.onload = () => {
      let body: (FilePutResult & { error?: string }) | undefined;
      try {
        body = JSON.parse(xhr.responseText);
      } catch {
        // not JSON
      }
      if (xhr.status >= 200 && xhr.status < 300 && body) {
        finish({ status: body.outcome === "skipped" ? "skipped" : "done", outcome: body.outcome, loaded: t.entry.file.size });
      } else {
        finish({ status: "failed", error: body?.error || xhr.statusText || `HTTP ${xhr.status}` });
      }
    };
    xhr.onerror = () => finish({ status: "failed", error: "the upload was interrupted" });
    xhr.onabort = () => finish({ status: "cancelled" });
    update(t.key, { status: "uploading" });
    xhr.send(t.entry.file);
  };

  const pump = () => {
    while (running.size < PARALLEL && queue.length) start(queue.shift()!);
  };

  return {
    items: [],
    dest: "",
    enqueue: (entries, parent, conflict, dest) => {
      const items: UploadItem[] = entries.map((e) => {
        const key = nextKey++;
        queue.push({ key, entry: e, parent, conflict });
        return { key, path: e.path, size: e.file.size, loaded: 0, status: "queued" };
      });
      // Finished uploads from before make way for the new ones.
      const keep = get().items.filter((it) => it.status === "queued" || it.status === "uploading" || it.status === "failed");
      set({ items: [...keep, ...items], dest });
      pump();
    },
    cancel: () => {
      for (const t of queue.splice(0)) update(t.key, { status: "cancelled" });
      for (const xhr of running.values()) xhr.abort();
    },
    clear: () => set({ items: get().items.filter((it) => it.status === "queued" || it.status === "uploading") }),
  };
});

export const uploadActive = (it: UploadItem) => it.status === "queued" || it.status === "uploading";
