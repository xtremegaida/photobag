import { create } from "zustand";
import type { AnalysisSettings } from "../api/types";

interface DraftState {
  /** Settings being edited (null until loaded). Kept while browsing other pages. */
  draft: AnalysisSettings | null;
  /** Unsaved API key: undefined keeps the stored one, "" forgets it. */
  apiKey: string | undefined;
  setDraft: (d: AnalysisSettings | null) => void;
  patch: (p: Partial<AnalysisSettings>) => void;
  setApiKey: (k: string | undefined) => void;
}

/** Unsaved analysis settings, so leaving the page does not lose edits. */
export const useAnalysisDraft = create<DraftState>((set) => ({
  draft: null,
  apiKey: undefined,
  setDraft: (draft) => set({ draft }),
  patch: (p) => set((s) => (s.draft ? { draft: { ...s.draft, ...p } } : s)),
  setApiKey: (apiKey) => set({ apiKey }),
}));
