import { ActionIcon, Badge, Button, Group, Kbd, Loader, SegmentedControl, Text, Tooltip } from "@mantine/core";
import { IconChevronLeft, IconChevronRight, IconX, IconZoomIn, IconZoomReset } from "@tabler/icons-react";
import { useEffect, useLayoutEffect, useRef, useState, type PointerEvent, type WheelEvent } from "react";
import { originalViewUrl, resultUrl } from "../../api/reencode";
import type { ReencodeItem } from "../../api/types";
import { formatBytes } from "../../lib/format";
import { formatName, psnrLabel, sizeChange } from "../../lib/reencode";
import classes from "./CompareView.module.css";

/** What is shown: the region of the picture (normalised centre) and zoom (1 = fit). */
interface View {
  scale: number;
  cx: number;
  cy: number;
}

const FIT: View = { scale: 1, cx: 0.5, cy: 0.5 };
const MAX_SCALE = 64;

function clampView(v: View): View {
  const scale = Math.min(Math.max(v.scale, 1), MAX_SCALE);
  if (scale === 1) return FIT;
  return { scale, cx: Math.min(Math.max(v.cx, 0), 1), cy: Math.min(Math.max(v.cy, 0), 1) };
}

function usePaneSize(ref: React.RefObject<HTMLDivElement | null>) {
  const [size, setSize] = useState({ w: 0, h: 0 });
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    setSize({ w: el.clientWidth, h: el.clientHeight });
    return () => ro.disconnect();
  }, [ref]);
  return size;
}

/** The picture's box in a pane: fitted, then zoomed around the view's centre. */
function layout(pane: { w: number; h: number }, aspect: number, view: View) {
  const fitW = Math.min(pane.w, pane.h * aspect);
  const fitH = fitW / aspect;
  const w = fitW * view.scale;
  const h = fitH * view.scale;
  return { w, h, left: pane.w / 2 - view.cx * w, top: pane.h / 2 - view.cy * h, fitW };
}

