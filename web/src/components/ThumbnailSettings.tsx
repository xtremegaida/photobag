import { Card, Code, Group, Loader, Radio, Stack, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { type ReactNode, useState } from "react";
import { errorMessage } from "../api/client";
import { useSetThumbMode, useStats, useThumbSettings } from "../api/hooks";
import type { ThumbInfo } from "../api/types";
import { formatBytes, plural } from "../lib/format";
import { isActive, useJobStore } from "../stores/jobs";
import { Confirm } from "./Confirm";

type Mode = ThumbInfo["mode"];

/** The space stored thumbnails take, "about" when estimated. */
export const storedSize = (t: ThumbInfo) => `${t.estimated ? "about " : ""}${formatBytes(t.storedBytes)}`;

/** What storing every thumbnail would take, judging by those made so far. */
function wouldTake(t: ThumbInfo) {
  return t.inMemory > 0 ? `about ${formatBytes((t.files * t.memoryBytes) / t.inMemory)}` : undefined;
}

function Choice({ value, label, children }: { value: Mode; label: string; children: ReactNode }) {
  return (
    <Radio.Card value={value} radius="md" p="sm">
      <Group wrap="nowrap" align="flex-start" gap="sm">
        <Radio.Indicator mt={2} />
        <div>
          <Text fw={600} size="sm">
            {label}
          </Text>
          <Text size="sm" c="dimmed">
            {children}
          </Text>
        </div>
      </Group>
    </Radio.Card>
  );
}

/** Where the bag keeps its thumbnails: a trade between space and speed. */
export function ThumbnailSettings() {
  const { data: t } = useThumbSettings();
  const { data: stats } = useStats();
  const setMode = useSetThumbMode();
  const busy = useJobStore((s) => Object.values(s.jobs).some((j) => j.kind === "thumbnails" && isActive(j)));
  const [asking, setAsking] = useState<Mode | null>(null);
  if (!t) return null;

  const change = (mode: Mode) =>
    setMode.mutate(mode, {
      onSuccess: () => setAsking(null),
      onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
    });
  const share = stats?.file.sizeBytes ? Math.round((100 * t.storedBytes) / stats.file.sizeBytes) : 0;
  const would = wouldTake(t);

  return (
    <Card withBorder>
      <Stack>
        <Group justify="space-between">
          <Title order={5}>Thumbnails</Title>
          {busy && (
            <Group gap={6}>
              <Loader size="xs" />
              <Text size="sm" c="dimmed">
                Changing…
              </Text>
            </Group>
          )}
        </Group>
        <Radio.Group value={t.mode} onChange={(v) => !busy && v !== t.mode && setAsking(v as Mode)}>
          <Stack gap="xs">
            <Choice value="stored" label="Stored in the bag">
              Made once, when an image arrives, and kept: the library scrolls quickly, even the first time. They take
              space in the bag{t.mode === "stored" ? `: ${storedSize(t)} now` : would ? ` (${would} here)` : ""}.
            </Choice>
            <Choice value="on-demand" label="Made when shown">
              Not kept in the bag: each is made from its original the first time it is shown, then held in memory until
              PhotoBag stops. Saves the space, but images take longer to appear until their thumbnails are made, large
              photos most of all.
            </Choice>
          </Stack>
        </Radio.Group>
        <Text size="xs" c="dimmed">
          {t.mode === "stored" ? (
            `${plural(t.stored, "thumbnail")} for ${plural(t.files, "image file")}: ${storedSize(t)}${share ? `, ${share}% of the bag` : ""}.`
          ) : (
            <>
              {plural(t.inMemory, "thumbnail")} in memory now ({formatBytes(t.memoryBytes)} of {formatBytes(t.memoryLimit)};
              set the limit with <Code style={{ whiteSpace: "nowrap" }}>photobag serve --thumb-memory</Code>).
            </>
          )}
        </Text>
      </Stack>
      <Confirm
        opened={asking === "on-demand"}
        title="Stop storing thumbnails?"
        confirm="Remove stored thumbnails"
        color="teal"
        loading={setMode.isPending}
        onClose={() => setAsking(null)}
        onConfirm={() => change("on-demand")}
      >
        <Text size="sm">
          This removes the {plural(t.stored, "stored thumbnail")} from the bag and gives their space ({storedSize(t)})
          back to the disk. From then on each thumbnail is made from its original the first time it is shown. You can
          store them again later; that makes them all anew.
        </Text>
      </Confirm>
      <Confirm
        opened={asking === "stored"}
        title="Store thumbnails in the bag?"
        confirm="Make thumbnails"
        color="teal"
        loading={setMode.isPending}
        onClose={() => setAsking(null)}
        onConfirm={() => change("stored")}
      >
        <Text size="sm">
          PhotoBag will make a thumbnail for each of the {plural(t.files, "image file")} and keep it in the bag
          {would ? `, taking ${would}` : ""}. Every image is opened in full for this, so it takes a while in a large
          bag; you can keep working meanwhile.
        </Text>
      </Confirm>
    </Card>
  );
}
