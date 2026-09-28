import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Center,
  Group,
  Loader,
  Modal,
  NumberInput,
  SegmentedControl,
  SimpleGrid,
  Slider,
  Stack,
  Text,
  Title,
  Tooltip,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconCopy, IconSearch, IconStar, IconStarFilled } from "@tabler/icons-react";
import { useEffect, useMemo, useState } from "react";
import { errorMessage, previewUrl, thumbUrl } from "../api/client";
import { useResolve, useScan, useScans, useSubmitJob } from "../api/hooks";
import type { Cluster, DedupParams, Image, ImageQuery, Resolution } from "../api/types";
import { Page } from "../components/Page";
import { QueryBuilder } from "../components/QueryBuilder";
import { RecentJobs } from "../components/RecentJobs";
import { formatBytes, formatTaken, percent, plural } from "../lib/format";
import { useJobStore } from "../stores/jobs";
import classes from "./Dedup.module.css";

type Mark = "keep" | "delete";

function ComparePair({ a, b, onClose }: { a?: Image; b?: Image; onClose: () => void }) {
  return (
    <Modal opened={!!a && !!b} onClose={onClose} fullScreen title="Compare">
      {a && b && (
        <Stack gap="xs">
          <div className={classes.compare}>
            <img src={previewUrl(a.id, 1600)} alt={a.name} />
            <img src={previewUrl(b.id, 1600)} alt={b.name} />
          </div>
          <Group grow>
            {[a, b].map((im) => (
              <Text key={im.id} size="sm" ta="center">
                {im.name} · {im.width}×{im.height} · {formatBytes(im.size)} · {im.format.toUpperCase()}
              </Text>
            ))}
          </Group>
        </Stack>
      )}
    </Modal>
  );
}

function ClusterCard({
  cluster,
  images,
  scanId,
  threshold,
}: {
  cluster: Cluster;
  images: Map<number, Image>;
  scanId: string;
  threshold: number;
}) {
  const [keeper, setKeeper] = useState(cluster.keeper);
  const [marks, setMarks] = useState<Record<number, Mark>>(() =>
    Object.fromEntries(cluster.members.map((id) => [id, cluster.proposed.includes(id) ? "delete" : "keep"])),
  );
  const [compare, setCompare] = useState<number | null>(null);
  const resolve = useResolve();
  const sims = new Map(cluster.members.map((id, i) => [id, cluster.keeperSim[i]]));
  const markOf = (id: number): Mark => (id === keeper ? "keep" : (marks[id] ?? "keep"));
  const deleting = cluster.members.filter((id) => markOf(id) === "delete");

  const submit = (res: Resolution) =>
    resolve.mutate(
      { scanId, resolutions: [res] },
      { onError: (e) => notifications.show({ color: "red", title: "Could not resolve", message: errorMessage(e) }) },
    );
  const apply = () =>
    submit({
      keep: keeper,
      delete: deleting,
      keepAlso: cluster.members.filter((id) => id !== keeper && markOf(id) === "keep"),
    });
  const distinct = () => submit({ keep: keeper, keepAlso: cluster.members.filter((id) => id !== keeper) });

  return (
    <Card withBorder>
      <Stack gap="sm">
        <Group justify="space-between">
          <Group gap="xs">
            <Badge variant="light">{percent(cluster.maxSim)} similar</Badge>
            <Text size="sm" c="dimmed">
              {plural(cluster.members.length, "image")}
            </Text>
          </Group>
          <Group gap="xs">
            <Button size="xs" variant="default" onClick={distinct} loading={resolve.isPending}>
              Not duplicates
            </Button>
            <Button size="xs" color={deleting.length ? "red" : undefined} onClick={apply} loading={resolve.isPending}>
              {deleting.length ? `Keep ★, trash ${deleting.length}` : "Keep all"}
            </Button>
          </Group>
        </Group>
        <div className={classes.members}>
          {cluster.members.map((id) => {
            const im = images.get(id);
            const mark = markOf(id);
            const sim = id === keeper ? 1 : (sims.get(id) ?? 0);
            return (
              <div key={id} className={classes.member} data-delete={mark === "delete" || undefined} data-keeper={id === keeper || undefined}>
                <img
                  className={classes.thumb}
                  src={thumbUrl(id)}
                  alt={im?.name}
                  loading="lazy"
                  onClick={() => setCompare(id === keeper ? (cluster.members.find((m) => m !== keeper) ?? null) : id)}
                />
                <Stack gap={4} mt={6}>
                  <Group gap={4} wrap="nowrap" justify="space-between">
                    <Text size="sm" fw={600} truncate title={im?.name}>
                      {im?.name ?? `#${id}`}
                    </Text>
                    <Tooltip label={id === keeper ? "Kept image (duplicates merge into it)" : "Make this the kept image"}>
                      <ActionIcon variant="subtle" color="yellow" size="sm" onClick={() => setKeeper(id)}>
                        {id === keeper ? <IconStarFilled size={16} /> : <IconStar size={16} />}
                      </ActionIcon>
                    </Tooltip>
                  </Group>
                  {im && (
                    <>
                      <Text size="xs" c="dimmed">
                        {im.width}×{im.height} · {formatBytes(im.size)} · {im.format.toUpperCase()}
                      </Text>
                      <Text size="xs" c="dimmed" truncate title={im.originalPath}>
                        {im.originalPath}
                      </Text>
                      <Text size="xs" c="dimmed">
                        {formatTaken(im.takenAt)}
                        {im.tags.length ? ` · ${im.tags.length} tag${im.tags.length > 1 ? "s" : ""}` : ""}
                      </Text>
                    </>
                  )}
                  <Group gap={4} justify="space-between">
                    <Badge size="sm" variant="light" color={id === keeper ? "teal" : sim >= threshold ? "orange" : "gray"}>
                      {id === keeper ? "keeper" : `${percent(sim)} to ★`}
                    </Badge>
                    {id !== keeper && (
                      <SegmentedControl
                        size="xs"
                        data={[
                          { value: "keep", label: "Keep" },
                          { value: "delete", label: "Trash" },
                        ]}
                        value={mark}
                        color={mark === "delete" ? "red" : undefined}
                        onChange={(v) => setMarks((m) => ({ ...m, [id]: v as Mark }))}
                      />
                    )}
                  </Group>
                </Stack>
              </div>
            );
          })}
        </div>
      </Stack>
      <ComparePair
        a={images.get(keeper)}
        b={compare !== null ? images.get(compare) : undefined}
        onClose={() => setCompare(null)}
      />
    </Card>
  );
}

