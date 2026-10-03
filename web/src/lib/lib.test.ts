import { describe, expect, it } from "vitest";
import { createBatcher } from "../api/batcher";
import type { ReencodeItem } from "../api/types";
import { decodeText, isClutter, resolveNoteLink, splitExt } from "./files";
import { DEFAULT_REENCODE, defaultMode, describeReencode, pickItems, psnrLabel, sizeChange } from "./reencode";
import { formatBytes, formatTaken, percent } from "./format";
import type { AnalysisSettings, TaggerStatus } from "../api/types";
import { defaultSweep, gridLayout, isSeedName, rangeValues, requestFrom, valueCount, valueKind } from "./generate";
import { eta, formatDuration, formatMillis, notReady, usesTagger } from "./pipelines";
import { galleryParams, hasFilters, parseGallery } from "./query-url";
import { rangeBetween, toggled } from "./selection";
import {
  DEFAULT_SETTINGS,
  describeSettings,
  fadeMillis,
  moveIds,
  playOrder,
  reshuffle,
  slideSource,
  slideshowPath,
  validSettings,
  withDefaults,
} from "./slideshow";

describe("selection", () => {
  const order = [10, 20, 30, 40, 50];
  it("selects ranges in either direction", () => {
    expect(rangeBetween(order, 20, 40)).toEqual([20, 30, 40]);
    expect(rangeBetween(order, 50, 30)).toEqual([30, 40, 50]);
  });
  it("falls back to the clicked item without an anchor", () => {
    expect(rangeBetween(order, null, 30)).toEqual([30]);
    expect(rangeBetween(order, 99, 30)).toEqual([30]);
    expect(rangeBetween(order, 10, 99)).toEqual([]);
  });
  it("toggles immutably", () => {
    const a = new Set([1, 2]);
    const b = toggled(a, 2);
    expect([...b]).toEqual([1]);
    expect([...a]).toEqual([1, 2]);
    expect([...toggled(b, 3)].sort()).toEqual([1, 3]);
  });
});

describe("gallery URL state", () => {
  it("round-trips filters and sort", () => {
    const state = parseGallery(
      new URLSearchParams("tag=Holiday&tag=2019&not=blurry&q=IMG_*&sort=score&metric=3&desc=1"),
    );
    expect(state.query).toEqual({ tagsAll: ["Holiday", "2019"], tagsNone: ["blurry"], nameGlob: "IMG_*" });
    expect(state.sort).toEqual({ field: "score", desc: true, metricId: 3 });
    expect(parseGallery(galleryParams(state))).toEqual(state);
  });
  it("defaults to newest imports first", () => {
    const state = parseGallery(new URLSearchParams(""));
    expect(state.sort).toEqual({ field: "imported", desc: true });
    expect(galleryParams(state).toString()).toBe("");
  });
  it("ignores a score sort without a metric", () => {
    expect(parseGallery(new URLSearchParams("sort=score")).sort.field).toBe("imported");
  });
  it("adds the trash scope", () => {
    expect(parseGallery(new URLSearchParams("any=a"), true).query).toEqual({ tagsAny: ["a"], scope: "trash" });
  });
  it("detects filters", () => {
    expect(hasFilters({})).toBe(false);
    expect(hasFilters({ scope: "trash" })).toBe(false);
    expect(hasFilters({ untagged: true })).toBe(true);
    expect(hasFilters({ text: "  " })).toBe(false);
    expect(hasFilters({ text: "dog" })).toBe(true);
  });
  it("keeps the description search", () => {
    const state = parseGallery(new URLSearchParams('text="long hair" beach'));
    expect(state.query).toEqual({ text: '"long hair" beach' });
    expect(parseGallery(galleryParams(state))).toEqual(state);
  });
});

