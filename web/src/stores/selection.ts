import { create } from "zustand";
import { rangeBetween, toggled } from "../lib/selection";

interface SelectionState {
  selected: Set<number>;
  anchor: number | null;
  /** Ctrl/Cmd-click or checkbox: toggle one image. */
  toggle: (id: number) => void;
  /** Shift-click: add the range from the anchor to id in the current order. */
  extend: (order: number[], id: number) => void;
  set: (ids: Iterable<number>) => void;
  remove: (ids: Iterable<number>) => void;
  clear: () => void;
}

/** Gallery selection, kept across pages so it can feed export and scoring. */
export const useSelection = create<SelectionState>((set) => ({
  selected: new Set(),
  anchor: null,
  toggle: (id) => set((s) => ({ selected: toggled(s.selected, id), anchor: id })),
  extend: (order, id) =>
    set((s) => {
      const next = new Set(s.selected);
      for (const x of rangeBetween(order, s.anchor, id)) next.add(x);
      return { selected: next, anchor: id };
    }),
  set: (ids) => set({ selected: new Set(ids), anchor: null }),
  remove: (ids) =>
    set((s) => {
      const next = new Set(s.selected);
      for (const x of ids) next.delete(x);
      return { selected: next };
    }),
  clear: () => set({ selected: new Set(), anchor: null }),
}));
