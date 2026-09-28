import { IconCategory, IconLetterCase, IconTags, IconTextCaption, type Icon } from "@tabler/icons-react";

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
    description: "Danbooru-style tags such as 1girl, outdoors or long_hair, kept with the image and optionally added as tags.",
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

/** Mean time per request so far, and the time left, for a running job. */
export function eta(startedAt: number | undefined, done: number, total: number): string | undefined {
  if (!startedAt || done <= 0 || done >= total) return undefined;
  const elapsed = Date.now() - startedAt;
  const left = (elapsed / done) * (total - done);
  return formatDuration(left);
}

export function formatDuration(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}
