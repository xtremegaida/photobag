// Helpers for slide decks and slideshows.
import { originalUrl, previewUrl } from "../api/client";
import type { Image, SlideshowSettings } from "../api/types";

export const DEFAULT_SETTINGS: SlideshowSettings = {
  advance: "timed",
  interval: 5,
  crossfade: true,
  fade: 1,
  fit: "contain",
  background: "#000000",
  loop: true,
  shuffle: false,
  captions: false,
};

/** Settings from storage, with anything missing or broken replaced. */
export function withDefaults(s: Partial<SlideshowSettings> | null | undefined): SlideshowSettings {
  const d = DEFAULT_SETTINGS;
  const out = { ...d, ...(s ?? {}) };
  if (out.advance !== "manual" && out.advance !== "timed") out.advance = d.advance;
  if (!(out.interval >= 1 && out.interval <= 3600)) out.interval = d.interval;
  if (!(out.fade >= 0.1 && out.fade <= 10)) out.fade = d.fade;
  if (!["contain", "cover", "stretch", "center"].includes(out.fit)) out.fit = d.fit;
  if (!/^#[0-9a-f]{6}$/i.test(out.background)) out.background = d.background;
  return out;
}

export function sameSettings(a: SlideshowSettings, b: SlideshowSettings): boolean {
  return (Object.keys(DEFAULT_SETTINGS) as (keyof SlideshowSettings)[]).every((k) => a[k] === b[k]);
}

/** Whether settings would be accepted by the server. */
export function validSettings(s: SlideshowSettings): boolean {
  return sameSettings(withDefaults(s), s);
}

/**
 * How long cross-fades take (ms): never more than three quarters of the
 * time a slide shows, so each settles before the next.
 */
export function fadeMillis(s: SlideshowSettings): number {
  if (!s.crossfade) return 0;
  const fade = s.advance === "timed" ? Math.min(s.fade, s.interval * 0.75) : s.fade;
  return Math.round(fade * 1000);
}

/** A one-line summary of how a deck plays. */
export function describeSettings(s: SlideshowSettings): string {
  const parts = [s.advance === "timed" ? `every ${seconds(s.interval)}` : "advance by hand"];
  if (s.crossfade) parts.push(`cross-fade ${seconds(s.fade)}`);
  parts.push({ contain: "fit", cover: "fill", stretch: "stretch", center: "actual size" }[s.fit]);
  if (s.shuffle) parts.push("shuffled");
  if (!s.loop) parts.push("once");
  const text = parts.join(" · ");
  return text[0].toUpperCase() + text.slice(1);
}

export function seconds(n: number): string {
  return `${Number.isInteger(n) ? n : n.toFixed(1)} s`;
}

/**
 * Moves the members of ids found in order, keeping their order, to just
 * before before (or, when before is 0, missing, or itself moved with
 * nothing unmoved after it, to the end). The server does the same.
 */
export function moveIds(order: number[], ids: number[], before: number): number[] {
  const moving = new Set(ids);
  const block = order.filter((x) => moving.has(x));
  let anchor = 0;
  const at = before ? order.indexOf(before) : -1;
  if (at >= 0) anchor = order.slice(at).find((x) => !moving.has(x)) ?? 0;
  const out: number[] = [];
  for (const x of order) {
    if (moving.has(x)) continue;
    if (x === anchor) out.push(...block);
    out.push(x);
  }
  if (!anchor) out.push(...block);
  return out;
}

/** A shuffled copy (Fisher–Yates). */
export function shuffled<T>(xs: T[], random = Math.random): T[] {
  const out = [...xs];
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(random() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

/**
 * The order to show n slides in, as indexes, beginning with start: the
 * deck order, or start and then the rest shuffled.
 */
export function playOrder(n: number, start: number, shuffle: boolean, random = Math.random): number[] {
  const all = Array.from({ length: n }, (_, i) => i);
  if (!shuffle || n < 2) return all;
  const first = Math.min(Math.max(0, start), n - 1);
  return [first, ...shuffled(all.filter((i) => i !== first), random)];
}

/** A fresh shuffle for the next time round, not starting with last. */
export function reshuffle(n: number, last: number, random = Math.random): number[] {
  const order = shuffled(
    Array.from({ length: n }, (_, i) => i),
    random,
  );
  if (n > 1 && order[0] === last) [order[0], order[n - 1]] = [order[n - 1], order[0]];
  return order;
}

/** Formats a browser shows as they are (with animation and transparency). */
const SHOW_ORIGINAL = new Set(["gif", "png", "webp"]);
const MAX_ORIGINAL = 24 << 20;

/**
 * What to show for an image: the original for GIF, PNG and WebP (so
 * animations play and transparency shows the background), unless large;
 * otherwise a preview sized for the screen.
 */
export function slideSource(im: Pick<Image, "id" | "format" | "size">, screenPixels: number): string {
  if (SHOW_ORIGINAL.has(im.format) && im.size <= MAX_ORIGINAL) return originalUrl(im.id);
  return previewUrl(im.id, screenPixels > 1700 ? 2560 : 1600);
}

/** The slideshow of a deck, or of gallery filters, from slide start. */
export function slideshowPath(to: { deck?: number; params?: URLSearchParams; start?: number }): string {
  const sp = new URLSearchParams(to.params);
  if (to.deck) sp.set("deck", String(to.deck));
  if (to.start) sp.set("start", String(to.start));
  const q = sp.toString();
  return `/slideshow${q ? `?${q}` : ""}`;
}
