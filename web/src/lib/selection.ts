/** Ids between a and b (inclusive) in the given order; [b] if a is absent. */
export function rangeBetween(order: number[], a: number | null, b: number): number[] {
  const j = order.indexOf(b);
  if (j < 0) return [];
  const i = a === null ? -1 : order.indexOf(a);
  if (i < 0) return [b];
  const [lo, hi] = i < j ? [i, j] : [j, i];
  return order.slice(lo, hi + 1);
}

/** Toggles an id in a set (returning a new set). */
export function toggled(set: Set<number>, id: number): Set<number> {
  const next = new Set(set);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  return next;
}
