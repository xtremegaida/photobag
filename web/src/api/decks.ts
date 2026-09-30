// Queries and mutations for slide decks.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { moveIds } from "../lib/slideshow";
import { api } from "./client";
import { invalidateTopics } from "./hooks";
import type { Deck, DeckAdded, DeckDetail, ImageQuery, SlideshowSettings, Sort } from "./types";

export const dk = {
  decks: ["decks"] as const,
  deck: (id: number) => ["deck", id] as const,
};

/** Images to add to a deck: ids, or those matching a query in a sort order. */
export interface DeckImages {
  ids?: number[];
  query?: ImageQuery;
  sort?: Sort;
}

export function useDecks() {
  return useQuery({ queryKey: dk.decks, queryFn: () => api.get<Deck[]>("/api/decks") });
}

export function useDeck(id: number | undefined) {
  return useQuery({
    queryKey: dk.deck(id ?? 0),
    queryFn: () => api.get<DeckDetail>(`/api/decks/${id}`),
    enabled: !!id && id > 0,
  });
}

export function useCreateDeck() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { name: string; notes?: string; settings?: SlideshowSettings } & DeckImages) =>
      api.post<DeckDetail>("/api/decks", v),
    onSuccess: (d) => {
      qc.setQueryData(dk.deck(d.id), d);
      invalidateTopics(qc, ["decks"]);
    },
  });
}

export function useUpdateDeck(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { name?: string; notes?: string; settings?: SlideshowSettings }) =>
      api.patch<DeckDetail>(`/api/decks/${id}`, v),
    onSuccess: (d) => {
      qc.setQueryData(dk.deck(id), d);
      qc.invalidateQueries({ queryKey: dk.decks });
    },
  });
}

export function useDeleteDeck() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del(`/api/decks/${id}`),
    onSuccess: () => invalidateTopics(qc, ["decks"]),
  });
}

export function useAddToDeck() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...v }: { id: number } & DeckImages) => api.post<DeckAdded>(`/api/decks/${id}/add`, v),
    onSuccess: () => invalidateTopics(qc, ["decks"]),
  });
}

/** Changes the cached deck at once, then asks the server. */
function useDeckEdit<V>(id: number, apply: (d: DeckDetail, v: V) => DeckDetail, send: (v: V) => Promise<unknown>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: send,
    onMutate: async (v: V) => {
      await qc.cancelQueries({ queryKey: dk.deck(id) });
      const before = qc.getQueryData<DeckDetail>(dk.deck(id));
      if (before) qc.setQueryData(dk.deck(id), apply(before, v));
      return { before };
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.before) qc.setQueryData(dk.deck(id), ctx.before);
    },
    onSettled: () => invalidateTopics(qc, ["decks"]),
  });
}

const withIds = (d: DeckDetail, ids: number[]): DeckDetail => ({ ...d, ids, count: ids.length, covers: ids.slice(0, 4) });

export function useRemoveFromDeck(id: number) {
  return useDeckEdit(
    id,
    (d, ids: number[]) => {
      const gone = new Set(ids);
      return withIds(
        d,
        d.ids.filter((x) => !gone.has(x)),
      );
    },
    (ids) => api.post<{ removed: number }>(`/api/decks/${id}/remove`, { ids }),
  );
}

export function useMoveInDeck(id: number) {
  return useDeckEdit(
    id,
    (d, v: { ids: number[]; before: number }) => withIds(d, moveIds(d.ids, v.ids, v.before)),
    (v) => api.post<DeckDetail>(`/api/decks/${id}/move`, v),
  );
}

/** Sorts a deck like the gallery, or puts it in the order of ids. */
export function useSortDeck(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { sort: Sort } | { ids: number[] }) => api.post<DeckDetail>(`/api/decks/${id}/sort`, v),
    onSuccess: (d) => {
      qc.setQueryData(dk.deck(id), d);
      qc.invalidateQueries({ queryKey: dk.decks });
    },
  });
}
