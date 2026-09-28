import { keepPreviousData, useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { createBatcher } from "./batcher";
import { api } from "./client";
import type {
  Analysis,
  AnalysisOptions,
  AnalysisOutcome,
  AnalysisPlan,
  AnalysisSettingsView,
  FsListing,
  Image,
  ImageDetail,
  ImageQuery,
  Job,
  Metric,
  Pair,
  PipelineStats,
  Rankings,
  Resolution,
  Run,
  Scan,
  ScanView,
  Sort,
  Stats,
  Tag,
} from "./types";

export const qk = {
  stats: ["stats"] as const,
  tags: ["tags"] as const,
  ids: (q: ImageQuery, s: Sort) => ["ids", q, s] as const,
  count: (q: ImageQuery) => ["count", q] as const,
  image: (id: number) => ["image", id] as const,
  detail: (id: number) => ["detail", id] as const,
  metrics: ["metrics"] as const,
  runs: (metric = 0) => ["runs", metric] as const,
  run: (id: number) => ["run", id] as const,
  pair: (id: number) => ["pair", id] as const,
  rankings: (id: number) => ["rankings", id] as const,
  jobs: ["jobs"] as const,
  scans: ["scans"] as const,
  scan: (id: string, t: number) => ["scan", id, t] as const,
  fs: (p: string) => ["fs", p] as const,
  analysisSettings: ["analysis-settings"] as const,
  analysisStats: ["analysis-stats"] as const,
  analysisPlan: (o: AnalysisOptions) => ["analysis-plan", o] as const,
};

/** Maps server "changed" topics to the query keys they invalidate. */
const topicKeys: Record<string, string[]> = {
  images: ["ids", "count", "image", "detail", "stats", "rankings"],
  tags: ["tags", "image", "detail", "ids", "count"],
  trash: ["ids", "count", "stats", "image", "detail"],
  metrics: ["metrics", "rankings", "detail", "ids"],
  runs: ["runs", "run", "metrics"],
  dedup: ["scans", "scan"],
  analysis: ["detail", "image", "ids", "count", "stats", "analysis-stats", "analysis-plan"],
  "analysis-settings": ["analysis-settings", "analysis-plan"],
};

export function invalidateTopics(qc: QueryClient, topics: string[]) {
  if (topics.includes("all")) {
    qc.invalidateQueries();
    return;
  }
  const keys = new Set(topics.flatMap((t) => topicKeys[t] ?? []));
  for (const k of keys) qc.invalidateQueries({ queryKey: [k] });
}

export function useStats() {
  return useQuery({ queryKey: qk.stats, queryFn: () => api.get<Stats>("/api/stats") });
}

export function useTags() {
  return useQuery({ queryKey: qk.tags, queryFn: () => api.get<Tag[]>("/api/tags"), staleTime: 10_000 });
}

export function useImageIds(query: ImageQuery, sort: Sort) {
  return useQuery({
    queryKey: qk.ids(query, sort),
    queryFn: () => api.post<{ ids: number[]; total: number }>("/api/images/ids", { query, sort }),
    placeholderData: keepPreviousData,
  });
}

export function useCount(query: ImageQuery, enabled = true) {
  return useQuery({
    queryKey: qk.count(query),
    queryFn: () =>
      api.post<{ count: number; pairs: number; description: string }>("/api/images/count", { query }),
    enabled,
    placeholderData: keepPreviousData,
  });
}

const imageBatcher = createBatcher<number, Image>(async (ids) => {
  const list = await api.post<Image[]>("/api/images/batch", { ids });
  return new Map(list.map((im) => [im.id, im]));
});

/** Metadata of one image; concurrent calls are batched into one request. */
export function useImage(id: number | undefined) {
  return useQuery({
    queryKey: qk.image(id ?? 0),
    queryFn: () => imageBatcher.load(id!),
    enabled: id !== undefined && id > 0,
    staleTime: 30_000,
  });
}

export function useImageDetail(id: number | undefined) {
  return useQuery({
    queryKey: qk.detail(id ?? 0),
    queryFn: () => api.get<ImageDetail>(`/api/images/${id}`),
    enabled: id !== undefined && id > 0,
  });
}

export function useMetrics() {
  return useQuery({ queryKey: qk.metrics, queryFn: () => api.get<Metric[]>("/api/metrics") });
}

export function useRuns(metric = 0) {
  return useQuery({
    queryKey: qk.runs(metric),
    queryFn: () => api.get<Run[]>(`/api/runs${metric ? `?metric=${metric}` : ""}`),
  });
}

export function useRankings(metricId: number) {
  return useQuery({
    queryKey: qk.rankings(metricId),
    queryFn: () => api.get<Rankings>(`/api/metrics/${metricId}/rankings`),
    enabled: metricId > 0,
  });
}

export function usePair(runId: number) {
  return useQuery({
    queryKey: qk.pair(runId),
    queryFn: () => api.get<Pair>(`/api/runs/${runId}/next`),
    enabled: runId > 0,
    staleTime: Infinity,
  });
}

export function useJobs() {
  return useQuery({ queryKey: qk.jobs, queryFn: () => api.get<Job[]>("/api/jobs") });
}

export function useScans() {
  return useQuery({ queryKey: qk.scans, queryFn: () => api.get<Scan[]>("/api/dedup/scans") });
}

export function useScan(id: string | undefined, threshold: number) {
  return useQuery({
    queryKey: qk.scan(id ?? "", threshold),
    queryFn: () => api.get<ScanView>(`/api/dedup/scans/${id}?threshold=${threshold}`),
    enabled: !!id,
    placeholderData: keepPreviousData,
    retry: false,
  });
}

export function useAnalysisSettings() {
  return useQuery({
    queryKey: qk.analysisSettings,
    queryFn: () => api.get<AnalysisSettingsView>("/api/analysis/settings"),
    staleTime: 60_000,
  });
}

export function useAnalysisStats() {
  return useQuery({ queryKey: qk.analysisStats, queryFn: () => api.get<PipelineStats[]>("/api/analysis/stats") });
}

export function useAnalysisPlan(opts: AnalysisOptions, enabled = true) {
  return useQuery({
    queryKey: qk.analysisPlan(opts),
    queryFn: () => api.post<AnalysisPlan>("/api/analysis/plan", opts),
    enabled: enabled && opts.pipelines.length > 0,
    placeholderData: keepPreviousData,
  });
}

export function useFsList(path: string, enabled: boolean) {
  return useQuery({
    queryKey: qk.fs(path),
    queryFn: () => api.get<FsListing>(`/api/fs/list?path=${encodeURIComponent(path)}`),
    enabled,
    retry: false,
  });
}

// ---- mutations ----

function useInvalidating<TVars, TResult>(fn: (v: TVars) => Promise<TResult>, topics: string[]) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => invalidateTopics(qc, topics),
  });
}

