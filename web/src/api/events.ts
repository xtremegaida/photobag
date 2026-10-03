import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useJobStore } from "../stores/jobs";
import { api, bagTag } from "./client";
import { invalidateTopics } from "./hooks";
import type { BackupDownload, Job } from "./types";

const finishedTopics: Record<string, string[]> = {
  import: ["images", "tags"],
  "empty-trash": ["images", "trash", "tags", "metrics", "decks"],
  compact: ["images"],
  backup: ["images"],
  "dedup-scan": ["dedup"],
  analyze: ["analysis", "tags", "images"],
  retag: ["analysis", "tags", "images"],
  generate: ["generations", "experiments"],
  "files-import": ["files"],
  reencode: ["reencode", "images", "stats"],
};

/** Backup jobs started by this tab: download the file when they finish. */
const pendingDownloads = new Set<string>();

export function downloadWhenDone(jobId: string) {
  pendingDownloads.add(jobId);
}

function maybeDownload(job: Job) {
  if (!pendingDownloads.has(job.id) || job.status !== "done") return;
  pendingDownloads.delete(job.id);
  const r = job.result as BackupDownload | undefined;
  if (r?.url) {
    const a = document.createElement("a");
    a.href = r.url;
    a.download = r.filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
  }
}

/** Subscribes to the server event stream for the lifetime of the app. */
export function useServerEvents() {
  const qc = useQueryClient();
  useEffect(() => {
    const store = useJobStore.getState();
    let es: EventSource | null = null;
    let wasConnected = false;
    let closed = false;

    const syncJobs = () =>
      api
        .get<Job[]>("/api/jobs")
        .then((jobs) => {
          store.setAll(jobs);
          jobs.forEach(maybeDownload);
        })
        .catch(() => undefined);

    const connect = () => {
      es = new EventSource("/api/events");
      es.onopen = () => {
        useJobStore.getState().setConnected(true);
        if (wasConnected) invalidateTopics(qc, ["all"]); // we may have missed events
        wasConnected = true;
        syncJobs();
      };
      es.onerror = () => useJobStore.getState().setConnected(false);
      es.onmessage = (m) => {
        const ev = JSON.parse(m.data) as { type: string; data: unknown };
        if (ev.type === "hello") {
          // Another bag is served at this address now: start afresh, or
          // this page would show its images under the old bag's ids.
          const bag = (ev.data as { bag: string }).bag;
          if (!bagTag.startsWith("dev-") && bag !== bagTag) window.location.reload();
        } else if (ev.type === "job") {
          const job = ev.data as Job;
          const prev = useJobStore.getState().jobs[job.id];
          useJobStore.getState().upsert(job);
          if (prev?.status !== job.status && (job.status === "done" || job.status === "failed" || job.status === "cancelled")) {
            invalidateTopics(qc, finishedTopics[job.kind] ?? []);
            qc.invalidateQueries({ queryKey: ["jobs"] });
            maybeDownload(job);
          }
        } else if (ev.type === "changed") {
          invalidateTopics(qc, (ev.data as { topics: string[] }).topics);
        }
      };
    };
    if (!closed) connect();
    return () => {
      closed = true;
      es?.close();
    };
  }, [qc]);
}
