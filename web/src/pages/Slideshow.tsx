import { ActionIcon, Button, Center, Loader, Stack, Text, Tooltip } from "@mantine/core";
import {
  IconMaximize,
  IconMinimize,
  IconPlayerPause,
  IconPlayerPlay,
  IconPlayerTrackNext,
  IconPlayerTrackPrev,
  IconTextCaption,
  IconX,
} from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import {
  type CSSProperties,
  type PointerEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";
import { errorMessage } from "../api/client";
import { useDeck } from "../api/decks";
import { fetchImage, useImageIds } from "../api/hooks";
import type { Image, SlideshowSettings } from "../api/types";
import { useLibrarySlideshowSettings } from "../components/slideshow/start";
import { galleryParams, parseGallery } from "../lib/query-url";
import { fadeMillis, playOrder, reshuffle, slideSource } from "../lib/slideshow";
import classes from "./Slideshow.module.css";

interface Loaded {
  id: number;
  im: Image;
  src: string;
  /** Kept so the decoded image stays in memory. */
  img: HTMLImageElement;
}

interface Slide extends Loaded {
  key: number;
  leaving: boolean;
}

interface Nav {
  /** Indexes of the slides in the order they play. */
  order: number[];
  pos: number;
  dir: 1 | -1;
  /** Tried to go past the last slide (without looping). */
  ended: boolean;
}

function Control({
  label,
  onClick,
  children,
  size = "lg",
  active,
}: {
  label: string;
  onClick: () => void;
  children: ReactNode;
  size?: string;
  active?: boolean;
}) {
  return (
    <Tooltip label={label} withinPortal>
      <ActionIcon
        variant={active ? "white" : "subtle"}
        color={active ? "dark" : "gray.0"}
        size={size}
        radius="xl"
        onClick={onClick}
        // Keep focus off buttons so Space and Enter stay slideshow keys.
        onMouseDown={(e) => e.preventDefault()}
        aria-label={label}
      >
        {children}
      </ActionIcon>
    </Tooltip>
  );
}

/** Keeps the screen on while mounted, where the browser allows it. */
function useWakeLock() {
  useEffect(() => {
    let lock: WakeLockSentinel | null = null;
    let gone = false;
    const get = () =>
      navigator.wakeLock
        ?.request("screen")
        .then((l) => {
          if (gone) l.release();
          else lock = l;
        })
        .catch(() => undefined);
    get();
    const again = () => document.visibilityState === "visible" && get();
    document.addEventListener("visibilitychange", again);
    return () => {
      gone = true;
      lock?.release().catch(() => undefined);
      document.removeEventListener("visibilitychange", again);
    };
  }, []);
}

function Player({
  ids,
  settings,
  start,
  title,
  full,
  onFull,
  onClose,
}: {
  ids: number[];
  settings: SlideshowSettings;
  start: number;
  title: string;
  full: boolean;
  onFull: () => void;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const timed = settings.advance === "timed";
  const fade = fadeMillis(settings);
  const settingsRef = useRef(settings);
  const fadeRef = useRef(fade);
  useEffect(() => {
    settingsRef.current = settings;
    fadeRef.current = fade;
  });
  useWakeLock();

  // Each slide's metadata and decoded image; the last few are kept.
  const screenPixels = Math.max(window.screen.width, window.screen.height) * (window.devicePixelRatio || 1);
  const cache = useRef(new Map<number, Promise<Loaded>>());
  const load = useCallback(
    (id: number) => {
      const c = cache.current;
      let p = c.get(id);
      if (p) {
        c.delete(id);
        c.set(id, p);
        return p;
      }
      p = fetchImage(qc, id).then(async (im) => {
        const img = new window.Image();
        img.decoding = "async";
        img.src = slideSource(im, screenPixels);
        await img.decode();
        return { id, im, src: img.src, img };
      });
      p.catch(() => c.delete(id));
      c.set(id, p);
      while (c.size > 8) c.delete(c.keys().next().value!);
      return p;
    },
    [qc, screenPixels],
  );

  const [nav, setNav] = useState<Nav>(() => ({
    order: playOrder(ids.length, start, settings.shuffle),
    pos: settings.shuffle ? 0 : start,
    dir: 1,
    ended: false,
  }));
  const step = useCallback(
    (d: 1 | -1) =>
      setNav((n) => {
        const s = settingsRef.current;
        const len = n.order.length;
        const pos = n.pos + d;
        if (pos >= len) {
          if (!s.loop) return n.ended ? n : { ...n, ended: true };
          if (s.shuffle && len > 1) return { order: reshuffle(len, n.order[n.pos]), pos: 0, dir: 1, ended: false };
          return { ...n, pos: 0, dir: 1, ended: false };
        }
        if (pos < 0) {
          if (!s.loop) return n.ended ? { ...n, ended: false } : n;
          return { ...n, pos: len - 1, dir: -1, ended: false };
        }
        return { ...n, pos, dir: d, ended: false };
      }),
    [],
  );
  const jump = useCallback((pos: number) => setNav((n) => ({ ...n, pos, dir: 1, ended: false })), []);
  // Turning shuffle on or off (the deck changed elsewhere) keeps the slide.
  const shuffled = useRef(settings.shuffle);
  useEffect(() => {
    if (shuffled.current === settings.shuffle) return;
    shuffled.current = settings.shuffle;
    setNav((n) => {
      const cur = n.order[n.pos];
      return { ...n, order: playOrder(ids.length, cur, settings.shuffle), pos: settings.shuffle ? 0 : cur };
    });
  }, [settings.shuffle, ids.length]);

  // What is on screen: the current slide, and the one fading out.
  const [slides, setSlides] = useState<Slide[]>([]);
  const [shown, setShown] = useState(0);
  const current = useRef<Slide | null>(null);
  const seq = useRef(0);
  const removal = useRef(0);
  const show = useCallback((l: Loaded) => {
    const prev = current.current;
    if (prev?.id === l.id) return;
    const next = { ...l, key: ++seq.current, leaving: false };
    current.current = next;
    setSlides(prev ? [{ ...prev, leaving: true }, next] : [next]);
    setShown(next.key);
    clearTimeout(removal.current);
    removal.current = window.setTimeout(() => setSlides((ss) => ss.filter((s) => !s.leaving)), fadeRef.current + 50);
  }, []);
  useEffect(() => () => clearTimeout(removal.current), []);

  // Show the slide at the current position once it has loaded (skipping
  // images that fail), and get the next ones ready.
  const target = ids[nav.order[nav.pos]];
  const failures = useRef(0);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    load(target)
      .then((l) => {
        if (!live) return;
        failures.current = 0;
        show(l);
        const len = nav.order.length;
        for (const d of [1, 2, -1]) {
          const i = nav.order[(nav.pos + d * nav.dir + len) % len];
          if (i !== undefined) load(ids[i]).catch(() => undefined);
        }
      })
      .catch(() => {
        if (!live) return;
        if (++failures.current >= Math.min(ids.length, 20)) setFailed(true);
        else step(nav.dir);
      });
    return () => {
      live = false;
    };
  }, [target, nav.pos, nav.order, nav.dir, ids, load, show, step]);

  // Timed advance, which pauses part-way through a slide and resumes.
  const [paused, setPaused] = useState(false);
  const remaining = useRef({ key: 0, ms: 0 });
  useEffect(() => {
    if (!timed || paused || nav.ended || !shown) return;
    if (remaining.current.key !== shown) remaining.current = { key: shown, ms: settings.interval * 1000 };
    const began = performance.now();
    const t = setTimeout(() => step(1), remaining.current.ms);
    return () => {
      clearTimeout(t);
      if (remaining.current.key === shown) remaining.current.ms -= performance.now() - began;
    };
  }, [timed, paused, nav.ended, shown, settings.interval, step]);

  // The controls show while the mouse moves, and the cursor hides.
  const [active, setActive] = useState(true);
  const idle = useRef(0);
  const hovering = useRef(false);
  const wake = useCallback(() => {
    setActive(true);
    clearTimeout(idle.current);
    const hide = () => {
      if (hovering.current) idle.current = window.setTimeout(hide, 1000);
      else setActive(false);
    };
    idle.current = window.setTimeout(hide, 2500);
  }, []);
  useEffect(() => {
    wake();
    return () => clearTimeout(idle.current);
  }, [wake]);
  const mouse = useRef({ x: -1, y: -1 });

  const [captions, setCaptions] = useState(settings.captions);
  useEffect(() => setCaptions(settings.captions), [settings.captions]);
  const togglePause = useCallback(() => {
    setPaused((p) => !p);
    wake();
  }, [wake]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      switch (e.key) {
        case "ArrowRight":
        case "ArrowDown":
        case "PageDown":
        case "Enter":
          step(1);
          break;
        case "ArrowLeft":
        case "ArrowUp":
        case "PageUp":
        case "Backspace":
          step(-1);
          break;
        case " ":
          if (settingsRef.current.advance === "timed") togglePause();
          else step(1);
          break;
        case "Home":
          jump(0);
          break;
        case "End":
          jump(ids.length - 1);
          break;
        case "f":
        case "F":
          onFull();
          break;
        case "c":
        case "C":
          setCaptions((c) => !c);
          break;
        default:
          return;
      }
      e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [step, jump, togglePause, onFull, ids.length]);

  // Click: next (or, on the left quarter, back). Swipe: either way. Tap:
  // show or hide the controls.
  const down = useRef<{ x: number; y: number; type: string } | null>(null);
  const pointerUp = (e: PointerEvent) => {
    const d = down.current;
    down.current = null;
    if (!d || (e.target as Element).closest("[data-controls]")) return;
    const dx = e.clientX - d.x;
    if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(e.clientY - d.y)) step(dx < 0 ? 1 : -1);
    else if (d.type !== "mouse") {
      if (active) setActive(false);
      else wake();
    } else if (e.button === 0) step(e.clientX < window.innerWidth / 4 ? -1 : 1);
  };

  const dpr = window.devicePixelRatio || 1;
  return (
    <div
      className={classes.root}
      style={{ background: settings.background, cursor: active ? undefined : "none", "--fade": `${fade}ms` } as CSSProperties}
      onMouseMove={(e) => {
        // (Browsers also send moves when what is under the cursor changes.)
        if (e.clientX === mouse.current.x && e.clientY === mouse.current.y) return;
        mouse.current = { x: e.clientX, y: e.clientY };
        wake();
      }}
      onPointerDown={(e) => (down.current = { x: e.clientX, y: e.clientY, type: e.pointerType })}
      onPointerUp={pointerUp}
      onContextMenu={(e) => e.preventDefault()}
    >
      {slides.map((s) => (
        <div key={s.key} className={classes.slide} data-leaving={s.leaving || undefined}>
          <img
            src={s.src}
            alt={s.im.caption || s.im.name}
            data-fit={settings.fit}
            draggable={false}
            style={settings.fit === "center" ? { width: s.im.width / dpr, height: s.im.height / dpr } : undefined}
          />
          {captions && <div className={classes.caption}>{s.im.caption || s.im.name}</div>}
        </div>
      ))}
      {failed ? (
        <Center className={classes.fill}>
          <Text c="gray.4">These images could not be loaded.</Text>
        </Center>
      ) : (
        slides.length === 0 && (
          <Center className={classes.fill}>
            <Loader color="gray" size="sm" />
          </Center>
        )
      )}
      <div
        className={classes.chrome}
        data-visible={active || undefined}
        data-controls
        onMouseEnter={() => (hovering.current = true)}
        onMouseLeave={() => (hovering.current = false)}
      >
        <div className={classes.top}>
          <div className={classes.title}>
            <Text fw={600} size="sm" truncate>
              {title}
            </Text>
            <Text size="xs" c="gray.4">
              {nav.pos + 1} / {ids.length}
              {settings.shuffle ? " · shuffled" : ""}
            </Text>
          </div>
          <div className={classes.buttons}>
            <Control label={captions ? "Hide captions (C)" : "Show captions (C)"} onClick={() => setCaptions((c) => !c)} active={captions}>
              <IconTextCaption size={20} />
            </Control>
            <Control label={full ? "Leave full screen (F)" : "Full screen (F)"} onClick={onFull}>
              {full ? <IconMinimize size={20} /> : <IconMaximize size={20} />}
            </Control>
            <Control label="Close (Esc)" onClick={onClose}>
              <IconX size={20} />
            </Control>
          </div>
        </div>
        <div className={classes.bottom}>
          <Control label="Previous (←)" onClick={() => step(-1)} size="xl">
            <IconPlayerTrackPrev size={22} />
          </Control>
          {timed && (
            <Control label={paused ? "Play (Space)" : "Pause (Space)"} onClick={togglePause} size="xl">
              {paused ? <IconPlayerPlay size={24} /> : <IconPlayerPause size={24} />}
            </Control>
          )}
          <Control label="Next (→)" onClick={() => step(1)} size="xl">
            <IconPlayerTrackNext size={22} />
          </Control>
        </div>
        {timed && shown > 0 && (
          <div className={classes.bar}>
            <div
              key={shown}
              className={classes.progress}
              style={{
                animationDuration: `${settings.interval}s`,
                animationPlayState: paused || nav.ended ? "paused" : "running",
              }}
            />
          </div>
        )}
      </div>
      {(nav.ended || (paused && !active)) && (
        <div className={classes.status}>{nav.ended ? "The end · Esc to leave" : "Paused"}</div>
      )}
    </div>
  );
}