describe("format", () => {
  it("formats bytes", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(250 * 1024 * 1024)).toBe("250 MB");
  });
  it("formats EXIF times", () => {
    expect(formatTaken("2019-07-04T15:22:10", "+02:00")).toBe("2019-07-04 15:22:10 (UTC+02:00)");
    expect(formatTaken(undefined)).toBe("–");
  });
  it("formats durations and estimates time left", () => {
    expect(formatDuration(4_400)).toBe("4s");
    expect(formatDuration(125_000)).toBe("2m 5s");
    expect(formatDuration(3_720_000)).toBe("1h 2m");
    expect(eta(Date.now() - 10_000, 1, 3)).toBe("20s");
    expect(eta(undefined, 1, 3)).toBeUndefined();
    expect(eta(Date.now(), 3, 3)).toBeUndefined();
  });
  it("formats percentages", () => {
    expect(percent(0.9)).toBe("90%");
    expect(percent(0.999)).toBe("99.9%");
  });
});

describe("batcher", () => {
  it("coalesces loads into one request and rejects missing keys", async () => {
    const calls: number[][] = [];
    const b = createBatcher<number, string>(async (keys) => {
      calls.push(keys);
      return new Map(keys.filter((k) => k !== 3).map((k) => [k, `v${k}`]));
    });
    const results = await Promise.allSettled([b.load(1), b.load(2), b.load(1), b.load(3)]);
    expect(calls).toEqual([[1, 2, 3]]);
    expect(results.map((r) => (r.status === "fulfilled" ? r.value : "missing"))).toEqual(["v1", "v2", "v1", "missing"]);
  });
});

describe("pipeline readiness", () => {
  const settings = (over: { endpoint?: string; source?: string; local?: boolean; tagger?: string }) =>
    ({
      endpoint: over.endpoint ?? "",
      danbooru: { source: over.source ?? "model", tagger: { local: over.local ?? false, endpoint: over.tagger ?? "" } },
    }) as unknown as AnalysisSettings;
  const status = (ready: boolean) =>
    ({ installation: { ready, problem: ready ? undefined : "The tagger is not installed." } }) as TaggerStatus;

  it("needs the model for model pipelines", () => {
    expect(notReady(settings({}), "caption", undefined)).toMatch(/No vision model/);
    expect(notReady(settings({ endpoint: "http://x/v1" }), "caption", undefined)).toBeNull();
    expect(notReady(settings({ endpoint: "http://x/v1" }), "danbooru", undefined)).toBeNull();
  });
  it("needs a tagger, not the model, for tagger tags", () => {
    const remote = settings({ source: "tagger" });
    expect(usesTagger(remote, "danbooru")).toBe(true);
    expect(usesTagger(remote, "caption")).toBe(false);
    expect(notReady(remote, "danbooru", undefined)).toMatch(/No tagger address/);
    expect(notReady(settings({ source: "tagger", tagger: "host:8000" }), "danbooru", undefined)).toBeNull();
    const local = settings({ source: "tagger", local: true });
    expect(notReady(local, "danbooru", status(false))).toBe("The tagger is not installed.");
    expect(notReady(local, "danbooru", status(true))).toBeNull();
  });
  it("formats short durations in milliseconds", () => {
    expect(formatMillis(230)).toBe("230 ms");
    expect(formatMillis(12_400)).toBe("12s");
  });
});

