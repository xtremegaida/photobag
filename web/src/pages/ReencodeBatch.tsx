import {
  Alert,
  Anchor,
  Badge,
  Button,
  Checkbox,
  EmptyState,
  Group,
  Loader,
  Paper,
  Progress,
  SegmentedControl,
  Select,
  Stack,
  Text,
  Title,
  Tooltip,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconCheck, IconPlayerPlay, IconPlayerStop, IconTrash } from "@tabler/icons-react";
import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { errorMessage, shaThumbUrl, thumbUrl } from "../api/client";
import { useDecide, useDeleteReencode, useReencode, useResumeReencode, useStopReencode } from "../api/reencode";
import type { ReencodeItem } from "../api/types";
import { Confirm } from "../components/Confirm";
import { CompareView } from "../components/reencode/CompareView";
import { formatBytes, formatDate, plural } from "../lib/format";
import { formatName, pickItems, psnrLabel, sizeChange, reencodeTitle, sizeStory, type ItemFilter, type ItemSort } from "../lib/reencode";
import { batchSummary, stateColor, useBatchProgress } from "./Reencode";
import classes from "./ReencodeBatch.module.css";

const statusColor: Record<string, string> = { replaced: "teal", kept: "gray", skipped: "gray", failed: "red", pending: "blue" };

function Tile({
  item,
  selected,
  onToggle,
  onOpen,
}: {
  item: ReencodeItem;
  selected: boolean;
  onToggle: () => void;
  onOpen: () => void;
}) {
  const ready = item.status === "ready";
  const q = ready ? psnrLabel(item.psnr) : undefined;
  const scaled = item.newWidth && (item.newWidth !== item.oldWidth || item.newHeight !== item.oldHeight);
  return (
    <div
      className={classes.tile}
      data-selected={selected || undefined}
      data-dim={item.status === "skipped" || item.status === "failed" || item.status === "kept" || undefined}
      onClick={onOpen}
    >
      <div className={classes.thumb}>
        <img src={ready && item.newSha256 ? shaThumbUrl(item.newSha256) : thumbUrl(item.imageId)} loading="lazy" alt="" draggable={false} />
      </div>
      {ready && (
        <Checkbox
          className={classes.check}
          checked={selected}
          onClick={(e) => e.stopPropagation()}
          onChange={onToggle}
          aria-label={`Select ${item.name}`}
        />
      )}
      <div className={classes.badge}>
        {q ? (
          <Tooltip label={item.psnr === undefined ? "Decodes to exactly the same pixels" : `${item.psnr.toFixed(1)} dB PSNR`}>
            <Badge color={q.color} variant="filled" size="sm">
              {q.label}
            </Badge>
          </Tooltip>
        ) : (
          <Badge color={statusColor[item.status]} variant="filled" size="sm">
            {item.status}
          </Badge>
        )}
      </div>
      <div className={classes.body}>
        <Text size="sm" fw={500} truncate title={item.name}>
          {item.name}
        </Text>
        {item.newSize ? (
          <Text size="xs" c={item.status === "kept" ? "dimmed" : item.newSize < item.oldSize ? "teal" : "orange"}>
            {formatBytes(item.oldSize)} → {formatBytes(item.newSize)} ({sizeChange(item.oldSize, item.newSize)})
          </Text>
        ) : (
          <Text size="xs" c="dimmed">
            {formatName(item.oldFormat || "")} · {formatBytes(item.oldSize)}
          </Text>
        )}
        {scaled ? (
          <Text size="xs" c="dimmed">
            {item.oldWidth}×{item.oldHeight} → {item.newWidth}×{item.newHeight}
          </Text>
        ) : null}
        {item.reason && (
          <Text size="xs" c={item.status === "failed" ? "red" : "dimmed"} lineClamp={2} title={item.reason}>
            {item.reason}
          </Text>
        )}
      </div>
    </div>
  );
}

