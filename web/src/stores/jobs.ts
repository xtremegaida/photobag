import { create } from "zustand";
import type { Job } from "../api/types";

interface JobsState {
  jobs: Record<string, Job>;
  connected: boolean;
  upsert: (job: Job) => void;
  setAll: (jobs: Job[]) => void;
  setConnected: (c: boolean) => void;
}

/** Live job snapshots, fed by the server event stream. */
export const useJobStore = create<JobsState>((set) => ({
  jobs: {},
  connected: false,
  upsert: (job) => set((s) => ({ jobs: { ...s.jobs, [job.id]: job } })),
  setAll: (jobs) => set({ jobs: Object.fromEntries(jobs.map((j) => [j.id, j])) }),
  setConnected: (connected) => set({ connected }),
}));

export const isActive = (j: Job) => j.status === "queued" || j.status === "running";

export function useJob(id: string | undefined): Job | undefined {
  return useJobStore((s) => (id ? s.jobs[id] : undefined));
}
