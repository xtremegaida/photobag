import {
  Alert,
  Anchor,
  Button,
  Group,
  Image,
  Loader,
  NumberInput,
  Paper,
  Progress,
  SegmentedControl,
  Select,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconPlayerPlay, IconPlayerStop } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link } from "react-router";
import { api, errorMessage } from "../../api/client";
import { gk, useComfyNodes, useComfySettings, usePlan, useWorkflowVersion, useWorkflows } from "../../api/generate";
import { invalidateTopics } from "../../api/hooks";
import type { ExperimentView, GenerateProgress, GenerateRequest, InputSpec, Job, Override } from "../../api/types";
import { findNode, formatValue, sameInput, valueCount, valueKind } from "../../lib/generate";
import { isActive, useJob } from "../../stores/jobs";
import { OverrideEditor } from "./OverrideEditor";

const SEP = "\u0000";

function RunningJob({ job }: { job: Job }) {
  const p = (job.progress ?? {}) as Partial<GenerateProgress>;
  const total = p.prompts ?? 0;
  return (
    <Paper withBorder p="sm" radius="md">
      <Stack gap={6}>
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap">
            <Loader size="xs" />
            <Text size="sm" fw={500}>
              {job.status === "queued"
                ? "Waiting to start…"
                : `${p.done ?? 0} of ${total} done · ${p.images ?? 0} image${p.images === 1 ? "" : "s"}${p.failed ? ` · ${p.failed} failed` : ""}`}
            </Text>
          </Group>
          <Button
            size="compact-xs"
            variant="light"
            color="red"
            leftSection={<IconPlayerStop size={14} />}
            onClick={() => api.post(`/api/jobs/${job.id}/cancel`)}
          >
            Stop
          </Button>
        </Group>
        <Progress value={total ? ((p.done ?? 0) / total) * 100 : 0} size="sm" />
        {p.current && (
          <Text size="xs" c="dimmed" lineClamp={2}>
            {p.current}
          </Text>
        )}
        {!!p.steps && (
          <Group gap="xs" wrap="nowrap">
            <Text size="xs" c="dimmed" w={130} truncate>
              {p.node} {p.step}/{p.steps}
            </Text>
            <Progress value={((p.step ?? 0) / p.steps) * 100} size="xs" style={{ flex: 1 }} color="gray" />
          </Group>
        )}
        {!!p.preview && <Image src={`/api/generate/preview/${job.id}?n=${p.preview}`} maw={256} radius="sm" alt="Sampler preview" />}
        {p.lastError && (
          <Text size="xs" c="red" lineClamp={3}>
            Last failure: {p.lastError}
          </Text>
        )}
      </Stack>
    </Paper>
  );
}