describe("generation helpers", () => {
  it("expands ranges like the server", () => {
    expect(rangeValues({ from: 25, to: 30, step: 1 })).toEqual([25, 26, 27, 28, 29, 30]);
    expect(rangeValues({ from: 0.1, to: 0.5, step: 0.1 })).toEqual([0.1, 0.2, 0.3, 0.4, 0.5]);
    expect(rangeValues({ from: 30, to: 20, step: 5 })).toEqual([30, 25, 20]);
    expect(rangeValues({ from: 1, to: 2, step: 0 })).toEqual([]);
    expect(valueCount({ node: "KSampler", input: "steps", sweep: { range: { from: 1, to: 4, step: 1 } } })).toBe(4);
    expect(valueCount({ node: "KSampler", input: "steps", value: 3 })).toBe(1);
  });

  it("lays sweeps out as tables", () => {
    expect(gridLayout([], 2)).toBeNull();
    const one = gridLayout([{ node: "KSampler", input: "steps", values: [10, 20, 30] }], 2)!;
    expect(one.columns).toEqual(["10", "20", "30"]);
    expect(one.rows.map((r) => r.label)).toEqual(["#1", "#2"]);
    expect(one.rows[1].cells[2]).toEqual({ combo: 2, repeat: 1 });
    // Two dimensions: the first down the side, the last across; combos
    // run with the first dimension slowest.
    const two = gridLayout(
      [
        { node: "Checkpoint", input: "ckpt_name", values: ["a", "b"] },
        { node: "KSampler", input: "cfg", values: [3, 5, 7] },
      ],
      4,
    )!;
    expect(two.rowTitle).toBe("Checkpoint › ckpt_name");
    expect(two.rows.map((r) => r.label)).toEqual(["a", "b"]);
    expect(two.rows[1].cells.map((c) => c.combo)).toEqual([3, 4, 5]);
    const three = gridLayout(
      [
        { node: "A", input: "x", values: [1, 2] },
        { node: "B", input: "y", values: ["p", "q"] },
        { node: "C", input: "z", values: [true, false] },
      ],
      1,
    )!;
    expect(three.rows.map((r) => r.label)).toEqual(["1 · p", "1 · q", "2 · p", "2 · q"]);
    expect(three.rows[2].cells.map((c) => c.combo)).toEqual([4, 5]);
  });

  it("picks up from a generated image", () => {
    const g = {
      id: 7,
      versionId: 3,
      workflowId: 2,
      workflowName: "T2I",
      applied: [
        { node: "Positive", input: "text", value: "a cat" },
        { node: "KSampler", input: "steps", value: 24, kind: "sweep" },
        { node: "KSampler", input: "seed", value: 99, kind: "seed" },
        { node: "Refiner", input: "noise_seed", value: 5 },
      ],
      combo: 0,
      repeat: 0,
      batchIndex: 0,
      sha256: "",
      format: "png",
      size: 1,
      width: 1,
      height: 1,
      thumbW: 1,
      thumbH: 1,
      millis: 0,
      createdAt: 0,
      current: true,
      templateExists: true,
    };
    const fresh = requestFrom(g, false, 4);
    expect(fresh.overrides.map((o) => o.input)).toEqual(["text", "steps"]);
    expect(fresh.overrides[1]).toEqual({ node: "KSampler", input: "steps", value: 24 });
    expect(fresh).toMatchObject({ workflowId: 2, versionId: undefined, count: 4, seed: "random" });
    const seeded = requestFrom({ ...g, current: false }, true, 4);
    expect(seeded.overrides.map((o) => o.input)).toEqual(["text", "steps", "seed", "noise_seed"]);
    expect(seeded).toMatchObject({ versionId: 3, count: 1 });
    expect(requestFrom({ ...g, templateExists: false }, false, 1).workflowId).toBe(0);
  });

  it("chooses editors and default sweeps", () => {
    expect(valueKind({ name: "sampler_name", type: "COMBO", options: ["a"] }, "a")).toBe("combo");
    expect(valueKind(undefined, 5)).toBe("int");
    expect(valueKind(undefined, 5.5)).toBe("float");
    expect(valueKind(undefined, "a\nb")).toBe("multiline");
    expect(defaultSweep("float", 5, { name: "cfg", type: "FLOAT", step: 0.1, max: 100 })).toEqual({ range: { from: 5, to: 7, step: 1 } });
    // Near the maximum it sweeps down.
    expect(defaultSweep("float", 1, { name: "denoise", type: "FLOAT", step: 0.01, min: 0, max: 1 })).toEqual({
      range: { from: 0.8, to: 1, step: 0.1 },
    });
    expect(defaultSweep("combo", "euler")).toEqual({ values: ["euler"] });
    expect(isSeedName("noise_seed") && isSeedName("SEED") && !isSeedName("seeds")).toBe(true);
  });
});

