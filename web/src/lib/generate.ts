// Helpers for generation experiments: override values, sweeps and the
// layout of sweep results.
import type { Applied, Dim, GenerateRequest, Generation, InputSpec, Override, SweepRange, WorkflowNode } from "../api/types";

export type ValueKind = "combo" | "int" | "float" | "boolean" | "text" | "multiline";

/** How to edit an input: from ComfyUI's definition, else from its current value. */
export function valueKind(spec: InputSpec | undefined, current: unknown): ValueKind {
  switch (spec?.type) {
    case "COMBO":
      return "combo";
    case "INT":
      return "int";
    case "FLOAT":
      return "float";
    case "BOOLEAN":
      return "boolean";
    case "STRING":
      return spec.multiline ? "multiline" : "text";
  }
  if (Array.isArray(spec?.options) && spec.options.length) return "combo";
  if (typeof current === "boolean") return "boolean";
  if (typeof current === "number") return Number.isInteger(current) ? "int" : "float";
  if (typeof current === "string" && (current.includes("\n") || current.length > 60)) return "multiline";
  return "text";
}

/** Rounds like the server does, so the counts shown match. */
export function rangeValues(r: SweepRange, max = 500): number[] {
  if (!(r.step > 0) || !Number.isFinite(r.from) || !Number.isFinite(r.to)) return [];
  const n = Math.floor(Math.abs(r.to - r.from) / r.step + 1e-9) + 1;
  if (n > max) return [];
  const dec = Math.min(6, Math.max(decimals(r.from), decimals(r.step)));
  const scale = 10 ** dec;
  const dir = r.to < r.from ? -1 : 1;
  return Array.from({ length: n }, (_, i) => Math.round((r.from + dir * i * r.step) * scale) / scale);
}

function decimals(x: number): number {
  const s = String(x);
  const i = s.indexOf(".");
  return i < 0 ? 0 : s.length - i - 1;
}

/** How many values an override takes (1 unless it sweeps). */
export function valueCount(o: Override): number {
  if (!o.sweep) return 1;
  if (o.sweep.range) return rangeValues(o.sweep.range).length;
  return o.sweep.values?.length ?? 0;
}

/** Renders a value briefly. */
export function formatValue(v: unknown, max = 60): string {
  if (typeof v === "string") return v.length > max ? v.slice(0, max - 1) + "…" : v;
  if (v === undefined) return "–";
  return JSON.stringify(v);
}

export function inputLabel(a: { node: string; input: string }): string {
  return `${a.node} › ${a.input}`;
}

export const sameInput = (a: { node: string; input: string }, b: { node: string; input: string }) =>
  a.node === b.node && a.input === b.input;

/** The workflow node an override names ("Title" or "#id"). */
export function findNode(nodes: WorkflowNode[], ref: string): WorkflowNode | undefined {
  if (ref.startsWith("#")) return nodes.find((n) => n.id === ref.slice(1));
  return nodes.find((n) => n.ref === ref) ?? nodes.find((n) => n.title === ref);
}

/** Whether an input looks like a sampler seed (as the server decides). */
export function isSeedName(input: string): boolean {
  const n = input.toLowerCase();
  return n === "seed" || n === "noise_seed" || n.endsWith("_seed");
}

/**
 * A request that makes more images like g: its workflow version, its
 * overrides and swept values (as fixed values), and optionally its seed
 * (without it, seeds are left to the seed policy, even where an override
 * had set them).
 */
export function requestFrom(
  g: Generation & { current?: boolean; templateExists?: boolean },
  withSeed: boolean,
  count: number,
): GenerateRequest {
  const overrides: Override[] = g.applied
    .filter((a) => withSeed || (a.kind !== "seed" && !isSeedName(a.input)))
    .map((a) => ({ node: a.node, input: a.input, value: a.value }));
  return {
    workflowId: g.templateExists === false ? 0 : (g.workflowId ?? 0),
    versionId: g.current ? undefined : g.versionId,
    overrides,
    count: withSeed ? 1 : Math.max(1, count),
    seed: "random",
  };
}

/** The applied values that differ between images of a run. */
export function sweptValues(applied: Applied[]): Applied[] {
  return applied.filter((a) => a.kind === "sweep");
}

export interface GridCell {
  combo: number;
  /** Set when each cell holds one repeat (a single swept input). */
  repeat?: number;
}

export interface GridLayout {
  /** Values of the swept input across the top. */
  columns: string[];
  columnTitle: string;
  rows: { label: string; cells: GridCell[] }[];
  rowTitle: string;
}

/**
 * Lays a sweep out as a table: the last swept input across the top; the
 * other swept inputs (or, with only one, the repeats) down the side. Combo
 * indexes run with the first dimension slowest, as the server makes them.
 */
export function gridLayout(dims: Dim[], count: number): GridLayout | null {
  if (dims.length === 0) return null;
  const last = dims[dims.length - 1];
  const columns = last.values.map((v) => formatValue(v, 28));
  const columnTitle = inputLabel(last);
  if (dims.length === 1) {
    return {
      columns,
      columnTitle,
      rowTitle: "Seed",
      rows: Array.from({ length: Math.max(1, count) }, (_, r) => ({
        label: `#${r + 1}`,
        cells: last.values.map((_, c) => ({ combo: c, repeat: r })),
      })),
    };
  }
  const outer = dims.slice(0, -1);
  const nRows = outer.reduce((n, d) => n * d.values.length, 1);
  const rows = Array.from({ length: nRows }, (_, row) => {
    const parts: string[] = [];
    let rest = row;
    for (let d = outer.length - 1; d >= 0; d--) {
      const n = outer[d].values.length;
      parts.unshift(formatValue(outer[d].values[rest % n], 28));
      rest = Math.floor(rest / n);
    }
    return { label: parts.join(" · "), cells: last.values.map((_, col) => ({ combo: row * last.values.length + col })) };
  });
  return { columns, columnTitle, rows, rowTitle: outer.map(inputLabel).join(" · ") };
}

/** Default value for a new sweep of an input. */
export function defaultSweep(kind: ValueKind, current: unknown, spec?: InputSpec): Override["sweep"] {
  if (kind === "int" || kind === "float") {
    const v = typeof current === "number" ? current : 0;
    // Floats step 10× their finest step (cfg: 1, denoise: 0.1).
    const step = kind === "float" ? Math.round((spec?.step ? spec.step * 10 : 1) * 1e6) / 1e6 : 1;
    const up = v + 2 * step;
    if (spec?.max !== undefined && up > spec.max) {
      const from = Math.max(spec.min ?? -Infinity, Math.round((v - 2 * step) * 1e6) / 1e6);
      return { range: { from, to: v, step } };
    }
    return { range: { from: v, to: Math.round(up * 1e6) / 1e6, step } };
  }
  if (kind === "boolean") return { values: [true, false] };
  return { values: current === undefined ? [] : [current] };
}
