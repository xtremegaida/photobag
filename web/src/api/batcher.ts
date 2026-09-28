/**
 * Collects individual loads made in the same tick into one batched request
 * (like DataLoader), so each thumbnail tile can ask for its own metadata.
 */
export function createBatcher<K, V>(
  fetchMany: (keys: K[]) => Promise<Map<K, V>>,
  { maxBatch = 500, delayMs = 8 } = {},
) {
  let pending = new Map<K, { resolve: (v: V) => void; reject: (e: unknown) => void }[]>();
  let timer: ReturnType<typeof setTimeout> | null = null;

  async function flush() {
    timer = null;
    const batch = pending;
    pending = new Map();
    const keys = [...batch.keys()];
    for (let i = 0; i < keys.length; i += maxBatch) {
      const chunk = keys.slice(i, i + maxBatch);
      try {
        const found = await fetchMany(chunk);
        for (const k of chunk) {
          const waiters = batch.get(k) ?? [];
          const v = found.get(k);
          for (const w of waiters) {
            if (v === undefined) w.reject(new Error("not found"));
            else w.resolve(v);
          }
        }
      } catch (e) {
        for (const k of chunk) for (const w of batch.get(k) ?? []) w.reject(e);
      }
    }
  }

  return {
    load(key: K): Promise<V> {
      return new Promise<V>((resolve, reject) => {
        const list = pending.get(key);
        if (list) list.push({ resolve, reject });
        else pending.set(key, [{ resolve, reject }]);
        if (!timer) timer = setTimeout(flush, delayMs);
      });
    },
  };
}
