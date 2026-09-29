import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Group,
  Paper,
  ScrollArea,
  Slider,
  Stack,
  Switch,
  Text,
  Tooltip,
} from "@mantine/core";
import { IconAlertTriangle, IconArrowBackUp } from "@tabler/icons-react";
import { type MouseEvent, type ReactNode, useMemo, useRef } from "react";
import { generationThumb } from "../../api/generate";
import type { Generation, GenerationFailure, GenerationRun, GenerateRequest } from "../../api/types";
import { formatDate, plural } from "../../lib/format";
import { formatValue, gridLayout, inputLabel } from "../../lib/generate";
import { rangeBetween } from "../../lib/selection";
import classes from "./Results.module.css";

interface TileProps {
  g: Generation;
  size: number;
  selected: boolean;
  onOpen: (id: number) => void;
  onSelect: (id: number, e: MouseEvent | { shiftKey: boolean }) => void;
}

function Tile({ g, size, selected, onOpen, onSelect }: TileProps) {
  const h = g.width && g.height ? Math.round((size * g.height) / g.width) : size;
  const height = Math.min(Math.max(h, size * 0.5), size * 1.6);
  return (
    <div
      className={classes.tile}
      style={{ width: size, height }}
      data-selected={selected || undefined}
      data-moved={g.imageId ? true : undefined}
      onClick={(e) => (e.ctrlKey || e.metaKey || e.shiftKey ? onSelect(g.id, e) : onOpen(g.id))}
      title={g.applied.filter((a) => a.kind).map((a) => `${inputLabel(a)} = ${formatValue(a.value, 40)}`).join("\n")}
    >
      <img src={generationThumb(g)} loading="lazy" alt="" draggable={false} />
      <Checkbox
        className={classes.check}
        size="xs"
        checked={selected}
        onChange={() => undefined}
        onClick={(e) => {
          e.stopPropagation();
          onSelect(g.id, e);
        }}
        aria-label="Select"
      />
      {g.imageId ? (
        <Badge className={classes.badge} size="xs" variant="filled" color="dark">
          in library
        </Badge>
      ) : null}
    </div>
  );
}

function Empty({ size, failure }: { size: number; failure?: GenerationFailure }) {
  const box = (
    <div className={classes.empty} style={{ width: size, height: size * 0.75 }} data-failed={failure ? true : undefined}>
      {failure ? <IconAlertTriangle size={18} /> : "–"}
    </div>
  );
  return failure ? (
    <Tooltip label={failure.error} multiline w={320}>
      {box}
    </Tooltip>
  ) : (
    box
  );
}

function dimsText(run: GenerationRun): string {
  if (run.dims.length === 0) return "";
  return run.dims.map((d) => `${inputLabel(d)} (${d.values.length})`).join(" × ");
}

const statusColor: Record<string, string> = { running: "blue", done: "teal", failed: "red", cancelled: "orange" };

