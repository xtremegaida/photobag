import { ActionIcon, Affix, Button, Group, Paper, Progress, ScrollArea, Stack, Text, Tooltip } from "@mantine/core";
import { IconAlertCircle, IconCheck, IconChevronDown, IconChevronUp, IconX } from "@tabler/icons-react";
import { useState } from "react";
import { formatBytes } from "../../lib/format";
import { uploadActive, useUploads, type UploadItem } from "../../stores/uploads";

function ItemLine({ it }: { it: UploadItem }) {
  const pct = it.size ? (it.loaded / it.size) * 100 : it.status === "done" ? 100 : 0;
  return (
    <Stack gap={2}>
      <Group gap={6} wrap="nowrap" justify="space-between">
        <Text size="xs" truncate title={it.path} style={{ flex: 1 }}>
          {it.path}
        </Text>
        {it.status === "done" && <IconCheck size={14} color="var(--mantine-color-teal-6)" />}
        {it.status === "failed" && <IconAlertCircle size={14} color="var(--mantine-color-red-6)" />}
        <Text size="xs" c="dimmed" style={{ flexShrink: 0 }}>
          {it.status === "uploading"
            ? `${Math.round(pct)}%`
            : it.status === "done"
              ? it.outcome === "renamed"
                ? "kept both"
                : it.outcome === "replaced"
                  ? "replaced"
                  : formatBytes(it.size)
              : it.status === "skipped"
                ? "already there"
                : it.status}
        </Text>
      </Group>
      {it.status === "uploading" && <Progress value={pct} size={3} />}
      {it.error && (
        <Text size="xs" c="red" lineClamp={2}>
          {it.error}
        </Text>
      )}
    </Stack>
  );
}

/** Progress of uploads to the bag's files, wherever the user goes meanwhile. */
export function UploadPanel() {
  const { items, dest, cancel, clear } = useUploads();
  const [open, setOpen] = useState(false);
  if (items.length === 0) return null;
  const active = items.filter(uploadActive);
  const failed = items.filter((it) => it.status === "failed").length;
  const total = items.reduce((n, it) => n + it.size, 0);
  const loaded = items.reduce((n, it) => n + (uploadActive(it) ? it.loaded : it.size), 0);
  const done = items.length - active.length;
  return (
    <Affix position={{ bottom: 16, right: 16 }} zIndex={300}>
      <Paper withBorder shadow="md" p="sm" w={340}>
        <Stack gap="xs">
          <Group justify="space-between" wrap="nowrap" gap="xs">
            <div style={{ minWidth: 0 }}>
              <Text size="sm" fw={600} truncate>
                {active.length
                  ? `Uploading… ${done} of ${items.length} done`
                  : failed
                    ? `${failed} upload${failed === 1 ? "" : "s"} failed`
                    : `Uploaded ${items.length} file${items.length === 1 ? "" : "s"}`}
              </Text>
              <Text size="xs" c="dimmed" truncate>
                to {dest || "Files"} · {formatBytes(loaded)} of {formatBytes(total)}
              </Text>
            </div>
            <Group gap={2} wrap="nowrap">
              <ActionIcon variant="subtle" color="gray" onClick={() => setOpen((o) => !o)} aria-label={open ? "Hide the list" : "Show the list"}>
                {open ? <IconChevronDown size={16} /> : <IconChevronUp size={16} />}
              </ActionIcon>
              {active.length ? (
                <Button size="compact-xs" variant="subtle" color="red" onClick={cancel}>
                  Cancel
                </Button>
              ) : (
                <Tooltip label="Close">
                  <ActionIcon variant="subtle" color="gray" onClick={clear} aria-label="Close">
                    <IconX size={16} />
                  </ActionIcon>
                </Tooltip>
              )}
            </Group>
          </Group>
          {active.length > 0 && <Progress value={total ? (loaded / total) * 100 : 0} size="sm" />}
          {(open || failed > 0) && (
            <ScrollArea.Autosize mah={240}>
              <Stack gap={6}>
                {(open ? items : items.filter((it) => it.status === "failed")).map((it) => (
                  <ItemLine key={it.key} it={it} />
                ))}
              </Stack>
            </ScrollArea.Autosize>
          )}
        </Stack>
      </Paper>
    </Affix>
  );
}
