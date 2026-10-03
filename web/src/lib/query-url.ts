import type { ImageQuery, Sort } from "../api/types";

export interface GalleryState {
  query: ImageQuery;
  sort: Sort;
}

export const SORT_FIELDS = [
  { value: "imported", label: "Date imported" },
  { value: "taken", label: "Date taken" },
  { value: "name", label: "Name" },
  { value: "size", label: "File size" },
  { value: "pixels", label: "Resolution" },
  { value: "score", label: "Score" },
  { value: "similar", label: "Visual similarity" },
  { value: "random", label: "Shuffle" },
] as const;

const DEFAULT_SORT: Sort = { field: "imported", desc: true };

const MB = 1 << 20;

/** The formats images are stored in, for filtering. */
export const IMAGE_FORMATS = [
  { value: "jpeg", label: "JPEG" },
  { value: "png", label: "PNG" },
  { value: "webp", label: "WebP" },
  { value: "gif", label: "GIF" },
  { value: "tiff", label: "TIFF" },
  { value: "bmp", label: "BMP" },
];

/** Smallest-file-size choices for filtering, in MB. */
export const MIN_SIZES = [0.5, 1, 2, 5, 10, 20, 50];

/** Reads gallery filters and sort from the URL (tag=…&any=…&not=…&q=…&id=…). */
export function parseGallery(sp: URLSearchParams, trash = false): GalleryState {
  const query: ImageQuery = {};
  const all = sp.getAll("tag");
  const any = sp.getAll("any");
  const none = sp.getAll("not");
  if (all.length) query.tagsAll = all;
  if (any.length) query.tagsAny = any;
  if (none.length) query.tagsNone = none;
  if (sp.get("untagged") === "1") query.untagged = true;
  const q = sp.get("q");
  if (q) query.nameGlob = q;
  const text = sp.get("text");
  if (text) query.text = text;
  const types = sp.getAll("type").filter((t) => IMAGE_FORMATS.some((f) => f.value === t));
  if (types.length) query.formats = types;
  const minMB = Number(sp.get("minmb"));
  if (minMB > 0) query.minSize = Math.round(minMB * MB);
  const ids = sp.getAll("id").map(Number).filter((n) => Number.isInteger(n) && n > 0);
  if (ids.length) query.ids = ids;
  if (trash) query.scope = "trash";

  const field = sp.get("sort") ?? DEFAULT_SORT.field;
  const sort: Sort = { field, desc: sp.has("desc") ? sp.get("desc") === "1" : field === "imported" };
  const metric = Number(sp.get("metric"));
  if (metric > 0) sort.metricId = metric;
  const similar = Number(sp.get("similar"));
  if (similar > 0) sort.similarTo = similar;
  const seed = Number(sp.get("seed"));
  if (seed) sort.seed = seed;
  if (field === "score" && !sort.metricId) sort.field = DEFAULT_SORT.field;
  return { query, sort };
}

/** Writes gallery filters and sort to URL parameters (inverse of parseGallery). */
export function galleryParams({ query, sort }: GalleryState): URLSearchParams {
  const sp = new URLSearchParams();
  for (const t of query.tagsAll ?? []) sp.append("tag", t);
  for (const t of query.tagsAny ?? []) sp.append("any", t);
  for (const t of query.tagsNone ?? []) sp.append("not", t);
  if (query.untagged) sp.set("untagged", "1");
  if (query.nameGlob) sp.set("q", query.nameGlob);
  if (query.text) sp.set("text", query.text);
  for (const t of query.formats ?? []) sp.append("type", t);
  if (query.minSize) sp.set("minmb", String(+(query.minSize / MB).toFixed(2)));
  for (const id of query.ids ?? []) sp.append("id", String(id));
  const field = sort.field || "imported";
  if (field !== DEFAULT_SORT.field) sp.set("sort", field);
  const defaultDesc = field === "imported";
  if (!!sort.desc !== defaultDesc) sp.set("desc", sort.desc ? "1" : "0");
  if (sort.metricId) sp.set("metric", String(sort.metricId));
  if (sort.similarTo) sp.set("similar", String(sort.similarTo));
  if (sort.seed) sp.set("seed", String(sort.seed));
  return sp;
}

/** Whether the query filters anything (beyond scope). */
export function hasFilters(q: ImageQuery): boolean {
  return !!(
    q.tagsAll?.length ||
    q.tagsAny?.length ||
    q.tagsNone?.length ||
    q.untagged ||
    q.nameGlob ||
    q.text?.trim() ||
    q.formats?.length ||
    q.minSize ||
    q.ids?.length
  );
}
