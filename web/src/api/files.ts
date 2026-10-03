// Queries and mutations for the bag's ordinary files.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, sendBytes, versioned } from "./client";
import { invalidateTopics } from "./hooks";
import type { FileDeleteResult, FileListing, FileMoveResult, FileNode, FilePutResult, FileSummary } from "./types";

export const fk = {
  at: (path: string) => ["files", "at", path] as const,
  summary: ["file-summary"] as const,
  folders: ["file-folders"] as const,
};

/** Joins path segments into a slash-separated path. */
export const joinPath = (parts: string[]) => parts.filter(Boolean).join("/");

/** The app route showing a file or folder (path segments escaped). */
export const filesRoute = (path: string) =>
  "/files" + (path ? "/" + path.split("/").filter(Boolean).map(encodeURIComponent).join("/") : "");

export const fileContentUrl = (node: Pick<FileNode, "id" | "sha256">, download = false) =>
  versioned(`/api/files/${node.id}/content${download ? "?download=1" : ""}`, node.sha256);

/** Serves a file by its path, which lets relative links in notes work. */
export const fileRawUrl = (path: string) =>
  "/api/files-raw/" + path.split("/").filter(Boolean).map(encodeURIComponent).join("/");

/** Downloads files and folders (everything, with no ids) as one zip. */
export const zipUrl = (ids: number[]) => "/api/files/zip" + (ids.length ? "?" + ids.map((id) => `id=${id}`).join("&") : "");

/** The node at a path (a folder with its children, or a file). */
export function useFileListing(path: string) {
  return useQuery({
    queryKey: fk.at(path),
    queryFn: () => api.get<FileListing>(`/api/files?path=${encodeURIComponent(path)}`),
    placeholderData: keepPreviousData,
    retry: (n, e) => n < 2 && (e as { status?: number }).status !== 404,
  });
}

export function useFileSummary() {
  return useQuery({ queryKey: fk.summary, queryFn: () => api.get<FileSummary>("/api/files/summary") });
}

/** Every folder, for choosing where to move things. */
export function useFileFolders(enabled = true) {
  return useQuery({ queryKey: fk.folders, queryFn: () => api.get<FileNode[]>("/api/files/folders"), enabled });
}

function useFilesMutation<V, R>(fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => invalidateTopics(qc, ["files"]) });
}

export const useMakeFolder = () =>
  useFilesMutation((v: { parent: number; name: string }) => api.post<FileNode>("/api/files/folders", v));

export const useRenameFile = () =>
  useFilesMutation((v: { id: number; name: string }) => api.patch<FileNode>(`/api/files/${v.id}`, { name: v.name }));

export const useMoveFiles = () =>
  useFilesMutation((v: { ids: number[]; parent: number }) => api.post<FileMoveResult>("/api/files/move", v));

export const useDeleteFiles = () =>
  useFilesMutation((ids: number[]) => api.post<FileDeleteResult>("/api/files/delete", { ids }));

/** The text of a version of a file, as shown and edited. */
export const textKey = (id: number, sha: string) => ["file-text", id, sha] as const;

/**
 * Saves an edit over a file. With ifSha, the server refuses (412) if the
 * file is no longer that version.
 */
export const saveFileContent = (id: number, body: Uint8Array<ArrayBuffer>, ifSha?: string) =>
  sendBytes<FileNode>("PUT", `/api/files/${id}/content`, body, ifSha ? { "If-Match": `"${ifSha}"` } : {});

/** Makes a file in a folder; the server refuses (409) a name already taken. */
export const createFile = (parent: number, name: string, body: Uint8Array<ArrayBuffer> = new Uint8Array()) =>
  sendBytes<FilePutResult>(
    "PUT",
    "/api/files/upload?" + new URLSearchParams({ parent: String(parent), path: name, conflict: "fail" }).toString(),
    body,
  );

/** Which of these paths (relative to a folder) are already taken. */
export const checkExisting = (parent: number, paths: string[]) =>
  api.post<{ existing: string[] }>("/api/files/check", { parent, paths }).then((r) => r.existing);