function Message({ text, onClose }: { text: string; onClose: () => void }) {
  return (
    <div className={classes.root} style={{ background: "#000" }}>
      <Center className={classes.fill}>
        <Stack align="center">
          <Text c="gray.3">{text}</Text>
          <Button variant="white" color="dark" onClick={onClose}>
            Close
          </Button>
        </Stack>
      </Center>
    </div>
  );
}

/**
 * A full-screen slideshow of a deck (?deck=), or of the library with
 * gallery filters (or of images chosen by the page that started it).
 */
export function SlideshowPage() {
  const [sp] = useSearchParams();
  const location = useLocation();
  const navigate = useNavigate();
  const deckId = Number(sp.get("deck")) || 0;
  const start = Math.max(0, Math.floor(Number(sp.get("start")) || 0));
  const chosen = (location.state as { ids?: number[] } | null)?.ids;
  const gallery = useMemo(() => parseGallery(sp), [sp]);
  const deck = useDeck(deckId || undefined);
  const [librarySettings] = useLibrarySlideshowSettings();
  const list = useImageIds(gallery.query, gallery.sort, !deckId && !chosen);
  const live = deckId ? deck.data?.ids : (chosen ?? list.data?.ids);
  // The slides are fixed when the show starts; the settings stay live.
  const [ids, setIds] = useState<number[] | null>(null);
  useEffect(() => {
    if (live && !ids) setIds(live);
  }, [live, ids]);
  const settings = deckId ? deck.data?.settings : librarySettings;

  const closing = useRef(false);
  const close = useCallback(() => {
    if (closing.current) return;
    closing.current = true;
    if (document.fullscreenElement) document.exitFullscreen().catch(() => undefined);
    if (location.key !== "default") navigate(-1);
    else navigate(deckId ? `/decks/${deckId}` : `/?${galleryParams(gallery)}`, { replace: true });
  }, [location.key, navigate, deckId, gallery]);

  // Leaving full screen with Esc ends the show; F (or the button) only
  // switches.
  const [full, setFull] = useState(!!document.fullscreenElement);
  const switching = useRef(false);
  useEffect(() => {
    const onChange = () => {
      const now = !!document.fullscreenElement;
      setFull(now);
      if (!now && !switching.current) close();
      switching.current = false;
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && close();
    document.addEventListener("fullscreenchange", onChange);
    window.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("fullscreenchange", onChange);
      window.removeEventListener("keydown", onKey);
    };
  }, [close]);
  const toggleFull = useCallback(() => {
    if (document.fullscreenElement) {
      switching.current = true;
      document.exitFullscreen().catch(() => (switching.current = false));
    } else {
      document.documentElement.requestFullscreen({ navigationUI: "hide" }).catch(() => undefined);
    }
  }, []);

  const error = deckId ? deck.error : list.error;
  if (error) return <Message text={errorMessage(error)} onClose={close} />;
  if (!ids || !settings) {
    return (
      <div className={classes.root} style={{ background: settings?.background ?? "#000" }}>
        <Center className={classes.fill}>
          <Loader color="gray" size="sm" />
        </Center>
      </div>
    );
  }
  if (ids.length === 0) return <Message text={deckId ? "This deck has no slides yet." : "There are no images to show."} onClose={close} />;
  const title = deckId ? (deck.data?.name ?? "") : chosen ? "Selected images" : "Library";
  return (
    <Player
      ids={ids}
      settings={settings}
      start={Math.min(start, ids.length - 1)}
      title={title}
      full={full}
      onFull={toggleFull}
      onClose={close}
    />
  );
}
