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
  return parse<T>(await fetch(path, init));
}

/** Sends bytes as the request body (file contents), expecting JSON back. */
export async function sendBytes<T>(method: string, path: string, body: BodyInit, headers: Record<string, string> = {}): Promise<T> {
  return parse<T>(await fetch(path, { method, credentials: "same-origin", headers, body }));
}

async function parse<T>(res: Response): Promise<T> {
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

/**
 * The open bag's tag, from the page the server sent. Image URLs carry it,
 * since browsers keep thumbnails for good: image 5 of another bag, served
 * on the same address another day, is another picture. (The Vite dev
 * server's page has none; images are then cached for the page's life.)
 */
export const bagTag =
  (typeof document !== "undefined" && document.querySelector<HTMLMetaElement>('meta[name="photobag-bag"]')?.content) ||
  `dev-${Date.now().toString(36)}`;

/** Adds the bag's tag to an image URL. */
export const tagged = (url: string) => `${url}${url.includes("?") ? "&" : "?"}bag=${bagTag}`;

/**
 * Adds a content version to the URL of something whose id is used again
 * once it is deleted (generations, re-encodes, files): within one page
 * browsers show an image already loaded from a URL again without asking
 * the server, whatever it says about caching.
 */
export const versioned = (url: string, sha: string | undefined) =>
  sha ? `${url}${url.includes("?") ? "&" : "?"}v=${sha.slice(0, 16)}` : url;

export const thumbUrl = (id: number) => tagged(`/api/images/${id}/thumb`);
/** The thumbnail of some content (a generation, a re-encode result). */
export const shaThumbUrl = (sha: string) => tagged(`/api/thumbs/${sha}`);
export const previewUrl = (id: number, size = 1600) => tagged(`/api/images/${id}/preview?size=${size}`);
export const originalUrl = (id: number, download = false) =>
  tagged(`/api/images/${id}/original${download ? "?download=1" : ""}`);

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
