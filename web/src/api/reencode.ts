// Queries and mutations for re-encoding images.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, tagged, versioned } from "./client";
import { invalidateTopics } from "./hooks";
import type {
  ImageQuery,
  ReencodeBatch,
  ReencodeDecided,
  ReencodeDetail,
  ReencodeEstimate,
  ReencodeSettings,
  Sort,
} from "./types";

export const rk = {
  batches: ["reencode"] as const,
  batch: (id: number) => ["reencode-batch", id] as const,
};

/** The images to re-encode: ids, or a query in a sort order. */
export interface ReencodeImages {
  ids?: number[];
  query?: ImageQuery;
  sort?: Sort;
}

/** A result awaiting review. */
export const resultUrl = (batch: number, it: { imageId: number; newSha256?: string }) =>
  tagged(versioned(`/api/reencode/${batch}/result/${it.imageId}`, it.newSha256));

/** An image's original, as any browser shows it upright and unaltered. */
export const originalViewUrl = (image: number) => tagged(`/api/images/${image}/original?view=1`);

export function useReencodes() {
  return useQuery({ queryKey: rk.batches, queryFn: () => api.get<ReencodeBatch[]>("/api/reencode") });
}

export function useReencode(id: number) {
  return useQuery({
    queryKey: rk.batch(id),
    queryFn: () => api.get<ReencodeDetail>(`/api/reencode/${id}`),
    enabled: id > 0,
  });
}

export function useCreateReencode() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: ReencodeImages & { settings: ReencodeSettings; mode: string }) => api.post<ReencodeBatch>("/api/reencode", v),
    onSuccess: () => invalidateTopics(qc, ["reencode"]),
  });
}

export function useTryReencode() {
  return useMutation({
    mutationFn: (v: ReencodeImages & { settings: ReencodeSettings }) => api.post<ReencodeEstimate>("/api/reencode/try", v),
  });
}

export function useDecide(batch: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { ids?: number[]; replace: boolean }) => api.post<ReencodeDecided>(`/api/reencode/${batch}/decide`, v),
    onSuccess: () => invalidateTopics(qc, ["reencode", "images"]),
  });
}

function useBatchAction(action: "resume" | "stop") {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (batch: number) => api.post(`/api/reencode/${batch}/${action}`),
    onSuccess: () => invalidateTopics(qc, ["reencode"]),
  });
}

export const useResumeReencode = () => useBatchAction("resume");
export const useStopReencode = () => useBatchAction("stop");

export function useDeleteReencode() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (batch: number) => api.del(`/api/reencode/${batch}`),
    onSuccess: () => invalidateTopics(qc, ["reencode"]),
  });
}
