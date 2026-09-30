import { Checkbox } from "@mantine/core";
import { type DragEvent, memo, type MouseEvent, useCallback, useRef, useState } from "react";
import { thumbUrl } from "../../api/client";
import { useImage } from "../../api/hooks";
import classes from "./DeckGrid.module.css";

interface Drop {
  /** The image to move in front of; 0 is the end. */
  before: number;
  /** Where the marker shows: the tile, and which side of it. */
  index: number;
  after: boolean;
}

const MIME = "application/x-photobag-slides";

/** A ghost for dragging several slides: the first, with the count. */
function dragImage(e: DragEvent, tile: HTMLElement, n: number) {
  const img = tile.querySelector("img");
  if (!img) return;
  const ghost = document.createElement("div");
  ghost.className = classes.ghost;
  ghost.appendChild(img.cloneNode());
  const badge = document.createElement("span");
  badge.textContent = String(n);
  ghost.appendChild(badge);
  document.body.appendChild(ghost);
  e.dataTransfer.setDragImage(ghost, 30, 30);
  setTimeout(() => ghost.remove());
}

/**
 * The slides of a deck in order. Drag slides (or the selection) to move
 * them; click to open, Ctrl/Shift-click to select.
 */
export function DeckGrid({
  ids,
  size,
  selected,
  onSelect,
  onOpen,
  onMove,
}: {
  ids: number[];
  size: number;
  selected: Set<number>;
  onSelect: (id: number, e: { shiftKey: boolean }) => void;
  onOpen: (index: number) => void;
  onMove: (ids: number[], before: number) => void;
}) {
  const [dragging, setDragging] = useState<number[] | null>(null);
  const [drop, setDrop] = useState<Drop | null>(null);
  // (Mirrored in refs so the tiles' handlers stay the same while dragging.)
  const dragRef = useRef<number[] | null>(null);
  const dropRef = useRef<Drop | null>(null);
  const scroller = useRef<HTMLDivElement>(null);

  const start = useCallback(
    (e: DragEvent<HTMLDivElement>, id: number) => {
      const moving = selected.has(id) ? ids.filter((x) => selected.has(x)) : [id];
      dragRef.current = moving;
      setDragging(moving);
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData(MIME, moving.join(","));
      if (moving.length > 1) dragImage(e, e.currentTarget, moving.length);
    },
    [ids, selected],
  );
  const end = useCallback(() => {
    dragRef.current = dropRef.current = null;
    setDragging(null);
    setDrop(null);
  }, []);
  const place = useCallback((d: Drop) => {
    const cur = dropRef.current;
    if (cur?.index === d.index && cur.after === d.after) return;
    dropRef.current = d;
    setDrop(d);
  }, []);
  const overTile = useCallback(
    (e: DragEvent<HTMLDivElement>, index: number) => {
      if (!dragRef.current) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      const r = e.currentTarget.getBoundingClientRect();
      const after = e.clientX > r.left + r.width / 2;
      place({ before: after ? (ids[index + 1] ?? 0) : ids[index], index, after });
    },
    [ids, place],
  );
  const finish = (e: DragEvent) => {
    const moving = dragRef.current;
    if (!moving) return;
    e.preventDefault();
    if (dropRef.current) onMove(moving, dropRef.current.before);
    end();
  };
  // Scroll while dragging near the top or bottom edge.
  const autoScroll = (e: DragEvent) => {
    const el = scroller.current;
    if (!dragRef.current || !el) return;
    const r = el.getBoundingClientRect();
    const zone = 70;
    if (e.clientY < r.top + zone) el.scrollBy(0, -Math.ceil((r.top + zone - e.clientY) / 4));
    else if (e.clientY > r.bottom - zone) el.scrollBy(0, Math.ceil((e.clientY - (r.bottom - zone)) / 4));
  };

  const moving = dragging ? new Set(dragging) : null;
  return (
    <div
      ref={scroller}
      className={classes.scroller}
      data-selecting={selected.size > 0 || undefined}
      onDragOver={(e) => {
        autoScroll(e);
        if (dragRef.current) e.preventDefault();
      }}
      onDrop={finish}
    >
      <div className={classes.grid} style={{ gridTemplateColumns: `repeat(auto-fill, minmax(${size}px, 1fr))` }}>
        {ids.map((id, i) => (
          <Slide
            key={id}
            id={id}
            index={i}
            selected={selected.has(id)}
            dragging={moving?.has(id) ?? false}
            marker={drop && drop.index === i ? (drop.after ? "after" : "before") : undefined}
            onOpen={onOpen}
            onSelect={onSelect}
            onDragStart={start}
            onDragEnd={end}
            onDragOver={overTile}
          />
        ))}
        {dragging && (
          <div
            className={classes.end}
            data-active={drop?.index === -1 || undefined}
            onDragOver={(e) => {
              e.preventDefault();
              place({ before: 0, index: -1, after: false });
            }}
          >
            Move to the end
          </div>
        )}
      </div>
    </div>
  );
}

interface SlideProps {
  id: number;
  index: number;
  selected: boolean;
  dragging: boolean;
  marker?: "before" | "after";
  onOpen: (index: number) => void;
  onSelect: (id: number, e: { shiftKey: boolean }) => void;
  onDragStart: (e: DragEvent<HTMLDivElement>, id: number) => void;
  onDragEnd: () => void;
  onDragOver: (e: DragEvent<HTMLDivElement>, index: number) => void;
}

const Slide = memo(function Slide({
  id,
  index,
  selected,
  dragging,
  marker,
  onOpen,
  onSelect,
  onDragStart,
  onDragEnd,
  onDragOver,
}: SlideProps) {
  const { data: im } = useImage(id);
  const click = (e: MouseEvent) => {
    if (e.ctrlKey || e.metaKey || e.shiftKey) {
      e.preventDefault();
      onSelect(id, e);
    } else onOpen(index);
  };
  return (
    <div
      className={classes.tile}
      draggable
      data-selected={selected || undefined}
      data-dragging={dragging || undefined}
      data-marker={marker}
      onClick={click}
      onDragStart={(e) => onDragStart(e, id)}
      onDragEnd={onDragEnd}
      onDragOver={(e) => onDragOver(e, index)}
      title={im?.name}
    >
      <div className={classes.frame}>
        <img src={thumbUrl(id)} loading="lazy" decoding="async" alt={im?.caption || im?.name || ""} draggable={false} />
      </div>
      <span className={classes.number}>{index + 1}</span>
      <Checkbox
        className={classes.check}
        checked={selected}
        size="sm"
        radius="xl"
        onClick={(e) => {
          e.stopPropagation();
          onSelect(id, e);
        }}
        onChange={() => undefined}
        aria-label="Select"
      />
    </div>
  );
});
