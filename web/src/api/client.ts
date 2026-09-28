// Thin fetch wrapper for the PhotoBag JSON API.

export class ApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: "same-origin" };
  if (body !== undefined) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  const res = await fetch(path, init);
  if (!res.ok) {
    let message = res.statusText || `HTTP ${res.status}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) message = j.error;
    } catch {
      // not JSON
    }
    throw new ApiError(res.status, message);
  }
  return (await res.json()) as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body: unknown = {}) => request<T>("POST", path, body),
  patch: <T>(path: string, body: unknown) => request<T>("PATCH", path, body),
  put: <T>(path: string, body: unknown) => request<T>("PUT", path, body),
  del: <T>(path: string) => request<T>("DELETE", path),
};

/**
 * In dev mode the SPA is served by Vite, so the ?token= of the launch URL
 * reaches the page rather than the Go server: exchange it for the session
 * cookie and strip it from the address bar.
 */
export async function exchangeToken(): Promise<void> {
  const url = new URL(window.location.href);
  const token = url.searchParams.get("token");
  if (!token) return;
  await fetch("/api/auth", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }),
  });
  url.searchParams.delete("token");
  window.history.replaceState(null, "", url.pathname + url.search + url.hash);
}

export const thumbUrl = (id: number) => `/api/images/${id}/thumb`;
export const previewUrl = (id: number, size = 1600) => `/api/images/${id}/preview?size=${size}`;
export const originalUrl = (id: number, download = false) =>
  `/api/images/${id}/original${download ? "?download=1" : ""}`;

/** Warm the browser cache for an image URL. */
export function preload(url: string) {
  const img = new Image();
  img.decoding = "async";
  img.src = url;
}

export function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}
