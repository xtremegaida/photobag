import { Button, Card, Group, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDeviceFloppy, IconDownload, IconPackage } from "@tabler/icons-react";
import { useState } from "react";
import { errorMessage } from "../api/client";
import { downloadWhenDone } from "../api/events";
import { useStats, useSubmitJob } from "../api/hooks";
import { FolderPicker } from "../components/FolderPicker";
import { Page } from "../components/Page";
import { RecentJobs } from "../components/RecentJobs";
import { formatBytes } from "../lib/format";

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
        {label}
      </Text>
      <Text fw={600}>{value}</Text>
    </div>
  );
}

export function BackupPage() {
  const { data: stats } = useStats();
  const submit = useSubmitJob();
  const [path, setPath] = useState("");
  const onError = (e: unknown) => notifications.show({ color: "red", message: errorMessage(e) });
  const download = () =>
    submit.mutate({ path: "/api/jobs/backup", body: { mode: "download" } }, { onSuccess: (job) => downloadWhenDone(job.id), onError });
  const toPath = () => submit.mutate({ path: "/api/jobs/backup", body: { mode: "path", path } }, { onError });
  const compact = () => submit.mutate({ path: "/api/jobs/compact", body: {} }, { onError });

  return (
    <Page
      title="Backup"
      description="A backup is a complete, compacted copy of the bag (VACUUM INTO): one self-contained file with every image, tag, run and score. Open it with PhotoBag like any bag."
      side={<RecentJobs kinds={["backup", "compact"]} />}
    >
      {stats && (
        <Card withBorder>
          <SimpleGrid cols={{ base: 2, sm: 4 }}>
            <Stat label="Bag file" value={formatBytes(stats.file.sizeBytes)} />
            <Stat label="Free pages" value={formatBytes(stats.file.freeBytes)} />
            <Stat label="Originals" value={formatBytes(stats.originalBytes)} />
            <Stat label="Images" value={stats.images.toLocaleString()} />
          </SimpleGrid>
          <Text size="xs" c="dimmed" mt="sm" style={{ wordBreak: "break-all" }}>
            {stats.path} · journal {stats.file.journalMode} · schema v{stats.file.schemaVersion}
          </Text>
        </Card>
      )}
      <Card withBorder>
        <Stack>
          <Title order={5}>Download</Title>
          <Text size="sm" c="dimmed">
            The copy is written next to the bag first (it needs about {stats ? formatBytes(stats.file.sizeBytes - stats.file.freeBytes) : "the bag's size"} of free space), then downloaded
            and removed.
          </Text>
          <Group>
            <Button leftSection={<IconDownload size={18} />} onClick={download} loading={submit.isPending}>
              Download backup
            </Button>
          </Group>
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Title order={5}>Save on this machine</Title>
          <FolderPicker
            label="Folder or file path"
            description="A folder gets a timestamped file name; an existing file is never overwritten"
            value={path}
            onChange={setPath}
          />
          <Group>
            <Button leftSection={<IconDeviceFloppy size={18} />} onClick={toPath} disabled={!path.trim()} variant="light">
              Save backup
            </Button>
          </Group>
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Title order={5}>Compact</Title>
          <Text size="sm" c="dimmed">
            Rebuilds the bag in place to give free space back to the disk (for example after emptying the trash). Needs
            free space about the size of the bag and blocks other changes while it runs.
          </Text>
          <Group>
            <Button leftSection={<IconPackage size={18} />} onClick={compact} variant="default">
              Compact bag
            </Button>
          </Group>
        </Stack>
      </Card>
    </Page>
  );
}
