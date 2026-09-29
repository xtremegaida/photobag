import { IconCategory, IconLetterCase, IconTags, IconTextCaption, type Icon } from "@tabler/icons-react";
import type { AnalysisSettings, TaggerStatus } from "../api/types";

export type PipelineId = "caption" | "ocr" | "danbooru" | "category";

export interface PipelineInfo {
  id: PipelineId;
  label: string;
  /** Heading for the result in an image's details. */
  heading: string;
  description: string;
  icon: Icon;
}

export const PIPELINES: PipelineInfo[] = [
  {
    id: "caption",
    label: "Caption",
    heading: "Caption",
    description:
      "A short description of what is happening in the image, for people who cannot see it. Also used as the image's alt text.",
    icon: IconTextCaption,
  },
  {
    id: "ocr",
    label: "Text in image (OCR)",
    heading: "Text in image",
    description: "Any text that can be read in the image, or blank when there is none. Searchable from the library.",
    icon: IconLetterCase,
  },
  {
    id: "danbooru",
    label: "Danbooru tags",
    heading: "Danbooru tags",
    description:
      "Danbooru-style tags such as 1girl, outdoors or long_hair, from the vision model or a WD tagger, kept with the image and optionally added as tags.",
    icon: IconTags,
  },
  {
    id: "category",
    label: "Category",
    heading: "Category",
    description: "One category, or a main and a sub category, chosen from your list or by the model, and added as tags.",
    icon: IconCategory,
  },
];

export const pipelineInfo = (id: string) => PIPELINES.find((p) => p.id === id) ?? PIPELINES[0];

/** Whether a pipeline uses the WD tagger instead of the vision model. */
export function usesTagger(s: AnalysisSettings, id: string): boolean {
  return id === "danbooru" && s.danbooru.source === "tagger";
}

/** Why a pipeline cannot run with these settings, or null when it can. */
export function notReady(s: AnalysisSettings, id: string, tagger: TaggerStatus | undefined): string | null {
  if (!usesTagger(s, id)) return s.endpoint ? null : "No vision model is connected (Connection tab).";
  if (!s.danbooru.tagger.local) return s.danbooru.tagger.endpoint ? null : "No tagger address is set (Pipelines & prompts → Danbooru tags).";
  if (!tagger) return null;
  return tagger.installation.ready ? null : (tagger.installation.problem ?? "The local tagger is not installed.");
}

/** Mean time per request so far, and the time left, for a running job. */
export function eta(startedAt: number | undefined, done: number, total: number): string | undefined {
  if (!startedAt || done <= 0 || done >= total) return undefined;
  const elapsed = Date.now() - startedAt;
  const left = (elapsed / done) * (total - done);
  return formatDuration(left);
}

/** Milliseconds under a second, otherwise as formatDuration. */
export function formatMillis(ms: number): string {
  return ms < 1000 ? `${Math.round(ms)} ms` : formatDuration(ms);
}

export function formatDuration(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}