/** One re-encode: progress, and the results to compare and decide on. */
export function ReencodeBatchPage() {
  const id = Number(useParams().id);
  const navigate = useNavigate();
  const { data, error, isLoading } = useReencode(id);
  const running = useBatchProgress(data);
  const decide = useDecide(id);
  const resume = useResumeReencode();
  const stop = useStopReencode();
  const del = useDeleteReencode();
  const [filter, setFilter] = useState<ItemFilter | null>(null);
  const [sort, setSort] = useState<ItemSort>("order");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [comparing, setComparing] = useState<{ order: number[]; start: number } | null>(null);
  const [confirm, setConfirm] = useState<{ replace: boolean; ids?: number[] } | null>(null);
  const [deleting, setDeleting] = useState(false);

  const items = useMemo(() => data?.items ?? [], [data]);
  const byId = useMemo(() => new Map(items.map((it) => [it.imageId, it])), [items]);
  const c = data?.counts;
  // Show what needs attention: results to review, else everything.
  const shownFilter: ItemFilter = filter ?? (c?.ready ? "ready" : "all");
  const shown = useMemo(() => pickItems(items, shownFilter, sort), [items, shownFilter, sort]);
  const chosen = [...selected].filter((x) => byId.get(x)?.status === "ready");

  if (isLoading) return <Loader m="md" />;
  if (error || !data) {
    return (
      <Alert color="red" m="md">
        {error ? errorMessage(error) : "No such re-encode"}{" "}
        <Anchor component={Link} to="/reencode">
          All re-encodes
        </Anchor>
      </Alert>
    );
  }

  const p = running?.progress;
  const act = (replace: boolean, ids?: number[]) =>
    decide.mutate(
      { ids, replace },
      {
        onSuccess: (r) => {
          setSelected(new Set());
          setConfirm(null);
          const parts = [];
          if (r.replaced) parts.push(`Replaced ${plural(r.replaced, "original")}`);
          if (r.kept) parts.push(`kept ${plural(r.kept, "original")}`);
          if (r.stale) parts.push(`${r.stale} changed meanwhile and were left as they are`);
          notifications.show({ message: parts.join("; ") || "Nothing to do" });
        },
        onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
      },
    );
  const filters = [
    { value: "ready", label: `To review (${c!.ready})` },
    { value: "replaced", label: `Replaced (${c!.replaced})` },
    { value: "kept", label: `Kept (${c!.kept})` },
    { value: "other", label: `Skipped & failed (${c!.skipped + c!.failed + c!.pending})` },
    { value: "all", label: `All (${c!.total})` },
  ];
  const readyItems = items.filter((it) => it.status === "ready");
  const confirmCount = confirm ? (confirm.ids ?? readyItems.map((it) => it.imageId)).length : 0;

  return (
    <Stack gap="md" p="md" style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
      <Group justify="space-between" align="flex-start" wrap="wrap">
        <div style={{ minWidth: 0, flex: 1 }}>
          <Group gap="xs">
            <Anchor component={Link} to="/reencode" size="sm" c="dimmed">
              Re-encode
            </Anchor>
            <Text size="sm" c="dimmed">
              /
            </Text>
          </Group>
          <Group gap="sm" align="center">
            <Title order={3}>{reencodeTitle(data.settings)}</Title>
            <Badge color={stateColor[data.state]} variant="light">
              {data.state}
            </Badge>
          </Group>
          <Text size="sm" c="dimmed">
            {data.description} · {data.mode === "review" ? "compare first" : "replace straight away"} · started{" "}
            {formatDate(data.createdAt)}
            {data.settings.keepMetadata ? "" : " · metadata left out"}
            {data.settings.onlySmaller ? " · only smaller files" : ""}
          </Text>
        </div>
        <Group gap="xs">
          {(data.state === "running" || data.state === "queued") && (
            <Button variant="default" color="red" leftSection={<IconPlayerStop size={16} />} loading={stop.isPending} onClick={() => stop.mutate(id)}>
              Stop
            </Button>
          )}
          {data.state === "stopped" && c!.pending > 0 && (
            <Button leftSection={<IconPlayerPlay size={16} />} loading={resume.isPending} onClick={() => resume.mutate(id, { onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }) })}>
              Carry on ({c!.pending} left)
            </Button>
          )}
          {data.state !== "running" && data.state !== "queued" && (
            <Button variant="default" color="red" leftSection={<IconTrash size={16} />} onClick={() => setDeleting(true)}>
              Delete
            </Button>
          )}
        </Group>
      </Group>

      {data.error && (
        <Alert color="red" title="Stopped by an error">
          {data.error}
        </Alert>
      )}
      {running && (
        <Paper withBorder p="sm">
          <Stack gap={6}>
            <Progress value={p && p.total ? (p.done / p.total) * 100 : 0} animated={!p} striped={!p} />
            <Text size="sm">
              {p ? `${p.done} of ${p.total} done` : "Waiting to start"}
              {p?.replaced ? ` · ${p.replaced} replaced` : ""}
              {p?.ready ? ` · ${p.ready} to review` : ""}
              {p?.skipped ? ` · ${p.skipped} skipped` : ""}
              {p?.failed ? ` · ${p.failed} failed` : ""}
            </Text>
            {p?.current && (
              <Text size="xs" c="dimmed" truncate>
                {p.current}
              </Text>
            )}
          </Stack>
        </Paper>
      )}

      <Group gap="lg" wrap="wrap">
        <Text size="sm">{batchSummary(data)}</Text>
        {c!.replaced > 0 && (
          <Text size="sm" c="teal">
            Saved {formatBytes(data.replacedOld - data.replacedNew)}: {sizeStory(data.replacedOld, data.replacedNew)}
          </Text>
        )}
      </Group>

      {c!.ready > 0 && (
        <Paper withBorder p="sm" bg="var(--mantine-color-yellow-light)">
          <Group justify="space-between" wrap="wrap" gap="sm">
            <Text size="sm">
              <b>{plural(c!.ready, "result")}</b> waiting: {sizeStory(data.readyOld, data.readyNew)}. Click one to compare it
              with its original.
            </Text>
            <Group gap="xs">
              <Button variant="default" size="xs" onClick={() => setConfirm({ replace: false })}>
                Keep all {c!.ready} originals
              </Button>
              <Button color="teal" size="xs" leftSection={<IconCheck size={14} />} onClick={() => setConfirm({ replace: true })}>
                Replace all {c!.ready}
              </Button>
            </Group>
          </Group>
        </Paper>
      )}

      <Group gap="sm" wrap="wrap">
        <SegmentedControl size="xs" data={filters} value={shownFilter} onChange={(v) => setFilter(v as ItemFilter)} />
        <Select
          size="xs"
          w={200}
          value={sort}
          onChange={(v) => v && setSort(v as ItemSort)}
          allowDeselect={false}
          data={[
            { value: "order", label: "In order" },
            { value: "saving", label: "Biggest saving first" },
            { value: "quality", label: "Most changed first" },
            { value: "name", label: "By name" },
          ]}
        />
        {chosen.length > 0 && (
          <Group gap="xs" ml="auto">
            <Text size="sm" fw={600}>
              {chosen.length} selected
            </Text>
            <Button size="xs" variant="default" onClick={() => setConfirm({ replace: false, ids: chosen })}>
              Keep originals
            </Button>
            <Button size="xs" color="teal" onClick={() => setConfirm({ replace: true, ids: chosen })}>
              Replace
            </Button>
            <Button size="xs" variant="subtle" color="gray" onClick={() => setSelected(new Set())}>
              Clear
            </Button>
          </Group>
        )}
      </Group>

      {shown.length === 0 ? (
        <EmptyState title="Nothing here" description={running ? "Results appear as they are made." : "Choose another view above."} />
      ) : (
        <div className={classes.grid}>
          {shown.map((it) => (
            <Tile
              key={it.imageId}
              item={it}
              selected={selected.has(it.imageId)}
              onToggle={() =>
                setSelected((s) => {
                  const next = new Set(s);
                  if (next.has(it.imageId)) next.delete(it.imageId);
                  else next.add(it.imageId);
                  return next;
                })
              }
              onOpen={() => setComparing({ order: shown.map((x) => x.imageId), start: it.imageId })}
            />
          ))}
        </div>
      )}

      {comparing && (
        <CompareView
          batch={id}
          order={comparing.order}
          items={byId}
          start={comparing.start}
          deciding={decide.isPending}
          onDecide={(image, replace) => act(replace, [image])}
          onClose={() => setComparing(null)}
        />
      )}
      <Confirm
        opened={!!confirm}
        title={confirm?.replace ? `Replace ${plural(confirmCount, "original")}?` : `Keep ${plural(confirmCount, "original")}?`}
        confirm={confirm?.replace ? "Replace" : "Keep originals"}
        color={confirm?.replace ? "teal" : "gray"}
        loading={decide.isPending}
        onClose={() => setConfirm(null)}
        onConfirm={() => confirm && act(confirm.replace, confirm.ids)}
      >
        <Text size="sm">
          {confirm?.replace
            ? "The new files take the originals' places (with their tags, scores and decks); the originals are deleted and cannot be brought back."
            : "The new files are dropped; the originals stay as they are."}
        </Text>
      </Confirm>
      <Confirm
        opened={deleting}
        title="Delete this re-encode?"
        confirm="Delete"
        loading={del.isPending}
        onClose={() => setDeleting(false)}
        onConfirm={() =>
          del.mutate(id, {
            onSuccess: () => navigate("/reencode"),
            onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
          })
        }
      >
        <Text size="sm">
          {c!.ready
            ? `The ${plural(c!.ready, "result")} still waiting are dropped and their originals stay. `
            : ""}
          Replacements already made stay made.
        </Text>
      </Confirm>
    </Stack>
  );
}
