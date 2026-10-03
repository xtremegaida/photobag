import { Alert, Badge, Button, Card, Group, Progress, Stack, Table, Text } from "@mantine/core";
import type { ReactNode } from "react";
import { api } from "../api/client";
import { Link } from "react-router";
import type {
  AnalysisProgress,
  AnalysisReport,
  ExportReport,
  FileTransferReport,
  GenerateProgress,
  GenerateResult,
  ImportReport,
  Job,
  ReencodeProgress,
} from "../api/types";
import { formatBytes, formatDate } from "../lib/format";
import { eta, formatDuration, pipelineInfo } from "../lib/pipelines";
import { isActive } from "../stores/jobs";
import { jobFraction } from "./JobIndicator";

const statusColor: Record<string, string> = {
  queued: "gray",
  running: "blue",
  done: "teal",
  failed: "red",
  cancelled: "orange",
};

function ProgressLine({ job }: { job: Job }) {
  const p = (job.progress ?? {}) as Record<string, number | string>;
  const frac = jobFraction(job);
  let detail: ReactNode = job.message;
  if (job.kind === "import" && p.phase) {
    detail =
      p.phase === "scanning"
        ? `Scanning… ${p.found} files found`
        : `${p.done}/${p.found} files · ${p.added} added · ${p.skipped} skipped · ${p.failed} failed`;
  } else if (job.kind === "export" && p.total !== undefined) {
    detail = `${p.done}/${p.total} images · ${p.written} written · ${p.skipped} skipped · ${p.failed} failed`;
  } else if ((job.kind === "dedup-scan" || job.kind === "refresh") && p.total !== undefined) {
    detail = `Comparing thumbprints ${p.done}/${p.total}`;
  } else if ((job.kind === "files-import" || job.kind === "files-export") && p.phase) {
    detail =
      p.phase === "scanning"
        ? `Scanning… ${p.found} files found`
        : `${p.done}/${p.found} files · ${formatBytes(Number(p.bytes))}`;
  } else if (job.kind === "reencode" && p.total !== undefined) {
    const r = job.progress as ReencodeProgress;
    const left = eta(job.startedAt, r.done, r.total);
    detail = `${r.done}/${r.total} images · ${r.replaced} replaced · ${r.ready} to review${r.skipped ? ` · ${r.skipped} skipped` : ""}${r.failed ? ` · ${r.failed} failed` : ""}${left ? ` · about ${left} left` : ""}`;
  } else if (job.kind === "retag" && p.total !== undefined) {
    detail = `${p.done}/${p.total} images`;
  } else if (job.kind === "generate" && p.prompts !== undefined) {
    const g = job.progress as GenerateProgress;
    const left = eta(job.startedAt, g.done, g.prompts);
    detail = `${g.done}/${g.prompts} prompts · ${g.images} image${g.images === 1 ? "" : "s"}${g.failed ? ` · ${g.failed} failed` : ""}${left ? ` · about ${left} left` : ""}`;
  } else if (job.kind === "analyze" && p.total !== undefined) {
    const a = job.progress as AnalysisProgress;
    const left = eta(job.startedAt, a.done, a.total);
    detail = `${a.done}/${a.total} requests · ${a.stored} stored · ${a.failed} failed${left ? ` · about ${left} left` : ""}`;
  }
  if (!isActive(job) && job.kind !== "import" && job.kind !== "export") detail = null;
  const lastError =
    (job.kind === "analyze" || job.kind === "generate") && isActive(job)
      ? (job.progress as AnalysisProgress | GenerateProgress | undefined)?.lastError
      : undefined;
  return (
    <Stack gap={4}>
      {isActive(job) && (
        <Progress value={frac !== undefined ? frac * 100 : 100} animated={frac === undefined} striped={frac === undefined} />
      )}
      {detail && (
        <Text size="sm" c="dimmed" truncate>
          {detail}
        </Text>
      )}
      {isActive(job) && p.current && (
        <Text size="xs" c="dimmed" truncate>
          {String(p.current)}
        </Text>
      )}
      {lastError && (
        <Text size="xs" c="red" lineClamp={2}>
          Last error: {lastError}
        </Text>
      )}
    </Stack>
  );
}