export function useBulkTags() {
  return useInvalidating(
    (v: { ids: number[]; add?: string[]; remove?: string[] }) => api.post("/api/images/tags", v),
    ["images", "tags"],
  );
}

export function useRename() {
  return useInvalidating(
    (v: { id: number; name: string }) => api.patch<Image>(`/api/images/${v.id}`, { name: v.name }),
    ["images"],
  );
}

export function useTrash() {
  return useInvalidating(
    (ids: number[]) => api.post<{ trashed: number }>("/api/images/trash", { ids }),
    ["images", "trash", "tags", "metrics"],
  );
}

export function useRestore() {
  return useInvalidating(
    (ids: number[]) => api.post<{ restored: number }>("/api/images/restore", { ids }),
    ["images", "trash", "tags", "metrics", "dedup"],
  );
}

export function useResolve() {
  return useInvalidating(
    (v: { scanId: string; resolutions?: Resolution[]; applyAll?: boolean; threshold?: number }) =>
      api.post<{ trashed: number; resolved: number }>("/api/dedup/resolve", v),
    ["images", "trash", "tags", "metrics", "dedup"],
  );
}

export function useEditAnalysis() {
  return useInvalidating(
    (v: { id: number; pipeline: string; text: string }) =>
      api.put<Analysis[]>(`/api/images/${v.id}/analysis/${v.pipeline}`, { text: v.text }),
    ["analysis"],
  );
}

export function useDeleteAnalysis() {
  return useInvalidating(
    (v: { id: number; pipeline: string }) => api.del(`/api/images/${v.id}/analysis/${v.pipeline}`),
    ["analysis", "tags"],
  );
}

export function useAnalyzeImage() {
  return useInvalidating(
    (v: { id: number; pipelines: string[] }) =>
      api.post<{ outcomes: AnalysisOutcome[] }>(`/api/images/${v.id}/analyze`, { pipelines: v.pipelines }),
    ["analysis", "tags"],
  );
}

export function useSubmitJob() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { path: string; body: unknown }) => api.post<Job>(v.path, v.body),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.jobs }),
  });
}