/** What to generate: workflow, overrides, count and seeds; and the button. */
export function GenerateForm({
  experiment,
  draft,
  onChange,
  jobId,
  onStarted,
}: {
  experiment: ExperimentView;
  draft: GenerateRequest;
  onChange: (r: GenerateRequest) => void;
  jobId?: string;
  onStarted: (job: Job) => void;
}) {
  const qc = useQueryClient();
  const { data: settings } = useComfySettings();
  const { data: workflows } = useWorkflows();
  const template = workflows?.find((w) => w.id === draft.workflowId);
  const versionId = draft.versionId || template?.versionId;
  const { data: version } = useWorkflowVersion(versionId);
  const { data: specs } = useComfyNodes(version?.classes ?? [], !!settings?.endpoint);
  const [debounced] = useDebouncedValue(draft, 300);
  const { data: plan, isFetching: planning } = usePlan(experiment.id, debounced, !!versionId);
  const liveJob = useJob(jobId);
  const job = liveJob && isActive(liveJob) ? liveJob : undefined;
  const [starting, setStarting] = useState(false);
  const [search, setSearch] = useState("");

  const nodes = useMemo(() => version?.nodes ?? [], [version]);
  const specOf = (nodeRef: string, input: string): InputSpec | undefined => {
    const n = findNode(nodes, nodeRef);
    return n ? specs?.nodes[n.classType]?.inputs.find((i) => i.name === input) : undefined;
  };
  const currentOf = (nodeRef: string, input: string) => findNode(nodes, nodeRef)?.inputs.find((i) => i.name === input)?.value;

  const addable = useMemo(
    () =>
      nodes
        .filter((n) => n.inputs.length > 0)
        .map((n) => ({
          group: n.title === n.classType ? n.title : `${n.title} (${n.classType})`,
          items: n.inputs
            .filter((i) => !draft.overrides.some((o) => sameInput(o, { node: n.ref, input: i.name })))
            .map((i) => ({ value: n.ref + SEP + i.name, label: `${n.title} › ${i.name} = ${formatValue(i.value, 30)}` })),
        }))
        .filter((g) => g.items.length > 0),
    [nodes, draft.overrides],
  );

  const setOverride = (i: number, o: Override) => onChange({ ...draft, overrides: draft.overrides.map((x, j) => (j === i ? o : x)) });
  const add = (key: string | null) => {
    if (!key) return;
    const [node, input] = key.split(SEP);
    onChange({ ...draft, overrides: [...draft.overrides, { node, input, value: currentOf(node, input) }] });
    setSearch("");
  };
  const sweeping = draft.overrides.some((o) => o.sweep);
  const missing = (o: Override) => {
    if (!version) return undefined;
    const n = findNode(nodes, o.node);
    if (!n) return `This workflow has no node titled “${o.node}”.`;
    if (!n.inputs.some((i) => i.name === o.input)) return `“${o.node}” has no input “${o.input}” that can be set.`;
    return undefined;
  };

  const generate = async () => {
    setStarting(true);
    try {
      const j = await api.post<Job>(`/api/experiments/${experiment.id}/generate`, { request: draft });
      onStarted(j);
      invalidateTopics(qc, ["experiments"]);
      qc.invalidateQueries({ queryKey: gk.runs(experiment.id) });
    } catch (e) {
      notifications.show({ color: "red", title: "Could not start", message: errorMessage(e) });
    } finally {
      setStarting(false);
    }
  };

  const noWorkflows = workflows && workflows.length === 0;
  const blocked = !settings?.endpoint ? "Set ComfyUI's address first (Generate → ComfyUI)." : plan?.error;
  return (
    <Stack gap="md">
      <Select
        label="Workflow"
        placeholder={noWorkflows ? "No workflows yet" : "Choose a workflow"}
        data={(workflows ?? []).map((w) => ({ value: String(w.id), label: w.name }))}
        value={draft.workflowId ? String(draft.workflowId) : null}
        onChange={(v) => onChange({ ...draft, workflowId: Number(v) || 0, versionId: undefined })}
        allowDeselect={false}
        searchable
      />
      {noWorkflows && (
        <Text size="sm">
          <Anchor component={Link} to="/generate?tab=workflows" size="sm">
            Add a workflow
          </Anchor>{" "}
          exported from ComfyUI in the API format.
        </Text>
      )}
      {draft.versionId && template && template.versionId !== draft.versionId && (
        <Alert color="blue" variant="light" p="xs">
          <Stack gap={4}>
            <Text size="sm">
              These settings use the version of “{template.name}” an image was made with; the workflow has changed since.
            </Text>
            <Button size="compact-xs" variant="light" w="fit-content" onClick={() => onChange({ ...draft, versionId: undefined })}>
              Use the current version
            </Button>
          </Stack>
        </Alert>
      )}
      {draft.versionId && !template && (
        <Alert color="blue" variant="light" p="xs">
          <Text size="sm">Using the workflow an image was made with (its template has been deleted).</Text>
        </Alert>
      )}
      {version?.problems.map((p) => (
        <Alert key={p} color="orange" variant="light" p="xs">
          <Text size="xs">{p}</Text>
        </Alert>
      ))}
      {specs?.error && (
        <Text size="xs" c="orange">
          ComfyUI's node definitions are unavailable ({specs.error}), so values are edited as plain text and numbers.
        </Text>
      )}

      {version && (
        <Stack gap="xs">
          <Title order={6}>Overrides</Title>
          {draft.overrides.length === 0 && (
            <Text size="sm" c="dimmed">
              None: the workflow runs as it is. Add the inputs to change, and sweep one or more of them to compare values.
            </Text>
          )}
          {draft.overrides.map((o, i) => {
            const spec = specOf(o.node, o.input);
            const current = currentOf(o.node, o.input);
            return (
              <OverrideEditor
                key={o.node + SEP + o.input}
                override={o}
                spec={spec}
                kind={valueKind(spec, current)}
                current={current}
                missing={missing(o)}
                onChange={(n) => setOverride(i, n)}
                onRemove={() => onChange({ ...draft, overrides: draft.overrides.filter((_, j) => j !== i) })}
              />
            );
          })}
          <Select
            placeholder="Add an override…"
            data={addable}
            value={null}
            onChange={add}
            searchable
            searchValue={search}
            onSearchChange={setSearch}
            size="sm"
            nothingFoundMessage="No such input"
            maxDropdownHeight={360}
            comboboxProps={{ withinPortal: true }}
          />
        </Stack>
      )}

      {version && (
        <Group align="flex-start" grow>
          <NumberInput
            label={sweeping ? "Images per combination" : "Images"}
            min={1}
            max={500}
            value={draft.count || 1}
            onChange={(v) => onChange({ ...draft, count: typeof v === "number" ? v : 1 })}
            allowDecimal={false}
          />
          <Stack gap={4}>
            <Text size="sm" fw={500}>
              Seed
            </Text>
            <SegmentedControl
              size="xs"
              value={draft.seed || "random"}
              onChange={(v) => onChange({ ...draft, seed: v })}
              data={[
                { value: "random", label: "Random" },
                { value: "increment", label: "Count up" },
                { value: "fixed", label: "Keep" },
              ]}
            />
          </Stack>
        </Group>
      )}
      {version && (
        <Text size="xs" c="dimmed">
          {plan && !plan.error && plan.seedInputs.length === 0 && draft.seed !== "fixed"
            ? "Every seed is set by an override (or the workflow has none), so the seed policy changes nothing."
            : draft.seed === "fixed"
            ? "Seeds stay as the workflow (or an override) sets them."
            : draft.seed === "increment"
              ? "Each image uses the workflow's seed plus its number."
              : sweeping
                ? "Each image gets a random seed; every combination uses the same seeds, so only the swept values differ."
                : "Each image gets a random seed."}
          {plan && plan.seedInputs.length > 0 && draft.seed !== "fixed" && ` Sets ${plan.seedInputs.join(", ")}.`}
        </Text>
      )}

      {version && plan && (
        <Stack gap={4}>
          {plan.error ? (
            <Alert color="red" variant="light" p="xs">
              <Text size="sm">{plan.error}</Text>
            </Alert>
          ) : (
            <Text size="sm" fw={500} c={planning ? "dimmed" : undefined}>
              {plan.prompts} image{plan.prompts === 1 ? "" : "s"}
              {plan.combos > 1 &&
                `: ${plan.dims.map((d) => `${d.values.length} ${d.input}`).join(" × ")}${plan.count > 1 ? ` × ${plan.count} each` : ""}`}
            </Text>
          )}
          {plan.warnings.map((w) => (
            <Text key={w} size="xs" c="orange">
              {w}
            </Text>
          ))}
          {draft.overrides.some((o) => o.sweep && valueCount(o) === 0) && (
            <Text size="xs" c="red">
              A sweep has no values yet.
            </Text>
          )}
        </Stack>
      )}

      {job ? (
        <RunningJob job={job} />
      ) : (
        <Group gap="xs">
          <Button
            leftSection={<IconPlayerPlay size={16} />}
            onClick={generate}
            loading={starting}
            disabled={!version || !!blocked || !plan || planning}
          >
            Generate{plan && !plan.error ? ` ${plan.prompts}` : ""}
          </Button>
          {blocked && !plan?.error && (
            <Text size="xs" c="orange">
              {blocked}
            </Text>
          )}
        </Group>
      )}
    </Stack>
  );
}