function AnalyzeResult({ r }: { r: AnalysisReport }) {
  const by = Object.entries(r.byPipeline ?? {}).filter(([, n]) => n > 0);
  return (
    <Stack gap="xs">
      <Text size="sm">
        <b>{r.stored}</b> of {r.requests} result(s) stored for {r.images} image(s)
        {r.failed ? (
          <>
            , <b>{r.failed}</b> failed
          </>
        ) : null}
        {r.skipped ? `, ${r.skipped} skipped` : ""} in {formatDuration(r.millis)}
        {r.cancelled ? " (cancelled)" : ""}.
      </Text>
      {by.length > 0 && (
        <Text size="xs" c="dimmed">
          {by.map(([p, n]) => `${pipelineInfo(p).heading}: ${n}`).join(" · ")} · {r.model || "default model"}
          {r.promptTokens + r.completionTokens > 0 && ` · ${(r.promptTokens + r.completionTokens).toLocaleString()} tokens`}
        </Text>
      )}
      {r.failures?.length > 0 && (
        <Alert color="red" variant="light" title={`${r.failed} request(s) failed`}>
          <Stack gap={2} mah={200} style={{ overflow: "auto" }}>
            {r.failures.map((f, i) => (
              <Text key={i} size="xs" style={{ wordBreak: "break-word" }}>
                <b>{f.name || `#${f.imageId}`}</b>{" "}
                ({pipelineInfo(f.pipeline).heading}): {f.error}
              </Text>
            ))}
          </Stack>
        </Alert>
      )}
    </Stack>
  );
}

