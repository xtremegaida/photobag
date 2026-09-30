import { useLocalStorage } from "@mantine/hooks";
import { useCallback, useMemo } from "react";
import { useNavigate } from "react-router";
import type { SlideshowSettings } from "../../api/types";
import { DEFAULT_SETTINGS, slideshowPath, withDefaults } from "../../lib/slideshow";

export interface SlideshowTarget {
  /** Play a deck… */
  deck?: number;
  /** …or the library with these gallery filters and sort… */
  params?: URLSearchParams;
  /** …or exactly these images (a selection). */
  ids?: number[];
  /** The slide to begin with. */
  start?: number;
}

/**
 * Starts a slideshow. Call it from a click: browsers only allow full
 * screen in response to one.
 */
export function useStartSlideshow() {
  const navigate = useNavigate();
  return useCallback(
    (to: SlideshowTarget) => {
      const el = document.documentElement;
      if (!document.fullscreenElement && el.requestFullscreen) {
        el.requestFullscreen({ navigationUI: "hide" }).catch(() => undefined);
      }
      navigate(slideshowPath(to), { state: to.ids ? { ids: to.ids } : undefined });
    },
    [navigate],
  );
}

/** Settings for slideshows of the library (kept in this browser). */
export function useLibrarySlideshowSettings(): [SlideshowSettings, (s: SlideshowSettings) => void] {
  const [raw, set] = useLocalStorage<Partial<SlideshowSettings>>({
    key: "pb-slideshow",
    defaultValue: DEFAULT_SETTINGS,
    getInitialValueInEffect: false,
  });
  return [useMemo(() => withDefaults(raw), [raw]), set];
}