function ScanForm({ onStarted }: { onStarted: (jobId: string) => void }) {
  const [mode, setMode] = useState("similar");
  const [neighbors, setNeighbors] = useState<number | string>(8);
  const [scope, setScope] = useState<ImageQuery>({});
  const submit = useSubmitJob();
  const start = () => {
    const body: DedupParams = { mode, neighbors: Number(neighbors) || 8, scope };
    submit.mutate(
      { path: "/api/dedup/scans", body },
      {
        onSuccess: (job) => onStarted(job.id),
        onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
      },
    );
  };
  return (
    <Card withBorder>
      <Stack>
        <SegmentedControl
          data={[
            { value: "similar", label: "Visually similar" },
            { value: "exact", label: "Bit-identical" },
          ]}
          value={mode}
          onChange={setMode}
        />
        <Text size="sm" c="dimmed">
          {mode === "similar"
            ? "Each image is compared with its closest images by thumbprint (perceptual hash and colour layout). Pairs above the threshold are grouped; resized, re-compressed and lightly edited copies are found."
            : "Groups files whose bytes are exactly the same (for example the same photo imported twice)."}
        </Text>
        {mode === "similar" && (
          <NumberInput
            label="Neighbours per image (N)"
            description="How many nearest images to compare each image with"
            min={1}
            max={64}
            value={neighbors}
            onChange={setNeighbors}
            w={260}
          />
        )}
        <QueryBuilder value={scope} onChange={setScope} label="Look for duplicates among" />
        <Group>
          <Button leftSection={<IconSearch size={18} />} onClick={start} loading={submit.isPending}>
            Find duplicates
          </Button>
        </Group>
      </Stack>
    </Card>
  );
}

