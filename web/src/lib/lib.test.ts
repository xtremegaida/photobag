import { describe, expect, it } from "vitest";
import { createBatcher } from "../api/batcher";
import { formatBytes, formatTaken, percent } from "./format";
import { eta, formatDuration } from "./pipelines";
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
