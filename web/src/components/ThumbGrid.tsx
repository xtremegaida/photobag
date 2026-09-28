import { Checkbox } from "@mantine/core";
import { useElementSize, useMergedRef } from "@mantine/hooks";
import { useVirtualizer } from "@tanstack/react-virtual";
import { memo, useEffect, useRef, type MouseEvent, type ReactNode } from "react";
import { thumbUrl } from "../api/client";
import { useImage } from "../api/hooks";
import classes from "./ThumbGrid.module.css";

export interface ThumbGridProps {
  ids: number[];
  /** Target tile edge in px (the grid fits as many columns as possible). */
  size: number;
  selected?: Set<number>;
  focused?: number;
  /** Plain click. */
  onOpen: (index: number) => void;
  /** Ctrl/Cmd-click or checkbox (range=true for shift-click). */
  onSelect?: (id: number, range: boolean) => void;
  fit?: boolean;
  captions?: boolean;
  badge?: (id: number) => ReactNode;
  scrollToIndex?: number;
}

const GAP = 6;

/** A virtualised grid of square thumbnails. */
export function ThumbGrid({
  ids,
  size,
  selected,
  focused,
  onOpen,
  onSelect,
  fit,
  captions,
  badge,
  scrollToIndex,
}: ThumbGridProps) {
  const { ref: sizeRef, width } = useElementSize();
  const scrollRef = useRef<HTMLDivElement>(null);
  const ref = useMergedRef(sizeRef, scrollRef);
  const inner = Math.max(0, width);
  const cols = Math.max(1, Math.floor((inner + GAP) / (size + GAP)));
  const tile = Math.max(40, Math.floor((inner - GAP * (cols - 1)) / cols));
  const rows = Math.ceil(ids.length / cols);

  const virtualizer = useVirtualizer({
    count: rows,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => tile + GAP,
    overscan: 3,
  });

  useEffect(() => {
    virtualizer.measure();
  }, [tile, cols, virtualizer]);

  useEffect(() => {
    if (scrollToIndex !== undefined && scrollToIndex >= 0) {
      virtualizer.scrollToIndex(Math.floor(scrollToIndex / cols), { align: "auto" });
    }
  }, [scrollToIndex, cols, virtualizer]);

  const selecting = !!selected && selected.size > 0;
  return (
    <div
      ref={ref}
      className={classes.scroller}
      data-selecting={selecting || undefined}
      data-captions={captions || undefined}
    >
      {width > 0 && (
        <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {virtualizer.getVirtualItems().map((row) => {
            const start = row.index * cols;
            return (
              <div key={row.key} className={classes.row} style={{ top: row.start, height: tile, gap: GAP }}>
                {ids.slice(start, start + cols).map((id, k) => (
                  <Thumb
                    key={id}
                    id={id}
                    index={start + k}
                    size={tile}
                    selected={selected?.has(id) ?? false}
                    focused={focused === start + k}
                    fit={fit}
                    onOpen={onOpen}
                    onSelect={onSelect}
                    badge={badge?.(id)}
                  />
                ))}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

interface ThumbProps {
  id: number;
  index: number;
  size: number;
  selected: boolean;
  focused: boolean;
  fit?: boolean;
  onOpen: (index: number) => void;
  onSelect?: (id: number, range: boolean) => void;
  badge?: ReactNode;
}

const Thumb = memo(function Thumb({ id, index, size, selected, focused, fit, onOpen, onSelect, badge }: ThumbProps) {
  const { data: im } = useImage(id);
  const click = (e: MouseEvent) => {
    if (onSelect && (e.ctrlKey || e.metaKey || e.shiftKey)) {
      e.preventDefault();
      onSelect(id, e.shiftKey);
    } else {
      onOpen(index);
    }
  };
  return (
    <div
      className={classes.tile}
      style={{ width: size, height: size }}
      data-selected={selected || undefined}
      data-focused={focused || undefined}
      data-fit={fit || undefined}
      onClick={click}
      title={im ? `${im.name}${im.tags.length ? ` — ${im.tags.join(", ")}` : ""}` : undefined}
    >
      <img src={thumbUrl(id)} loading="lazy" decoding="async" draggable={false} alt={im?.name ?? ""} />
      {onSelect && (
        <Checkbox
          className={classes.check}
          checked={selected}
          size="sm"
          radius="xl"
          onClick={(e) => {
            e.stopPropagation();
            onSelect(id, e.shiftKey);
          }}
          onChange={() => undefined}
          aria-label="Select"
        />
      )}
      {badge && <div className={classes.badge}>{badge}</div>}
      <div className={classes.caption}>{im?.name}</div>
    </div>
  );
});