describe("gallery URL ids", () => {
  it("round-trips chosen images", () => {
    const st = parseGallery(new URLSearchParams("id=173&id=174&id=x&tag=a"));
    expect(st.query.ids).toEqual([173, 174]);
    expect(hasFilters(st.query)).toBe(true);
    expect(galleryParams(st).getAll("id")).toEqual(["173", "174"]);
  });
});

describe("slide decks", () => {
  const order = [1, 2, 3, 4, 5, 6];
  it("moves slides like the server", () => {
    expect(moveIds(order, [5], 2)).toEqual([1, 5, 2, 3, 4, 6]);
    expect(moveIds(order, [5, 2], 1)).toEqual([2, 5, 1, 3, 4, 6]);
    expect(moveIds(order, [1, 2], 0)).toEqual([3, 4, 5, 6, 1, 2]);
    expect(moveIds(order, [2, 3], 3)).toEqual(order);
    expect(moveIds(order, [1, 4], 3)).toEqual([2, 1, 4, 3, 5, 6]);
    expect(moveIds(order, [3, 99], 99)).toEqual([1, 2, 4, 5, 6, 3]);
  });
  it("orders plays", () => {
    const seq = [0.9, 0.1, 0.5, 0.3, 0.7];
    let i = 0;
    const random = () => seq[i++ % seq.length];
    expect(playOrder(5, 2, false)).toEqual([0, 1, 2, 3, 4]);
    const p = playOrder(5, 2, true, random);
    expect(p[0]).toBe(2);
    expect([...p].sort()).toEqual([0, 1, 2, 3, 4]);
    for (let k = 0; k < 20; k++) {
      const r = reshuffle(4, 1);
      expect(r[0]).not.toBe(1);
      expect([...r].sort()).toEqual([0, 1, 2, 3]);
    }
  });
  it("checks and describes settings", () => {
    expect(withDefaults({ interval: 0, fit: "tile" as never, background: "red" })).toEqual(DEFAULT_SETTINGS);
    expect(validSettings({ ...DEFAULT_SETTINGS, interval: 0.5 })).toBe(false);
    expect(describeSettings(DEFAULT_SETTINGS)).toBe("Every 5 s · cross-fade 1 s · fit");
    expect(describeSettings({ ...DEFAULT_SETTINGS, advance: "manual", crossfade: false, fit: "center", loop: false, shuffle: true })).toBe(
      "Advance by hand · actual size · shuffled · once",
    );
    // Fades never outlast most of a slide.
    expect(fadeMillis({ ...DEFAULT_SETTINGS, interval: 2, fade: 3 })).toBe(1500);
    expect(fadeMillis({ ...DEFAULT_SETTINGS, advance: "manual", fade: 3 })).toBe(3000);
    expect(fadeMillis({ ...DEFAULT_SETTINGS, crossfade: false })).toBe(0);
  });
  it("shows originals where they animate or have transparency", () => {
    expect(slideSource({ id: 7, format: "gif", size: 5 << 20 }, 3000)).toBe("/api/images/7/original");
    expect(slideSource({ id: 7, format: "png", size: 90 << 20 }, 3000)).toBe("/api/images/7/preview?size=2560");
    expect(slideSource({ id: 7, format: "jpeg", size: 1000 }, 1200)).toBe("/api/images/7/preview?size=1600");
  });
  it("links slideshows", () => {
    expect(slideshowPath({ deck: 4, start: 3 })).toBe("/slideshow?deck=4&start=3");
    expect(slideshowPath({ params: new URLSearchParams("tag=cats&sort=name") })).toBe("/slideshow?tag=cats&sort=name");
  });
});

