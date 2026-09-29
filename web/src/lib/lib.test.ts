import { describe, expect, it } from "vitest";
import { createBatcher } from "../api/batcher";
import { formatBytes, formatTaken, percent } from "./format";
import type { AnalysisSettings, TaggerStatus } from "../api/types";
import { defaultSweep, gridLayout, isSeedName, rangeValues, requestFrom, valueCount, valueKind } from "./generate";
import { eta, formatDuration, formatMillis, notReady, usesTagger } from "./pipelines";
import { galleryParams, hasFilters, parseGallery } from "./query-url";
import { rangeBetween, toggled } from "./selection";

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
