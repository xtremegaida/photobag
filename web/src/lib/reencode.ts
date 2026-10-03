// Re-encoding helpers: settings, their descriptions, and how results read.
import type { ReencodeItem, ReencodeSettings } from "../api/types";
import { formatBytes } from "./format";

export const DEFAULT_REENCODE: ReencodeSettings = {
  format: "webp",
  lossless: true,
  quality: 80,
  effort: 4,
  progressive: true,
  chroma444: false,
  maxWidth: 0,
  maxHeight: 0,
  keepMetadata: true,
  onlySmaller: true,
};

const formatNames: Record<string, string> = { jpeg: "JPEG", png: "PNG", webp: "WebP", gif: "GIF", bmp: "BMP", tiff: "TIFF" };

/** A format's display name. */
export const formatName = (f: string) => formatNames[f] ?? f.toUpperCase();

/** Whether the settings scale images down. */
export const scales = (s: ReencodeSettings) => s.maxWidth > 0 || s.maxHeight > 0;

/** Whether the settings are lossless (what the pixels hold cannot change). */
export const isLossless = (s: ReencodeSettings) => s.format === "png" || (s.format === "webp" && s.lossless);

/** Replace straight away when nothing visible can change; compare first otherwise. */
export const defaultMode = (s: ReencodeSettings): "replace" | "review" => (isLossless(s) && !scales(s) ? "replace" : "review");

/** "WebP q80, at most 2000 wide", as the server says it. */
export function describeReencode(s: ReencodeSettings): string {
  let out = s.format === "png" ? "PNG" : s.format === "webp" && s.lossless ? "lossless WebP" : `${formatName(s.format)} q${s.quality}`;
  if (s.maxWidth > 0 && s.maxHeight > 0) out += `, at most ${s.maxWidth}×${s.maxHeight}`;
  else if (s.maxWidth > 0) out += `, at most ${s.maxWidth} wide`;
  else if (s.maxHeight > 0) out += `, at most ${s.maxHeight} high`;
  return out;
}

/** The description as a title ("Lossless WebP"). */
export const reencodeTitle = (s: ReencodeSettings) => {
  const d = describeReencode(s);
  return d.charAt(0).toUpperCase() + d.slice(1);
};

/** "−74%" (or "+12%") for a size change. */
export function sizeChange(oldSize: number, newSize: number): string {
  if (!oldSize) return "";
  const pct = Math.round(((newSize - oldSize) / oldSize) * 100);
  return pct <= 0 ? `−${-pct}%` : `+${pct}%`;
}

/** "4.2 MB → 1.1 MB (−74%)". */
export const sizeStory = (oldSize: number, newSize: number) =>
  `${formatBytes(oldSize)} → ${formatBytes(newSize)} (${sizeChange(oldSize, newSize)})`;

/**
 * How closely a result matches its original, in words. PSNR is a rough
 * guide: above about 45 dB differences are invisible, below 32 they show.
 */
export function psnrLabel(psnr: number | undefined): { label: string; color: string } {
  if (psnr === undefined) return { label: "identical pixels", color: "teal" };
  if (psnr >= 45) return { label: "excellent", color: "teal" };
  if (psnr >= 38) return { label: "good", color: "green" };
  if (psnr >= 32) return { label: "fair", color: "yellow" };
  return { label: "visible loss", color: "red" };
}

export type ItemFilter = "ready" | "replaced" | "kept" | "other" | "all";
export type ItemSort = "order" | "saving" | "quality" | "name";

/** The items a filter shows, in a sort order. */
export function pickItems(items: ReencodeItem[], filter: ItemFilter, sort: ItemSort): ReencodeItem[] {
  const shown = items.filter((it) =>
    filter === "all"
      ? true
      : filter === "other"
        ? it.status === "skipped" || it.status === "failed" || it.status === "pending"
        : it.status === filter,
  );
  const saving = (it: ReencodeItem) => (it.newSize ? it.oldSize - it.newSize : -Infinity);
  switch (sort) {
    case "saving":
      return shown.sort((a, b) => saving(b) - saving(a));
    case "quality":
      return shown.sort((a, b) => (a.psnr ?? Infinity) - (b.psnr ?? Infinity));
    case "name":
      return shown.sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
  }
  return shown.sort((a, b) => a.ord - b.ord);
}
