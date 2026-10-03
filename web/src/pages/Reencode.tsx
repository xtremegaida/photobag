import { Alert, Badge, Button, Card, EmptyState, Group, Loader, Progress, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { IconPlus, IconTransform } from "@tabler/icons-react";
import { useState } from "react";
import { Link } from "react-router";
import { errorMessage } from "../api/client";
import { useReencodes } from "../api/reencode";
import type { ReencodeBatch, ReencodeProgress } from "../api/types";
import { ReencodeDialog } from "../components/reencode/ReencodeDialog";
import { formatBytes, formatDate, plural } from "../lib/format";
import { reencodeTitle, sizeChange } from "../lib/reencode";
import { useJobStore } from "../stores/jobs";

export const stateColor: Record<string, string> = { queued: "gray", running: "blue", stopped: "orange", finished: "teal" };

/** One-line tallies of a batch ("3 to review · 12 replaced · 2 skipped"). */
export function batchSummary(b: ReencodeBatch): string {
  const c = b.counts;
  const parts = [];
  if (c.ready) parts.push(`${c.ready} to review`);
  if (c.replaced) parts.push(`${c.replaced} replaced`);
  if (c.kept) parts.push(`${c.kept} kept`);
  if (c.skipped) parts.push(`${c.skipped} skipped`);
  if (c.failed) parts.push(`${c.failed} failed`);
  if (c.pending) parts.push(`${c.pending} not done`);
  return parts.join(" · ") || "nothing yet";
}

/** The progress of a batch's running job, if any. */
export function useBatchProgress(b: ReencodeBatch | undefined) {
  const job = useJobStore((s) => (b?.jobId ? s.jobs[b.jobId] : undefined));
  if (!job || (job.status !== "running" && job.status !== "queued")) return undefined;
  return { job, progress: job.progress as ReencodeProgress | undefined };
}

function BatchCard({ b }: { b: ReencodeBatch }) {
  const running = useBatchProgress(b);
  const p = running?.progress;
  return (
    <Card withBorder component={Link} to={`/reencode/${b.id}`} padding="md">
      <Stack gap={6}>
        <Group justify="space-between" wrap="nowrap" gap="xs">
          <Text fw={600} truncate>
            {reencodeTitle(b.settings)}
          </Text>
          <Badge color={b.counts.ready && b.state === "finished" ? "yellow" : stateColor[b.state]} variant="light" style={{ flexShrink: 0 }}>
            {b.counts.ready && b.state === "finished" ? "to review" : b.state}
          </Badge>
        </Group>
        <Text size="sm" c="dimmed" lineClamp={2}>
          {b.description}
        </Text>
        {running && (
          <Progress value={p && p.total ? (p.done / p.total) * 100 : 0} animated={!p} striped={!p} size="sm" />
        )}
        <Text size="sm">{batchSummary(b)}</Text>
        {b.counts.replaced > 0 && (
          <Text size="xs" c="dimmed">
            Saved {formatBytes(b.replacedOld - b.replacedNew)} ({sizeChange(b.replacedOld, b.replacedNew)} on the replaced files)
          </Text>
        )}
        <Text size="xs" c="dimmed">
          {b.mode === "review" ? "Compare first" : "Replace straight away"} · {formatDate(b.createdAt)}
        </Text>
      </Stack>
    </Card>
  );
}

/** Re-encodes: past and running batches, and starting new ones. */
export function ReencodePage() {
  const { data, error, isLoading } = useReencodes();
  const [creating, setCreating] = useState(false);
  return (
    <Stack gap="md" p="md" style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
      <Group justify="space-between" align="flex-start">
        <div>
          <Title order={3}>Re-encode</Title>
          <Text size="sm" c="dimmed" maw={720}>
            Store images in another format, quality or size: losslessly (PNG or WebP) or lossy (JPEG or WebP), optionally
            scaled down. Results replace the originals straight away, or wait so you can compare them side by side and
            choose. Select images in the library and use “Re-encode”, or choose them here by type, size, tags or name.
          </Text>
        </div>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
          New re-encode
        </Button>
      </Group>
      {error && <Alert color="red">{errorMessage(error)}</Alert>}
      {isLoading && <Loader />}
      {data && data.length === 0 && (
        <EmptyState
          icon={<IconTransform size={40} />}
          title="Nothing re-encoded yet"
          description="For instance: every PNG over 5 MB as lossless WebP, or photos scaled down to 2560 pixels as WebP."
        />
      )}
      <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }}>
        {data?.map((b) => (
          <BatchCard key={b.id} b={b} />
        ))}
      </SimpleGrid>
      {data && data.length > 0 && (
        <Text size="xs" c="dimmed">
          {plural(data.reduce((n, b) => n + b.counts.replaced, 0), "image")} re-encoded in all, saving{" "}
          {formatBytes(data.reduce((n, b) => n + b.replacedOld - b.replacedNew, 0))}.
        </Text>
      )}
      <ReencodeDialog opened={creating} onClose={() => setCreating(false)} />
    </Stack>
  );
}