function ImportResult({ r }: { r: ImportReport }) {
  const reasons = new Map<string, number>();
  const failures = r.files.filter((f) => f.status === "failed");
  for (const f of r.files) if (f.status === "skipped") reasons.set(f.reason, (reasons.get(f.reason) ?? 0) + 1);
  return (
    <Stack gap="xs">
      <Text size="sm">
        <b>{r.added}</b> added, <b>{r.skipped}</b> skipped, <b>{r.failed}</b> failed of {r.found} files in{" "}
        {(r.millis / 1000).toFixed(1)}s{r.cancelled ? " (cancelled)" : ""}.
      </Text>
      {reasons.size > 0 && (
        <Table withRowBorders={false} verticalSpacing={2} fz="sm">
          <Table.Tbody>
            {[...reasons].map(([reason, n]) => (
              <Table.Tr key={reason}>
                <Table.Td w={60} ta="right">
                  {n}
                </Table.Td>
                <Table.Td c="dimmed">skipped: {reason}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      {failures.length > 0 && (
        <Alert color="red" variant="light" title={`${failures.length} file(s) failed`}>
          <Stack gap={2} mah={200} style={{ overflow: "auto" }}>
            {failures.slice(0, 200).map((f) => (
              <Text key={f.path} size="xs" style={{ wordBreak: "break-all" }}>
                {f.path}: {f.reason}
              </Text>
            ))}
          </Stack>
        </Alert>
      )}
    </Stack>
  );
}

function ExportResult({ r }: { r: ExportReport }) {
  return (
    <Stack gap="xs">
      <Text size="sm">
        <b>{r.written}</b> of {r.total} images written ({formatBytes(r.bytes)}) to <code>{r.dir}</code>
        {r.skipped ? `, ${r.skipped} skipped` : ""}
        {r.failed ? `, ${r.failed} failed` : ""}.
      </Text>
      {r.manifest && (
        <Text size="xs" c="dimmed">
          Manifest: {r.manifest}
        </Text>
      )}
      {r.files.length > 0 && (
        <Stack gap={2} mah={160} style={{ overflow: "auto" }}>
          {r.files.map((f) => (
            <Text key={f.imageId} size="xs" c={f.status === "failed" ? "red" : "dimmed"}>
              {f.status}: {f.name}: {f.reason}
            </Text>
          ))}
        </Stack>
      )}
    </Stack>
  );
}

function GenericResult({ job }: { job: Job }) {
  const r = job.result as Record<string, unknown> | undefined;
  if (!r) return null;
  if (job.kind === "backup" && typeof r.path === "string") {
    return (
      <Text size="sm">
        Backup written to <code>{r.path}</code> ({formatBytes(Number(r.bytes))}).
      </Text>
    );
  }
  if (job.kind === "backup" && typeof r.filename === "string") {
    return (
      <Text size="sm">
        Backup ready ({formatBytes(Number(r.bytes))}).{" "}
        <a href={String(r.url)} download={String(r.filename)}>
          Download {String(r.filename)}
        </a>{" "}
        (available once, for an hour)
      </Text>
    );
  }
  if (job.kind === "compact") {
    return (
      <Text size="sm">
        Bag compacted: {formatBytes(Number(r.before))} → {formatBytes(Number(r.after))}.
      </Text>
    );
  }
  if (job.kind === "empty-trash") {
    return (
      <Text size="sm">
        Purged {String(r.purged)} image(s), freeing {formatBytes(Number(r.bytesFreed))} of originals. Run Compact
        (Backup page) to shrink the file.
      </Text>
    );
  }
  if (job.kind === "refresh") {
    return (
      <Text size="sm">
        Updated {String(r.updated)} of {String(r.checked)} image file(s){Number(r.failed) ? `, ${String(r.failed)} failed` : ""}.
      </Text>
    );
  }
  if (job.kind === "retag") {
    return (
      <Text size="sm">
        {r.removed ? "Removed the Danbooru tags added by analysis." : `Updated the tags of ${String(r.updated)} analysed image(s).`}
      </Text>
    );
  }
  if (job.kind === "generate") {
    const g = r as unknown as GenerateResult;
    return (
      <Text size="sm">
        Made {g.images} image{g.images === 1 ? "" : "s"} from {g.prompts} prompt{g.prompts === 1 ? "" : "s"}
        {g.failed ? `, ${g.failed} failed` : ""}. <Link to={`/generate/${g.experimentId}`}>Open the experiment</Link>
      </Text>
    );
  }
  if (job.kind === "files-import" || job.kind === "files-export") {
    const t = r as unknown as FileTransferReport;
    const copied = t.added + t.replaced + t.renamed;
    return (
      <Stack gap={4}>
        <Text size="sm">
          {job.kind === "files-import" ? "Imported" : "Exported"} {copied} file{copied === 1 ? "" : "s"} ({formatBytes(t.bytes)})
          {t.replaced ? `, ${t.replaced} replacing others` : ""}
          {t.renamed ? `, ${t.renamed} kept beside others with a number` : ""}
          {t.skipped ? `, ${t.skipped} skipped as already there` : ""}
          {t.failed ? `, ${t.failed} failed` : ""}.{" "}
          {job.kind === "files-import" && <Link to="/files">Open Files</Link>}
        </Text>
        {t.problems?.length > 0 && (
          <Stack gap={2} mah={160} style={{ overflow: "auto" }}>
            {t.problems.map((f) => (
              <Text key={f.path} size="xs" c="red">
                {f.path}: {f.reason}
              </Text>
            ))}
          </Stack>
        )}
      </Stack>
    );
  }
  if (job.kind === "reencode") {
    const pr = (r as { progress?: ReencodeProgress }).progress;
    return (
      <Text size="sm">
        {pr
          ? `${pr.replaced} replaced${pr.newBytes ? ` (${formatBytes(pr.oldBytes)} → ${formatBytes(pr.newBytes)})` : ""}, ${pr.ready} to review, ${pr.skipped} skipped, ${pr.failed} failed. `
          : ""}
        <Link to={`/reencode/${String(r.batchId)}`}>Open the re-encode</Link>
      </Text>
    );
  }
  if (job.kind === "dedup-scan") {
    return (
      <Text size="sm">
        Scanned {String(r.scanned)} images, found {String(r.clusters)} duplicate group(s).
      </Text>
    );
  }
  return null;
}

/** Status, live progress and result of a background job. */
export function JobCard({ job, compact = false }: { job: Job; compact?: boolean }) {
  return (
    <Card withBorder padding={compact ? "sm" : "md"}>
      <Stack gap="xs">
        <Group justify="space-between" wrap="nowrap">
          <Text fw={600} truncate>
            {job.title}
          </Text>
          <Group gap="xs" wrap="nowrap">
            <Badge color={statusColor[job.status] ?? "gray"} variant="light">
              {job.status}
            </Badge>
            {isActive(job) && (
              <Button size="compact-xs" variant="subtle" color="red" onClick={() => api.post(`/api/jobs/${job.id}/cancel`)}>
                Cancel
              </Button>
            )}
          </Group>
        </Group>
        <ProgressLine job={job} />
        {job.error && (
          <Alert color="red" variant="light">
            {job.error}
          </Alert>
        )}
        {!isActive(job) && job.kind === "import" && job.result != null && <ImportResult r={job.result as ImportReport} />}
        {!isActive(job) && job.kind === "export" && job.result != null && <ExportResult r={job.result as ExportReport} />}
        {!isActive(job) && job.kind === "analyze" && job.result != null && <AnalyzeResult r={job.result as AnalysisReport} />}
        {!isActive(job) && <GenericResult job={job} />}
        {!compact && (
          <Text size="xs" c="dimmed">
            Started {formatDate(job.startedAt || job.createdAt)}
            {job.finishedAt ? ` · finished ${formatDate(job.finishedAt)}` : ""}
          </Text>
        )}
      </Stack>
    </Card>
  );
}
