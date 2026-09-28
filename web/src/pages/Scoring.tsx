import {
  ActionIcon,
  Anchor,
  Autocomplete,
  Badge,
  Button,
  Card,
  Group,
  Menu,
  Modal,
  Progress,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  Textarea,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconChartBar,
  IconDots,
  IconPencil,
  IconPlayerPause,
  IconPlayerPlay,
  IconPlus,
  IconTrash,
  IconTrophy,
} from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { api, errorMessage } from "../api/client";
import { invalidateTopics, useMetrics, useRuns } from "../api/hooks";
import type { ImageQuery, Metric, Run } from "../api/types";
import { Page } from "../components/Page";
import { QueryBuilder, type Source } from "../components/QueryBuilder";
import { formatDate, plural } from "../lib/format";

function NewRunModal({
  opened,
  onClose,
  initialSource,
  initialMetric,
}: {
  opened: boolean;
  onClose: () => void;
  initialSource: Source;
  initialMetric?: string;
}) {
  const { data: metrics = [] } = useMetrics();
  const [metric, setMetric] = useState(initialMetric ?? "");
  const [query, setQuery] = useState<ImageQuery>({});
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();
  const qc = useQueryClient();
  const existing = metrics.find((m) => m.name.toLocaleLowerCase() === metric.trim().toLocaleLowerCase());
  const start = async () => {
    setBusy(true);
    try {
      const run = await api.post<Run>("/api/runs", { metricName: metric.trim(), query });
      invalidateTopics(qc, ["runs", "metrics"]);
      onClose();
      navigate(`/scoring/runs/${run.id}`);
    } catch (e) {
      notifications.show({ color: "red", title: "Could not start the run", message: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened={opened} onClose={onClose} title="New scoring run" size="lg">
      <Stack>
        <Autocomplete
          label="Metric"
          description="What “better” means for this run — pick an existing metric or type a new one (e.g. Composition, Sharpness)"
          data={metrics.map((m) => m.name)}
          value={metric}
          onChange={setMetric}
          placeholder="Metric name"
          data-autofocus
        />
        {metric.trim() && (
          <Text size="xs" c="dimmed">
            {existing
              ? `Adds to “${existing.name}” (${plural(existing.comparisons, "comparison")} so far); scores combine every run.`
              : `Creates a new metric “${metric.trim()}”.`}
          </Text>
        )}
        <QueryBuilder value={query} onChange={setQuery} initialSource={initialSource} showPairs label="Images to compare" />
        <Text size="xs" c="dimmed">
          Pairs are shown in random order and you can stop at any time; every answer counts.
        </Text>
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={start} loading={busy} disabled={!metric.trim()}>
            Start comparing
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function EditMetricModal({ metric, onClose }: { metric: Metric | null; onClose: () => void }) {
  const [name, setName] = useState(metric?.name ?? "");
  const [desc, setDesc] = useState(metric?.description ?? "");
  const qc = useQueryClient();
  const save = async () => {
    if (!metric) return;
    try {
      await api.patch(`/api/metrics/${metric.id}`, { name, description: desc });
      invalidateTopics(qc, ["metrics", "runs"]);
      onClose();
    } catch (e) {
      notifications.show({ color: "red", message: errorMessage(e) });
    }
  };
  return (
    <Modal opened={!!metric} onClose={onClose} title="Edit metric">
      <Stack>
        <TextInput label="Name" value={name} onChange={(e) => setName(e.currentTarget.value)} />
        <Textarea label="Description" value={desc} onChange={(e) => setDesc(e.currentTarget.value)} autosize minRows={2} />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={save} disabled={!name.trim()}>
            Save
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function DeleteMetricModal({ metric, onClose }: { metric: Metric | null; onClose: () => void }) {
  const qc = useQueryClient();
  const del = async () => {
    if (!metric) return;
    try {
      await api.del(`/api/metrics/${metric.id}`);
      invalidateTopics(qc, ["metrics", "runs"]);
      onClose();
    } catch (e) {
      notifications.show({ color: "red", message: errorMessage(e) });
    }
  };
  return (
    <Modal opened={!!metric} onClose={onClose} title={`Delete metric “${metric?.name}”?`}>
      <Stack>
        <Text size="sm">
          This deletes its {plural(metric?.runs ?? 0, "run")} and {plural(metric?.comparisons ?? 0, "comparison")}. Images are
          not affected.
        </Text>
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Cancel
          </Button>
          <Button color="red" onClick={del}>
            Delete metric
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

const statusColor: Record<string, string> = { active: "blue", stopped: "gray", complete: "teal" };

function RunsTable({ runs }: { runs: Run[] }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [deleting, setDeleting] = useState<Run | null>(null);
  const setStatus = async (r: Run, status: string) => {
    try {
      await api.post(`/api/runs/${r.id}/status`, { status });
      invalidateTopics(qc, ["runs"]);
    } catch (e) {
      notifications.show({ color: "red", message: errorMessage(e) });
    }
  };
  const del = async () => {
    if (!deleting) return;
    try {
      await api.del(`/api/runs/${deleting.id}`);
      invalidateTopics(qc, ["runs", "metrics"]);
      setDeleting(null);
    } catch (e) {
      notifications.show({ color: "red", message: errorMessage(e) });
    }
  };
  if (runs.length === 0) return <Text c="dimmed">No runs yet.</Text>;
  return (
    <>
      <Table highlightOnHover verticalSpacing="xs">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Metric</Table.Th>
            <Table.Th>Images</Table.Th>
            <Table.Th w={220}>Progress</Table.Th>
            <Table.Th>Status</Table.Th>
            <Table.Th>Started</Table.Th>
            <Table.Th w={150} />
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {runs.map((r) => (
            <Table.Tr key={r.id}>
              <Table.Td>
                <Anchor component={Link} to={`/scoring/metrics/${r.metricId}`}>
                  {r.metricName}
                </Anchor>
              </Table.Td>
              <Table.Td>
                <Text size="sm">{plural(r.imageCount, "image")}</Text>
                <Text size="xs" c="dimmed">
                  {r.description}
                </Text>
              </Table.Td>
              <Table.Td>
                <Progress value={(r.position / Math.max(1, r.totalPairs)) * 100} size="sm" />
                <Text size="xs" c="dimmed" mt={2}>
                  {r.position.toLocaleString()} / {r.totalPairs.toLocaleString()} pairs · {r.compared} answered
                  {r.skipped ? `, ${r.skipped} skipped` : ""}
                </Text>
              </Table.Td>
              <Table.Td>
                <Badge variant="light" color={statusColor[r.status]}>
                  {r.status}
                </Badge>
              </Table.Td>
              <Table.Td>
                <Text size="xs">{formatDate(r.createdAt)}</Text>
              </Table.Td>
              <Table.Td>
                <Group gap={4} justify="flex-end" wrap="nowrap">
                  {r.status !== "complete" && (
                    <Button size="compact-sm" leftSection={<IconPlayerPlay size={14} />} onClick={() => navigate(`/scoring/runs/${r.id}`)}>
                      {r.position > 0 ? "Continue" : "Start"}
                    </Button>
                  )}
                  <Menu position="bottom-end">
                    <Menu.Target>
                      <ActionIcon variant="subtle">
                        <IconDots size={16} />
                      </ActionIcon>
                    </Menu.Target>
                    <Menu.Dropdown>
                      {r.status === "active" && (
                        <Menu.Item leftSection={<IconPlayerPause size={14} />} onClick={() => setStatus(r, "stopped")}>
                          Stop
                        </Menu.Item>
                      )}
                      <Menu.Item leftSection={<IconTrophy size={14} />} component={Link} to={`/scoring/metrics/${r.metricId}`}>
                        Rankings
                      </Menu.Item>
                      <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={() => setDeleting(r)}>
                        Delete run
                      </Menu.Item>
                    </Menu.Dropdown>
                  </Menu>
                </Group>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      <Modal opened={!!deleting} onClose={() => setDeleting(null)} title="Delete this run?">
        <Stack>
          <Text size="sm">
            Its {plural(deleting?.compared ?? 0, "answer")} will no longer count towards “{deleting?.metricName}”.
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button color="red" onClick={del}>
              Delete run
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}

export function ScoringPage() {
  const [sp, setSp] = useSearchParams();
  const { data: metrics = [] } = useMetrics();
  const { data: runs = [] } = useRuns();
  const [editing, setEditing] = useState<Metric | null>(null);
  const [deleting, setDeleting] = useState<Metric | null>(null);
  const [newFor, setNewFor] = useState<string | undefined>();
  const newParam = sp.get("new");
  const [open, setOpen] = useState(newParam !== null);
  const close = () => {
    setOpen(false);
    setNewFor(undefined);
    if (newParam !== null) setSp({}, { replace: true });
  };
  return (
    <Page
      title="Scoring"
      description="Rate images by comparing two at a time. Each run answers “which is better?” for one metric; all runs of a metric are combined with a Bradley–Terry model, and contradicting answers for the same pair cancel out."
      actions={
        <Button leftSection={<IconPlus size={18} />} onClick={() => setOpen(true)}>
          New run
        </Button>
      }
    >
      <Stack gap="xs">
        <Text fw={600}>Metrics</Text>
        {metrics.length === 0 ? (
          <Text c="dimmed" size="sm">
            No metrics yet — start a run and name one.
          </Text>
        ) : (
          <SimpleGrid cols={{ base: 1, sm: 2, lg: 3, xl: 4 }}>
            {metrics.map((m) => (
              <Card key={m.id} withBorder>
                <Stack gap={6}>
                  <Group justify="space-between" wrap="nowrap">
                    <Text fw={600} truncate>
                      {m.name}
                    </Text>
                    <Menu position="bottom-end">
                      <Menu.Target>
                        <ActionIcon variant="subtle">
                          <IconDots size={16} />
                        </ActionIcon>
                      </Menu.Target>
                      <Menu.Dropdown>
                        <Menu.Item leftSection={<IconPencil size={14} />} onClick={() => setEditing(m)}>
                          Edit
                        </Menu.Item>
                        <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={() => setDeleting(m)}>
                          Delete
                        </Menu.Item>
                      </Menu.Dropdown>
                    </Menu>
                  </Group>
                  {m.description && (
                    <Text size="sm" c="dimmed" lineClamp={2}>
                      {m.description}
                    </Text>
                  )}
                  <Text size="xs" c="dimmed">
                    {plural(m.comparisons, "comparison")} · {plural(m.runs, "run")}
                    {m.ranked ? ` · ${m.ranked} ranked` : ""}
                    {m.cancelledPairs ? ` · ${plural(m.cancelledPairs, "cancelled pair")}` : ""}
                  </Text>
                  <Group gap="xs" mt={4}>
                    <Button size="xs" variant="light" leftSection={<IconChartBar size={14} />} component={Link} to={`/scoring/metrics/${m.id}`}>
                      Rankings
                    </Button>
                    <Button
                      size="xs"
                      variant="default"
                      leftSection={<IconPlus size={14} />}
                      onClick={() => {
                        setNewFor(m.name);
                        setOpen(true);
                      }}
                    >
                      New run
                    </Button>
                  </Group>
                </Stack>
              </Card>
            ))}
          </SimpleGrid>
        )}
      </Stack>
      <Stack gap="xs">
        <Text fw={600}>Runs</Text>
        <RunsTable runs={runs} />
      </Stack>
      <NewRunModal
        key={`${open}-${newFor}`}
        opened={open}
        onClose={close}
        initialSource={newParam === "selection" ? "selection" : "all"}
        initialMetric={newFor}
      />
      <EditMetricModal key={editing?.id} metric={editing} onClose={() => setEditing(null)} />
      <DeleteMetricModal metric={deleting} onClose={() => setDeleting(null)} />
    </Page>
  );
}
