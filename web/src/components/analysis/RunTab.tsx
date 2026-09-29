import { Alert, Anchor, Button, Card, Checkbox, Group, Modal, Select, Stack, Table, Text, Title, Tooltip } from "@mantine/core";
import { useLocalStorage } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconPlayerPlay, IconTagMinus } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { api, errorMessage } from "../../api/client";
import { invalidateTopics, useAnalysisPlan, useAnalysisStats, useStats, useSubmitJob, useTaggerStatus } from "../../api/hooks";
import type { AnalysisOptions, AnalysisSettings, ImageQuery, PipelineStats } from "../../api/types";
import { plural } from "../../lib/format";
import { notReady, PIPELINES, pipelineInfo, usesTagger } from "../../lib/pipelines";
import { isActive, useJobStore } from "../../stores/jobs";
import { QueryBuilder, type Source } from "../QueryBuilder";
import { RecentJobs } from "../RecentJobs";

const MODES = [
  { value: "missing", label: "Only images without a result" },
  { value: "changed", label: "Also redo results made with other settings" },
  { value: "all", label: "Redo every image" },
];

function RemoveTags({ stat }: { stat: PipelineStats }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const qc = useQueryClient();
  const info = pipelineInfo(stat.pipeline);
  const remove = async () => {
    setBusy(true);
    try {
      const r = await api.post<{ removed: number }>("/api/analysis/remove-tags", { pipeline: stat.pipeline });
      notifications.show({ message: `Removed ${plural(r.removed, "tag")} added by ${info.label}` });
      invalidateTopics(qc, ["tags", "images", "analysis"]);
      setOpen(false);
    } catch (e) {
      notifications.show({ color: "red", message: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Tooltip label={`Remove the tags ${info.label} added`}>
        <Anchor size="sm" component="button" type="button" onClick={() => setOpen(true)}>
          {stat.tagLinks.toLocaleString()}
        </Anchor>
      </Tooltip>
      <Modal opened={open} onClose={() => setOpen(false)} title={`Remove the tags added by ${info.label}?`}>
        <Stack>
          <Text size="sm">
            {plural(stat.tagLinks, "tag")} on images came from {info.label}. They are removed, and tags no image carries any
            more are deleted. Tags you added yourself stay, and the {info.label.toLowerCase()} results are kept.
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button color="red" leftSection={<IconTagMinus size={16} />} onClick={remove} loading={busy}>
              Remove tags
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}

function StatsCard() {
  const { data: stats } = useAnalysisStats();
  const { data: lib } = useStats();
  if (!stats || !lib) return null;
  return (
    <Card withBorder>
      <Title order={5} mb="xs">
        Results in this bag
      </Title>
      <Table verticalSpacing={4} fz="sm">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Pipeline</Table.Th>
            <Table.Th ta="right">Images</Table.Th>
            <Table.Th ta="right">Edited</Table.Th>
            <Table.Th ta="right">Tags added</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {stats.map((s) => (
            <Table.Tr key={s.pipeline}>
              <Table.Td>{pipelineInfo(s.pipeline).label}</Table.Td>
              <Table.Td ta="right">
                {s.analysed.toLocaleString()} <Text span c="dimmed" size="xs">of {lib.images.toLocaleString()}</Text>
                {s.pipeline === "ocr" && s.analysed > 0 && (
                  <Text size="xs" c="dimmed">
                    {(s.analysed - s.empty).toLocaleString()} with text
                  </Text>
                )}
              </Table.Td>
              <Table.Td ta="right">{s.edited ? s.edited.toLocaleString() : "–"}</Table.Td>
              <Table.Td ta="right">{s.tagLinks ? <RemoveTags stat={s} /> : "–"}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Card>
  );
}

/** Chooses images and pipelines and starts an analysis job. */
export function RunTab({ settings, onSetup }: { settings: AnalysisSettings; onSetup: (tab: string) => void }) {
  const [sp] = useSearchParams();
  const [query, setQuery] = useState<ImageQuery>({});
  const [pipelines, setPipelines] = useLocalStorage<string[]>({ key: "pb-analysis-pipelines", defaultValue: ["caption"] });
  const [mode, setMode] = useState("missing");
  const opts = useMemo<AnalysisOptions>(() => ({ query, pipelines, mode }), [query, pipelines, mode]);
  const { data: plan, error } = useAnalysisPlan(opts);
  const submit = useSubmitJob();
  const running = useJobStore((s) => Object.values(s.jobs).some((j) => j.kind === "analyze" && isActive(j)));
  const { data: tagger } = useTaggerStatus();
  const blocked = pipelines.map((p) => notReady(settings, p, tagger)).find(Boolean);
  const needsModel = pipelines.some((p) => !usesTagger(settings, p));
  const toggle = (id: string, on: boolean) =>
    setPipelines((ps) => (on ? PIPELINES.map((p) => p.id).filter((p) => p === id || ps.includes(p)) : ps.filter((p) => p !== id)));
  const start = () =>
    submit.mutate(
      { path: "/api/jobs/analyze", body: opts },
      { onError: (e) => notifications.show({ color: "red", title: "Analysis not started", message: errorMessage(e) }) },
    );
  return (
    <Group align="flex-start" gap="xl" wrap="wrap">
      <Stack gap="md" style={{ flex: "1 1 480px", maxWidth: 680 }}>
        {blocked && (
          <Alert
            color="orange"
            variant="light"
            icon={<IconAlertTriangle size={18} />}
            title={needsModel && !settings.endpoint ? "No model connected" : "The tagger is not ready"}
          >
            <Text size="sm">
              {needsModel && !settings.endpoint ? "Set up an OpenAI-compatible endpoint with a vision model first, or choose only pipelines that do not need one." : blocked}{" "}
              <Anchor component="button" type="button" size="sm" onClick={() => onSetup(needsModel && !settings.endpoint ? "connection" : "pipelines")}>
                {needsModel && !settings.endpoint ? "Open Connection" : "Open Pipelines & prompts"}
              </Anchor>
            </Text>
          </Alert>
        )}
        <Card withBorder>
          <Stack>
            <QueryBuilder value={query} onChange={setQuery} initialSource={(sp.get("source") as Source) ?? "all"} label="Images to analyse" />
            <Stack gap={6}>
              <Text size="sm" fw={500}>
                Pipelines
              </Text>
              {PIPELINES.map((p) => (
                <Checkbox
                  key={p.id}
                  label={
                    <Group gap={6} wrap="nowrap">
                      <p.icon size={16} />
                      <span>{p.label}</span>
                      {usesTagger(settings, p.id) && (
                        <Text span size="xs" c="dimmed">
                          (WD tagger)
                        </Text>
                      )}
                      {plan && pipelines.includes(p.id) && (
                        <Text span size="xs" c="dimmed">
                          {plural(plan.byPipeline[p.id] ?? 0, "image")} to do
                        </Text>
                      )}
                    </Group>
                  }
                  description={p.description}
                  checked={pipelines.includes(p.id)}
                  onChange={(e) => toggle(p.id, e.currentTarget.checked)}
                />
              ))}
            </Stack>
            <Select
              label="Which images to send"
              description="Results you corrected by hand are never replaced."
              data={MODES}
              value={mode}
              onChange={(v) => v && setMode(v)}
              allowDeselect={false}
            />
            {error ? (
              <Text size="sm" c="red">
                {errorMessage(error)}
              </Text>
            ) : plan ? (
              <Text size="sm" c="dimmed">
                {plan.requests
                  ? `${plural(plan.requests, "request")} for ${plural(plan.todo, "image")}`
                  : `Nothing to do for these ${plural(plan.images, "image")}`}
                {plan.edited ? ` · ${plural(plan.edited, "edited result")} kept` : ""}
              </Text>
            ) : null}
            <Button
              leftSection={<IconPlayerPlay size={18} />}
              onClick={start}
              loading={submit.isPending}
              disabled={!!blocked || !plan?.requests}
            >
              {running ? "Queue after the running analysis" : "Start analysis"}
            </Button>
          </Stack>
        </Card>
      </Stack>
      <Stack gap="md" style={{ flex: "1 1 420px", maxWidth: 680 }}>
        <StatsCard />
        <RecentJobs kinds={["analyze"]} />
      </Stack>
    </Group>
  );
}
