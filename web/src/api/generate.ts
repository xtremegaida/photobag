// Queries and mutations for image generation with ComfyUI.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, shaThumbUrl, tagged, versioned } from "./client";
import { invalidateTopics } from "./hooks";
import type {
  ComfyNodes,
  ComfySettings,
  ExperimentView,
  GenerateRequest,
  Generation,
  GenerationDetail,
  GenerationRun,
  MoveResult,
  PlanSummary,
  WorkflowDetail,
  WorkflowTemplate,
  WorkflowVersion,
} from "./types";

export const gk = {
  settings: ["comfy-settings"] as const,
  nodes: (classes: string[]) => ["comfy-nodes", classes] as const,
  workflows: ["workflows"] as const,
  workflow: (id: number) => ["workflow", id] as const,
  version: (id: number) => ["workflow-version", id] as const,
  experiments: ["experiments"] as const,
  experiment: (id: number) => ["experiment", id] as const,
  generations: (id: number, moved: boolean) => ["generations", id, moved] as const,
  generation: (id: number) => ["generation", id] as const,
  runs: (id: number) => ["gen-runs", id] as const,
  plan: (id: number, r: GenerateRequest) => ["gen-plan", id, r] as const,
};

export const generationThumb = (g: Pick<Generation, "sha256">) => shaThumbUrl(g.sha256);
type GenerationRef = Pick<Generation, "id"> & { sha256?: string };
export const generationPreview = (g: GenerationRef, size = 1600) =>
  tagged(versioned(`/api/generations/${g.id}/preview?size=${size}`, g.sha256));
export const generationOriginal = (g: GenerationRef, download = false) =>
  tagged(versioned(`/api/generations/${g.id}/original${download ? "?download=1" : ""}`, g.sha256));
export const generationWorkflowUrl = (id: number) => `/api/generations/${id}/workflow?download=1`;

export function useComfySettings() {
  return useQuery({ queryKey: gk.settings, queryFn: () => api.get<ComfySettings>("/api/comfy/settings") });
}

/** ComfyUI's definitions of node classes (input types, choices, ranges). */
export function useComfyNodes(classes: string[], enabled = true) {
  const sorted = [...classes].sort();
  return useQuery({
    queryKey: gk.nodes(sorted),
    queryFn: () => api.get<ComfyNodes>(`/api/comfy/nodes?${sorted.map((c) => `class=${encodeURIComponent(c)}`).join("&")}`),
    enabled: enabled && sorted.length > 0,
    staleTime: 60_000,
    placeholderData: keepPreviousData,
  });
}

export function useWorkflows() {
  return useQuery({ queryKey: gk.workflows, queryFn: () => api.get<WorkflowTemplate[]>("/api/workflows") });
}

export function useWorkflow(id: number | undefined) {
  return useQuery({
    queryKey: gk.workflow(id ?? 0),
    queryFn: () => api.get<WorkflowDetail>(`/api/workflows/${id}`),
    enabled: !!id,
  });
}

export function useWorkflowVersion(id: number | undefined) {
  return useQuery({
    queryKey: gk.version(id ?? 0),
    queryFn: () => api.get<WorkflowVersion>(`/api/workflow-versions/${id}`),
    enabled: !!id,
    staleTime: Infinity, // versions never change
  });
}

export function useExperiments() {
  return useQuery({ queryKey: gk.experiments, queryFn: () => api.get<ExperimentView[]>("/api/experiments") });
}

export function useExperiment(id: number) {
  return useQuery({
    queryKey: gk.experiment(id),
    queryFn: () => api.get<ExperimentView>(`/api/experiments/${id}`),
    enabled: id > 0,
    retry: false,
  });
}

export function useGenerations(experimentId: number, moved: boolean) {
  return useQuery({
    queryKey: gk.generations(experimentId, moved),
    queryFn: () => api.get<Generation[]>(`/api/experiments/${experimentId}/generations${moved ? "?moved=1" : ""}`),
    enabled: experimentId > 0,
    placeholderData: keepPreviousData,
  });
}

export function useGeneration(id: number | undefined) {
  return useQuery({
    queryKey: gk.generation(id ?? 0),
    queryFn: () => api.get<GenerationDetail>(`/api/generations/${id}`),
    enabled: !!id,
  });
}

export function useGenerationRuns(experimentId: number) {
  return useQuery({
    queryKey: gk.runs(experimentId),
    queryFn: () => api.get<GenerationRun[]>(`/api/experiments/${experimentId}/runs`),
    enabled: experimentId > 0,
  });
}

export function usePlan(experimentId: number, req: GenerateRequest, enabled: boolean) {
  return useQuery({
    queryKey: gk.plan(experimentId, req),
    queryFn: () => api.post<PlanSummary>(`/api/experiments/${experimentId}/plan`, { request: req }),
    enabled: enabled && experimentId > 0,
    placeholderData: keepPreviousData,
  });
}

export function useMoveGenerations() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { ids: number[]; tags: string[] }) => api.post<MoveResult>("/api/generations/move", v),
    onSuccess: () => invalidateTopics(qc, ["generations", "experiments", "images", "tags"]),
  });
}

export function useDiscardGenerations() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (ids: number[]) => api.post<{ discarded: number }>("/api/generations/discard", { ids }),
    onSuccess: () => invalidateTopics(qc, ["generations", "experiments"]),
  });
}

export function useSaveGenerationWorkflow() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: number; name: string }) =>
      api.post<WorkflowDetail>(`/api/generations/${v.id}/save-workflow`, { name: v.name }),
    onSuccess: () => invalidateTopics(qc, ["workflows"]),
  });
}