function Pane({
  src,
  label,
  aspect,
  pixelWidth,
  view,
  onView,
}: {
  src: string;
  label: React.ReactNode;
  aspect: number;
  /** The picture's width in pixels, to show pixels crisply when zoomed in. */
  pixelWidth: number;
  view: View;
  onView: (v: View | ((v: View) => View)) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const size = usePaneSize(ref);
  const [loaded, setLoaded] = useState<string | null>(null);
  const drag = useRef<{ x: number; y: number } | null>(null);
  const [dragging, setDragging] = useState(false);
  const box = layout(size, aspect, view);
  const pixels = (box.w * window.devicePixelRatio) / pixelWidth >= 2;

  const down = (e: PointerEvent) => {
    if (view.scale === 1) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { x: e.clientX, y: e.clientY };
    setDragging(true);
  };
  const move = (e: PointerEvent) => {
    const d = drag.current;
    if (!d) return;
    const dx = e.clientX - d.x;
    const dy = e.clientY - d.y;
    drag.current = { x: e.clientX, y: e.clientY };
    onView((v) => clampView({ ...v, cx: v.cx - dx / box.w, cy: v.cy - dy / box.h }));
  };
  const up = () => {
    drag.current = null;
    setDragging(false);
  };
  const wheel = (e: WheelEvent) => {
    const rect = ref.current!.getBoundingClientRect();
    const px = e.clientX - rect.left - size.w / 2;
    const py = e.clientY - rect.top - size.h / 2;
    onView((v) => {
      const cur = layout(size, aspect, v);
      // The picture point under the pointer stays under it.
      const ux = v.cx + px / cur.w;
      const uy = v.cy + py / cur.h;
      const scale = Math.min(Math.max(v.scale * Math.exp(-e.deltaY * 0.0015), 1), MAX_SCALE);
      const next = layout(size, aspect, { ...v, scale });
      return clampView({ scale, cx: ux - px / next.w, cy: uy - py / next.h });
    });
  };
  return (
    <div
      ref={ref}
      className={classes.pane}
      data-dragging={dragging || undefined}
      onPointerDown={down}
      onPointerMove={move}
      onPointerUp={up}
      onPointerCancel={up}
      onWheel={wheel}
    >
      <div className={classes.label}>{label}</div>
      {loaded !== src && (
        <div className={classes.loading}>
          <Loader color="gray" />
        </div>
      )}
      {size.w > 0 && (
        <img
          key={src}
          src={src}
          alt=""
          draggable={false}
          data-pixels={pixels || undefined}
          onLoad={() => setLoaded(src)}
          style={{ width: box.w, height: box.h, left: box.left, top: box.top, visibility: loaded === src ? "visible" : "hidden" }}
        />
      )}
    </div>
  );
}

function describe(format: string, size: number, w: number, h: number) {
  return (
    <>
      <b>{formatName(format)}</b> · {formatBytes(size)} · {w}×{h}
    </>
  );
}

/**
 * Compares re-encoded images with their originals, side by side (or
 * flipping between them), with zoom and pan kept in step, and takes the
 * decision for each.
 */
export function CompareView({
  batch,
  order,
  items,
  start,
  onDecide,
  deciding,
  onClose,
}: {
  batch: number;
  /** The images to step through, in order. */
  order: number[];
  /** The latest state of every item, by image id. */
  items: Map<number, ReencodeItem>;
  start: number;
  onDecide: (image: number, replace: boolean) => void;
  deciding: boolean;
  onClose: () => void;
}) {
  const [current, setCurrent] = useState(start);
  const [mode, setMode] = useState<"side" | "flip">("side");
  const [flipped, setFlipped] = useState(false);
  const [view, setView] = useState<View>(FIT);
  const it = items.get(current);
  const pos = order.indexOf(current);

  const go = (step: number) => {
    const next = order[pos + step];
    if (next !== undefined) {
      setCurrent(next);
      setView(FIT);
    }
  };
  // After a decision, move on to the next image still waiting for one.
  const decide = (replace: boolean) => {
    if (!it || it.status !== "ready" || deciding) return;
    onDecide(it.imageId, replace);
    const next = order.slice(pos + 1).find((id) => id !== it.imageId && items.get(id)?.status === "ready");
    if (next !== undefined) {
      setCurrent(next);
      setView(FIT);
    }
  };

  const ready = it?.status === "ready";
  const replaced = it?.status === "replaced";
  const aspect = it ? (it.newWidth && it.newHeight ? it.newWidth / it.newHeight : it.oldWidth / it.oldHeight) || 1 : 1;
  const panesRef = useRef<HTMLDivElement>(null);
  // Zoom to one pixel of the picture shown first (the original, while
  // there is one) per screen pixel.
  const actual = () => {
    const pane = panesRef.current?.firstElementChild as HTMLElement | null | undefined;
    if (!pane || !it) return;
    const pixels = (replaced ? it.newWidth : it.oldWidth) || 1;
    const fitW = Math.min(pane.clientWidth, pane.clientHeight * aspect);
    setView(clampView({ scale: pixels / window.devicePixelRatio / fitW, cx: 0.5, cy: 0.5 }));
  };

  const keys = useRef({ go, decide, actual, onClose, mode, view });
  keys.current = { go, decide, actual, onClose, mode, view };
  useEffect(() => {
    const down = (e: KeyboardEvent) => {
      const k = keys.current;
      if (e.target instanceof HTMLInputElement) return;
      switch (e.key) {
        case "ArrowLeft":
          k.go(-1);
          break;
        case "ArrowRight":
          k.go(1);
          break;
        case "r":
        case "R":
          k.decide(true);
          break;
        case "k":
        case "K":
          k.decide(false);
          break;
        case "f":
        case "F":
          setMode((m) => (m === "side" ? "flip" : "side"));
          break;
        case "z":
        case "Z":
          if (k.view.scale !== 1) setView(FIT);
          else k.actual();
          break;
        case " ":
          if (k.mode === "flip") setFlipped(true);
          break;
        case "Escape":
          k.onClose();
          break;
        default:
          return;
      }
      e.preventDefault();
    };
    const up = (e: KeyboardEvent) => {
      if (e.key === " ") setFlipped(false);
    };
    window.addEventListener("keydown", down);
    window.addEventListener("keyup", up);
    return () => {
      window.removeEventListener("keydown", down);
      window.removeEventListener("keyup", up);
    };
  }, []);

  if (!it) return null;
  const originalLabel = replaced ? (
    <>Now · {describe(it.newFormat!, it.newSize!, it.newWidth!, it.newHeight!)}</>
  ) : (
    <>Original · {describe(it.oldFormat, it.oldSize, it.oldWidth, it.oldHeight)}</>
  );
  const resultLabel = ready ? (
    <>
      New · {describe(it.newFormat!, it.newSize!, it.newWidth!, it.newHeight!)} · {sizeChange(it.oldSize, it.newSize!)}
    </>
  ) : null;
  const q = ready ? psnrLabel(it.psnr) : undefined;
  const panes =
    ready && mode === "side" ? (
      <div ref={panesRef} className={classes.panes}>
        <Pane src={originalViewUrl(it.imageId)} label={originalLabel} aspect={aspect} pixelWidth={it.oldWidth} view={view} onView={setView} />
        <Pane src={resultUrl(batch, it)} label={resultLabel} aspect={aspect} pixelWidth={it.newWidth!} view={view} onView={setView} />
      </div>
    ) : ready ? (
      <div ref={panesRef} className={classes.panes} data-single>
        <Pane
          src={flipped ? originalViewUrl(it.imageId) : resultUrl(batch, it)}
          label={flipped ? originalLabel : resultLabel}
          aspect={aspect}
          pixelWidth={flipped ? it.oldWidth : it.newWidth!}
          view={view}
          onView={setView}
        />
      </div>
    ) : (
      <div ref={panesRef} className={classes.panes} data-single>
        <Pane
          src={originalViewUrl(it.imageId)}
          label={originalLabel}
          aspect={aspect}
          pixelWidth={replaced ? it.newWidth! : it.oldWidth}
          view={view}
          onView={setView}
        />
      </div>
    );

  return (
    <div className={classes.root} role="dialog" aria-label={`Compare ${it.name}`}>
      <div className={classes.bar}>
        <Text fw={600} truncate style={{ minWidth: 0 }}>
          {it.name}
        </Text>
        <Text className={classes.dim} style={{ flexShrink: 0 }}>
          {pos + 1} / {order.length}
        </Text>
        {q && (
          <Tooltip label="How closely the new pixels match the original (PSNR); above about 45 dB differences are invisible">
            <Badge color={q.color} variant="light">
              {it.psnr === undefined ? q.label : `${it.psnr.toFixed(1)} dB · ${q.label}`}
            </Badge>
          </Tooltip>
        )}
        {!ready && (
          <Badge color={it.status === "replaced" ? "teal" : it.status === "failed" ? "red" : "gray"} variant="light">
            {it.status}
          </Badge>
        )}
        <Group gap="xs" ml="auto" wrap="nowrap">
          {ready && (
            <Tooltip label="F switches; in Flip, hold Space to see the original">
              <SegmentedControl
                size="xs"
                value={mode}
                onChange={(m) => setMode(m as "side" | "flip")}
                data={[
                  { value: "side", label: "Side by side" },
                  { value: "flip", label: "Flip" },
                ]}
              />
            </Tooltip>
          )}
          <Tooltip label="Fit (Z)">
            <ActionIcon variant="subtle" color="gray" onClick={() => setView(FIT)} aria-label="Fit">
              <IconZoomReset size={18} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="Actual pixels of the original (Z); scroll to zoom, drag to move">
            <ActionIcon variant="subtle" color="gray" onClick={actual} aria-label="Actual pixels">
              <IconZoomIn size={18} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="Close (Esc)">
            <ActionIcon variant="subtle" color="gray" onClick={onClose} aria-label="Close">
              <IconX size={18} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </div>
      {panes}
      <div className={`${classes.bar} ${classes.bottom}`}>
        <ActionIcon variant="subtle" color="gray" onClick={() => go(-1)} disabled={pos <= 0} aria-label="Previous">
          <IconChevronLeft size={20} />
        </ActionIcon>
        <ActionIcon variant="subtle" color="gray" onClick={() => go(1)} disabled={pos >= order.length - 1} aria-label="Next">
          <IconChevronRight size={20} />
        </ActionIcon>
        <Text className={classes.dim} truncate style={{ flex: 1, minWidth: 0 }}>
          {ready
            ? it.notes.length
              ? it.notes.join(" · ")
              : mode === "flip"
                ? "Hold Space to see the original"
                : "Scroll to zoom, drag to move; both sides follow"
            : it.status === "replaced"
              ? "The original was replaced by this file."
              : it.status === "kept"
                ? "The original was kept; the new file was dropped."
                : it.reason || "No new file was made."}
        </Text>
        {ready && (
          <Group gap="xs" wrap="nowrap">
            <Button variant="default" onClick={() => decide(false)} disabled={deciding} rightSection={<Kbd size="xs">K</Kbd>}>
              Keep original
            </Button>
            <Button color="teal" onClick={() => decide(true)} disabled={deciding} rightSection={<Kbd size="xs">R</Kbd>}>
              Replace
            </Button>
          </Group>
        )}
      </div>
    </div>
  );
}
