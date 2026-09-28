export function formatBytes(n: number): string {
  if (!Number.isFinite(n)) return "–";
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let u = 0;
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[u]}`;
}

export function formatDate(ms: number | undefined): string {
  if (!ms) return "–";
  return new Date(ms).toLocaleString();
}

/** Formats an EXIF local time ("2019-07-04T15:22:10") with optional offset. */
export function formatTaken(takenAt?: string, offset?: string): string {
  if (!takenAt) return "–";
  const [d, t] = takenAt.split("T");
  return `${d} ${t}${offset ? ` (UTC${offset})` : ""}`;
}

export function plural(n: number, word: string, pluralWord = `${word}s`): string {
  return `${n.toLocaleString()} ${n === 1 ? word : pluralWord}`;
}

export function formatScore(score: number, stderr?: number): string {
  const s = Math.round(score).toString();
  return stderr !== undefined ? `${s} ± ${Math.round(stderr)}` : s;
}

export function percent(x: number): string {
  return `${(x * 100).toFixed(x >= 0.995 && x < 1 ? 1 : 0)}%`;
}