describe("files", () => {
  it("resolves the links of notes against their folder", () => {
    expect(resolveNoteLink("docs/guide.md", "img/a%20b.png")).toBe("docs/img/a b.png");
    expect(resolveNoteLink("docs/guide.md", "../README.md#intro")).toBe("README.md");
    expect(resolveNoteLink("docs/guide.md", "./other.md?x=1")).toBe("docs/other.md");
    expect(resolveNoteLink("guide.md", "../escape.md")).toBeNull();
    for (const external of ["https://example.com/a.png", "mailto:a@b.c", "#section", "/api/x", "//host/x", ""]) {
      expect(resolveNoteLink("docs/guide.md", external)).toBeNull();
    }
  });
  it("guesses text encodings", () => {
    const enc = (s: string) => new TextEncoder().encode(s).buffer;
    expect(decodeText(enc("héllo"))).toEqual({ text: "héllo", encoding: "UTF-8" });
    expect(decodeText(new Uint8Array([0xef, 0xbb, 0xbf, 0x68, 0x69]).buffer)).toEqual({ text: "hi", encoding: "UTF-8" });
    expect(decodeText(new Uint8Array([0xff, 0xfe, 0x68, 0x00, 0x69, 0x00]).buffer)).toEqual({ text: "hi", encoding: "UTF-16" });
    expect(decodeText(new Uint8Array([0x63, 0x61, 0x66, 0xe9]).buffer)).toEqual({ text: "café", encoding: "Windows-1252" });
    // A multi-byte character cut off by a partial read stays UTF-8.
    const cut = new Uint8Array([...new TextEncoder().encode("abcdefé")].slice(0, -1));
    expect(decodeText(cut.buffer).encoding).toBe("UTF-8");
  });
  it("splits names and spots clutter", () => {
    expect(splitExt("notes.final.md")).toEqual(["notes.final", ".md"]);
    expect(splitExt(".gitignore")).toEqual([".gitignore", ""]);
    expect(splitExt("README")).toEqual(["README", ""]);
    expect(isClutter(".DS_Store") && isClutter("Thumbs.db") && !isClutter("notes.md")).toBe(true);
  });
});

describe("re-encoding", () => {
  it("describes settings and picks the mode", () => {
    expect(describeReencode(DEFAULT_REENCODE)).toBe("lossless WebP");
    expect(defaultMode(DEFAULT_REENCODE)).toBe("replace");
    const lossy = { ...DEFAULT_REENCODE, lossless: false, format: "jpeg" as const, quality: 85, maxWidth: 2000 };
    expect(describeReencode(lossy)).toBe("JPEG q85, at most 2000 wide");
    expect(defaultMode(lossy)).toBe("review");
    expect(defaultMode({ ...DEFAULT_REENCODE, format: "png", maxHeight: 1000 })).toBe("review");
  });
  it("reads results", () => {
    expect(sizeChange(1000, 260)).toBe("−74%");
    expect(sizeChange(1000, 1120)).toBe("+12%");
    expect(psnrLabel(undefined).label).toBe("identical pixels");
    expect(psnrLabel(40).label).toBe("good");
    expect(psnrLabel(28).color).toBe("red");
    const item = (imageId: number, status: string, oldSize: number, newSize?: number, psnr?: number) =>
      ({ imageId, name: `n${imageId}`, ord: imageId, status, oldSize, newSize, psnr, notes: [] }) as unknown as ReencodeItem;
    const items = [item(1, "ready", 100, 90, 40), item(2, "ready", 100, 10, 30), item(3, "skipped", 50), item(4, "replaced", 10, 5)];
    expect(pickItems(items, "ready", "saving").map((i) => i.imageId)).toEqual([2, 1]);
    expect(pickItems(items, "ready", "quality").map((i) => i.imageId)).toEqual([2, 1]);
    expect(pickItems(items, "other", "order").map((i) => i.imageId)).toEqual([3]);
    expect(pickItems(items, "all", "order")).toHaveLength(4);
  });
  it("keeps type and size filters in the URL", () => {
    const st = parseGallery(new URLSearchParams("type=png&type=webp&type=heic&minmb=5"));
    expect(st.query).toEqual({ formats: ["png", "webp"], minSize: 5 << 20 });
    expect(galleryParams(st).toString()).toBe("type=png&type=webp&minmb=5");
    expect(hasFilters({ minSize: 1 })).toBe(true);
  });
});