export function DedupPage() {
  const { data: scans } = useScans();
  const [scanId, setScanId] = useState<string | undefined>();
  const [jobId, setJobId] = useState<string | undefined>();
  const job = useJobStore((s) => (jobId ? s.jobs[jobId] : undefined));
  const [threshold, setThreshold] = useState(0.9);
  const [thresholdD] = useDebouncedValue(threshold, 150);
  const [shown, setShown] = useState(20);
  const [confirmAll, setConfirmAll] = useState(false);
  const resolve = useResolve();

  useEffect(() => {
    if (!scanId && scans?.length) setScanId(scans[0].id);
  }, [scans, scanId]);
  useEffect(() => {
    const r = job?.result as { id?: string } | undefined;
    if (job?.status === "done" && r?.id) {
      setScanId(r.id);
      setShown(20);
    }
  }, [job]);

  const { data: view, error, isFetching } = useScan(scanId, thresholdD);
  const images = useMemo(() => new Map((view?.images ?? []).map((im) => [im.id, im])), [view]);
  const exact = view?.scan.params.mode === "exact";
  const proposed = view?.clusters.reduce((n, c) => n + c.proposed.length, 0) ?? 0;

  const applyAll = () =>
    resolve.mutate(
      { scanId: scanId!, applyAll: true, threshold: exact ? 1 : thresholdD },
      {
        onSuccess: (r) => {
          notifications.show({ message: `Moved ${plural(r.trashed, "duplicate")} to the trash` });
          setConfirmAll(false);
        },
        onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
      },
    );

  return (
    <Page
      title="Duplicates"
      description="Find duplicates, review each group and choose what to keep. Trashed duplicates are merged into the kept image: its tags are combined and their comparisons count for it. Nothing is deleted until you empty the trash."
    >
      <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="xl">
        <ScanForm onStarted={setJobId} />
        <RecentJobs kinds={["dedup-scan"]} limit={2} title="Scans" />
      </SimpleGrid>
      {error && (
        <Alert color="orange" title="Scan unavailable">
          {errorMessage(error)}
        </Alert>
      )}
      {view && (
        <Stack gap="sm">
          <Card withBorder>
            <Stack gap="xs">
              <Group justify="space-between">
                <Title order={5}>
                  {exact ? "Identical files" : "Similar images"} · {plural(view.clusters.length, "group")}
                </Title>
                <Group gap="xs">
                  {isFetching && <Loader size="xs" />}
                  <Button size="xs" color="red" variant="light" disabled={!proposed} onClick={() => setConfirmAll(true)}>
                    Trash all {proposed} proposed
                  </Button>
                </Group>
              </Group>
              <Text size="xs" c="dimmed">
                Scanned {plural(view.scan.scanned, "image")} {exact ? "" : `(N = ${view.scan.params.neighbors})`} in{" "}
                {(view.scan.millis / 1000).toFixed(1)}s · {new Date(view.scan.createdAt).toLocaleTimeString()}
              </Text>
              {!exact && (
                <div>
                  <Text size="sm" mb={4}>
                    Similarity threshold: <b>{percent(threshold)}</b>
                  </Text>
                  <Slider
                    min={0.5}
                    max={1}
                    step={0.005}
                    value={threshold}
                    onChange={setThreshold}
                    label={(v) => percent(v)}
                    marks={[
                      { value: 0.6, label: "loose" },
                      { value: 0.9, label: "default" },
                      { value: 0.98, label: "strict" },
                    ]}
                    mb="md"
                  />
                </div>
              )}
            </Stack>
          </Card>
          {view.clusters.length === 0 ? (
            <Center p="xl">
              <Stack align="center" gap={4}>
                <IconCopy size={32} color="var(--mantine-color-dimmed)" />
                <Text c="dimmed">No duplicates {exact ? "found" : "at this threshold"}.</Text>
              </Stack>
            </Center>
          ) : (
            <>
              {view.clusters.slice(0, shown).map((c) => (
                <ClusterCard
                  key={`${c.members.join("-")}@${thresholdD}`}
                  cluster={c}
                  images={images}
                  scanId={scanId!}
                  threshold={exact ? 1 : thresholdD}
                />
              ))}
              {shown < view.clusters.length && (
                <Button variant="default" onClick={() => setShown((s) => s + 20)}>
                  Show more ({view.clusters.length - shown} left)
                </Button>
              )}
            </>
          )}
        </Stack>
      )}
      <Modal opened={confirmAll} onClose={() => setConfirmAll(false)} title="Trash all proposed duplicates?">
        <Stack>
          <Text size="sm">
            {plural(proposed, "image")} in {plural(view?.clusters.length ?? 0, "group")} will be moved to the trash and
            merged into the image kept for their group. You can restore them from the trash.
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setConfirmAll(false)}>
              Cancel
            </Button>
            <Button color="red" onClick={applyAll} loading={resolve.isPending}>
              Trash {proposed}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Page>
  );
}