function RunSection({
  run,
  images,
  size,
  selected,
  onOpen,
  onSelect,
  onSelectRun,
  onUse,
}: {
  run: GenerationRun;
  images: Generation[];
  size: number;
  selected: Set<number>;
  onOpen: (id: number) => void;
  onSelect: (id: number, e: MouseEvent | { shiftKey: boolean }) => void;
  onSelectRun: (ids: number[]) => void;
  onUse: (r: GenerateRequest) => void;
}) {
  const layout = gridLayout(run.dims, run.request.count || 1);
  const byCell = useMemo(() => {
    const m = new Map<string, Generation[]>();
    for (const g of images) {
      for (const key of [`${g.combo}`, `${g.combo}/${g.repeat}`]) {
        const list = m.get(key) ?? [];
        list.push(g);
        m.set(key, list);
      }
    }
    return m;
  }, [images]);
  const failureAt = (combo: number, repeat?: number) =>
    run.failures.find((f) => f.combo === combo && (repeat === undefined || f.repeat === repeat));
  const tile = (g: Generation) => (
    <Tile key={g.id} g={g} size={size} selected={selected.has(g.id)} onOpen={onOpen} onSelect={onSelect} />
  );
  const held = images.filter((g) => !g.imageId).map((g) => g.id);
  return (
    <Paper withBorder p="sm" radius="md">
      <Stack gap="xs">
        <Group justify="space-between" wrap="nowrap" align="flex-start">
          <div style={{ minWidth: 0 }}>
            <Group gap="xs">
              <Text fw={600} size="sm">
                {run.workflowName}
              </Text>
              <Badge size="sm" variant="light" color={statusColor[run.status] ?? "gray"}>
                {run.status}
              </Badge>
              <Text size="xs" c="dimmed">
                {formatDate(run.createdAt)}
              </Text>
            </Group>
            <Text size="xs" c="dimmed">
              {plural(run.images, "image")} from {plural(run.prompts, "prompt")}
              {run.failed ? ` · ${run.failed} failed` : ""}
              {dimsText(run) ? ` · ${dimsText(run)}` : ""}
            </Text>
          </div>
          <Group gap={4} wrap="nowrap">
            {held.length > 0 && (
              <Button size="compact-xs" variant="subtle" onClick={() => onSelectRun(held)}>
                Select all
              </Button>
            )}
            <Tooltip label="Load this run's settings into the form">
              <Button size="compact-xs" variant="subtle" leftSection={<IconArrowBackUp size={14} />} onClick={() => onUse(run.request)}>
                Settings
              </Button>
            </Tooltip>
          </Group>
        </Group>
        {run.error && (
          <Alert color="red" variant="light" p="xs">
            <Text size="xs">{run.error}</Text>
          </Alert>
        )}
        {layout ? (
          <ScrollArea type="auto" offsetScrollbars>
            <table className={classes.grid}>
              <thead>
                <tr>
                  <th data-row>
                    <Text size="xs" c="dimmed">
                      {layout.rowTitle} ↓ · {layout.columnTitle} →
                    </Text>
                  </th>
                  {layout.columns.map((c, i) => (
                    <th key={i}>{c}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {layout.rows.map((row, r) => (
                  <tr key={r}>
                    <th data-row>{row.label}</th>
                    {row.cells.map((cell, c) => {
                      const list = byCell.get(cell.repeat === undefined ? `${cell.combo}` : `${cell.combo}/${cell.repeat}`) ?? [];
                      return (
                        <td key={c}>
                          {list.length ? (
                            <Group gap={4} wrap="nowrap" align="flex-start">
                              {list.map(tile)}
                            </Group>
                          ) : (
                            <Empty size={size} failure={failureAt(cell.combo, cell.repeat)} />
                          )}
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          </ScrollArea>
        ) : (
          <Group gap={6} align="flex-start">
            {images.map(tile)}
            {images.length === 0 && run.status !== "running" && (
              <Text size="xs" c="dimmed">
                {run.images ? "All of this run's images have been moved or deleted." : "No images."}
              </Text>
            )}
          </Group>
        )}
        {!layout && run.failures.length > 0 && (
          <Stack gap={2}>
            {run.failures.slice(0, 5).map((f, i) => (
              <Text key={i} size="xs" c="red" lineClamp={2}>
                Image {f.repeat + 1}: {f.error}
              </Text>
            ))}
            {run.failures.length > 5 && (
              <Text size="xs" c="red">
                … and {run.failures.length - 5} more
              </Text>
            )}
          </Stack>
        )}
      </Stack>
    </Paper>
  );
}

/** An experiment's images, run by run; sweeps are laid out as tables. */
export function Results({
  runs,
  generations,
  size,
  onSize,
  showMoved,
  onShowMoved,
  selected,
  onSelected,
  onOpen,
  onUse,
  selectionBar,
}: {
  /** Replaces the toolbar's left side while images are selected. */
  selectionBar?: ReactNode;
  runs: GenerationRun[];
  generations: Generation[];
  size: number;
  onSize: (n: number) => void;
  showMoved: boolean;
  onShowMoved: (v: boolean) => void;
  selected: Set<number>;
  onSelected: (s: Set<number>) => void;
  onOpen: (id: number) => void;
  onUse: (r: GenerateRequest) => void;
}) {
  const anchor = useRef<number | null>(null);
  const byRun = useMemo(() => {
    const m = new Map<number, Generation[]>();
    for (const g of generations) {
      const list = m.get(g.runId ?? 0) ?? [];
      list.push(g);
      m.set(g.runId ?? 0, list);
    }
    return m;
  }, [generations]);
  const order = useMemo(() => {
    const ids: number[] = [];
    for (const r of runs) for (const g of byRun.get(r.id) ?? []) ids.push(g.id);
    return ids;
  }, [runs, byRun]);
  const onSelect = (id: number, e: { shiftKey: boolean }) => {
    const next = new Set(selected);
    if (e.shiftKey && anchor.current !== null) {
      for (const x of rangeBetween(order, anchor.current, id)) next.add(x);
    } else if (next.has(id)) next.delete(id);
    else next.add(id);
    anchor.current = id;
    onSelected(next);
  };
  const orphans = byRun.get(0) ?? [];
  return (
    <Stack gap="sm" className={selected.size > 0 ? classes.selecting : undefined}>
      <Group
        justify="space-between"
        wrap="nowrap"
        mih={36}
        py={4}
        style={{ position: "sticky", top: 0, zIndex: 3, background: "var(--mantine-color-body)" }}
      >
        {selectionBar ?? (
          <Switch size="xs" label="Show images moved to the library" checked={showMoved} onChange={(e) => onShowMoved(e.currentTarget.checked)} />
        )}
        <Group gap="xs" w={200}>
          <Text size="xs" c="dimmed">
            Size
          </Text>
          <Slider size="xs" min={80} max={320} step={20} value={size} onChange={onSize} style={{ flex: 1 }} label={null} />
        </Group>
      </Group>
      {runs.length === 0 && (
        <Text size="sm" c="dimmed">
          Nothing generated yet. Choose a workflow and press Generate.
        </Text>
      )}
      {runs.map((r) => (
        <RunSection
          key={r.id}
          run={r}
          images={byRun.get(r.id) ?? []}
          size={size}
          selected={selected}
          onOpen={onOpen}
          onSelect={onSelect}
          onSelectRun={(ids) => onSelected(new Set([...selected, ...ids]))}
          onUse={onUse}
        />
      ))}
      {orphans.length > 0 && (
        <Paper withBorder p="sm" radius="md">
          <Group gap={6}>
            {orphans.map((g) => (
              <Tile key={g.id} g={g} size={size} selected={selected.has(g.id)} onOpen={onOpen} onSelect={onSelect} />
            ))}
          </Group>
        </Paper>
      )}
    </Stack>
  );
}
