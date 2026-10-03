import {
  ActionIcon,
  Alert,
  Box,
  Button,
  Center,
  Group,
  Loader,
  ScrollArea,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { useDebouncedValue, useLocalStorage, useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconArrowLeft, IconFolderPlus, IconTrash, IconX } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { api, errorMessage } from "../api/client";
import { gk, useExperiment, useGenerationRuns, useGenerations, useWorkflows, useDiscardGenerations } from "../api/generate";
import { invalidateTopics } from "../api/hooks";
import type { ExperimentView, GenerateRequest, GenerationDetail, Job } from "../api/types";
import { Confirm } from "../components/Confirm";
import { GenerateForm } from "../components/generate/GenerateForm";
import { GenerationViewer } from "../components/generate/GenerationViewer";
import { MoveDialog } from "../components/generate/MoveDialog";
import { Results } from "../components/generate/Results";
import { plural } from "../lib/format";
import { requestFrom } from "../lib/generate";

function NameEditor({ exp }: { exp: ExperimentView }) {
  const [value, setValue] = useState(exp.name);
  const qc = useQueryClient();
  useEffect(() => setValue(exp.name), [exp.name]);
  const save = async () => {
    const v = value.trim();
    if (!v || v === exp.name) {
      setValue(exp.name);
      return;
    }
    try {
      await api.patch(`/api/experiments/${exp.id}`, { name: v });
      invalidateTopics(qc, ["experiments"]);
    } catch (e) {
      notifications.show({ color: "red", title: "Not renamed", message: errorMessage(e) });
      setValue(exp.name);
    }
  };
  return (
    <TextInput
      variant="unstyled"
      value={value}
      onChange={(e) => setValue(e.currentTarget.value)}
      onBlur={save}
      onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
      styles={{ input: { fontSize: "var(--mantine-h3-font-size)", fontWeight: 700, height: 36 } }}
      w={Math.min(600, Math.max(160, value.length * 14 + 30))}
      aria-label="Experiment name"
    />
  );
}

function Notes({ exp }: { exp: ExperimentView }) {
  const [value, setValue] = useState(exp.notes);
  useEffect(() => setValue(exp.notes), [exp.notes]);
  return (
    <Textarea
      label="Notes"
      placeholder="What this experiment is for, what worked…"
      autosize
      minRows={2}
      maxRows={8}
      value={value}
      onChange={(e) => setValue(e.currentTarget.value)}
      onBlur={() => value !== exp.notes && api.patch(`/api/experiments/${exp.id}`, { notes: value })}
    />
  );
}

/** One experiment: the generation form, and the images it holds. */
export function ExperimentPage() {
  const id = Number(useParams().id);
  const navigate = useNavigate();
  const [sp, setSp] = useSearchParams();
  const qc = useQueryClient();
  const { data: exp, error } = useExperiment(id);
  const { data: workflows } = useWorkflows();
  const [showMoved, setShowMoved] = useState(false);
  const { data: gens } = useGenerations(id, showMoved);
  const { data: runs } = useGenerationRuns(id);
  const [size, setSize] = useLocalStorage({ key: "photobag-generated-size", defaultValue: 160 });
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [open, setOpen] = useState<number | null>(null);
  const [moving, setMoving] = useState<number[] | null>(null);
  const [discarding, setDiscarding] = useState<number[] | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [startedJob, setStartedJob] = useState<Job>();
  const discard = useDiscardGenerations();
  const wide = useMediaQuery("(min-width: 62em)");

  // The form's settings: loaded from the experiment, saved back as they change.
  const [draft, setDraft] = useState<GenerateRequest | null>(null);
  const draftOf = useRef(0);
  useEffect(() => {
    if (!exp || draftOf.current === exp.id || !workflows) return;
    draftOf.current = exp.id;
    const r = exp.request;
    setDraft({
      ...r,
      overrides: r.overrides ?? [],
      count: r.count || 1,
      seed: r.seed || "random",
      workflowId: r.workflowId || (r.versionId ? 0 : (workflows[0]?.id ?? 0)),
    });
  }, [exp, workflows]);
  const [debounced] = useDebouncedValue(draft, 800);
  const saved = useRef("");
  useEffect(() => {
    saved.current = "";
  }, [id]);
  useEffect(() => {
    if (!debounced || draftOf.current !== id) return;
    const js = JSON.stringify(debounced);
    if (!saved.current) {
      saved.current = js; // the loaded settings
      return;
    }
    if (js === saved.current) return;
    saved.current = js;
    api.patch(`/api/experiments/${id}`, { request: debounced }).catch(() => undefined);
  }, [debounced, id]);

  // ?use=<image>[&seed=1]: pick up from an image (from the library's viewer).
  const use = Number(sp.get("use"));
  const withSeed = sp.get("seed") === "1";
  useEffect(() => {
    if (!use || !draft) return;
    api
      .get<GenerationDetail>(`/api/generations/${use}`)
      .then((g) => setDraft(requestFrom(g, withSeed, draft.count)))
      .catch((e) => notifications.show({ color: "red", title: "Could not load the image's settings", message: errorMessage(e) }));
    const next = new URLSearchParams(sp);
    next.delete("use");
    next.delete("seed");
    setSp(next, { replace: true });
  }, [use, withSeed, draft, sp, setSp]);

  // Viewer order: as shown, newest run first.
  const order = useMemo(() => {
    const byRun = new Map<number, number[]>();
    for (const g of gens ?? []) byRun.set(g.runId ?? 0, [...(byRun.get(g.runId ?? 0) ?? []), g.id]);
    return [...(runs ?? []).flatMap((r) => byRun.get(r.id) ?? []), ...(byRun.get(0) ?? [])];
  }, [gens, runs]);
  const shas = useMemo(() => new Map((gens ?? []).map((g) => [g.id, g.sha256])), [gens]);
  const heldIds = useMemo(() => new Set((gens ?? []).filter((g) => !g.imageId).map((g) => g.id)), [gens]);
  useEffect(() => {
    setSelected((s) => {
      const kept = new Set([...s].filter((x) => heldIds.has(x)));
      return kept.size === s.size ? s : kept;
    });
  }, [heldIds]);
  const chosen = [...selected].filter((x) => heldIds.has(x));

  if (error) {
    return (
      <Stack p="md">
        <Alert color="red">{errorMessage(error)}</Alert>
        <Button component={Link} to="/generate" variant="light" w="fit-content">
          Back to the experiments
        </Button>
      </Stack>
    );
  }
  if (!exp || !draft) {
    return (
      <Center p="xl">
        <Loader />
      </Center>
    );
  }
  const jobId = startedJob?.id ?? exp.job?.id;
  const loadRequest = (r: GenerateRequest) => {
    setDraft({ ...r, overrides: r.overrides ?? [], count: r.count || 1, seed: r.seed || "random" });
    notifications.show({ message: "Settings loaded into the form" });
  };

  const form = (
    <Stack gap="lg">
      <GenerateForm experiment={exp} draft={draft} onChange={setDraft} jobId={jobId} onStarted={setStartedJob} />
      <Notes exp={exp} />
    </Stack>
  );
  const bulk = chosen.length > 0 && (
    <Group gap="xs" wrap="nowrap">
      <Text size="sm" fw={500}>
        {plural(chosen.length, "image")} selected
      </Text>
      <Button size="compact-sm" leftSection={<IconFolderPlus size={16} />} onClick={() => setMoving(chosen)}>
        Move to the library…
      </Button>
      <Button size="compact-sm" variant="light" color="red" leftSection={<IconTrash size={16} />} onClick={() => setDiscarding(chosen)}>
        Delete
      </Button>
      <ActionIcon variant="subtle" color="gray" onClick={() => setSelected(new Set())} aria-label="Clear the selection">
        <IconX size={16} />
      </ActionIcon>
    </Group>
  );
  const results = (
    <Results
      runs={runs ?? []}
      generations={gens ?? []}
      size={size}
      onSize={setSize}
      showMoved={showMoved}
      onShowMoved={setShowMoved}
      selected={selected}
      onSelected={setSelected}
      onOpen={(g) => setOpen(order.indexOf(g))}
      onUse={loadRequest}
      selectionBar={bulk || undefined}
    />
  );
  return (
    <Stack gap={0} style={{ flex: 1, minHeight: 0 }}>
      <Group px="md" py={6} justify="space-between" wrap="nowrap" style={{ borderBottom: "1px solid var(--mantine-color-default-border)" }}>
        <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
          <ActionIcon component={Link} to="/generate" variant="subtle" color="gray" aria-label="All experiments">
            <IconArrowLeft size={18} />
          </ActionIcon>
          <NameEditor exp={exp} />
          <Text size="sm" c="dimmed" truncate>
            {plural(exp.held, "image")} here{exp.moved ? ` · ${exp.moved} moved to the library` : ""}
          </Text>
        </Group>
        <Button size="xs" variant="subtle" color="red" leftSection={<IconTrash size={16} />} onClick={() => setDeleting(true)}>
          Delete experiment
        </Button>
      </Group>
      {wide ? (
        <div style={{ display: "flex", flex: 1, minHeight: 0 }}>
          <ScrollArea w={460} style={{ flex: "none", borderRight: "1px solid var(--mantine-color-default-border)" }}>
            <Box p="md">{form}</Box>
          </ScrollArea>
          <ScrollArea style={{ flex: 1 }}>
            <Stack p="md" pt={4} gap="sm">
              {results}
            </Stack>
          </ScrollArea>
        </div>
      ) : (
        <ScrollArea style={{ flex: 1 }}>
          <Stack p="md" gap="lg">
            {form}
            {results}
          </Stack>
        </ScrollArea>
      )}
      <GenerationViewer ids={order} shas={shas} index={open} onIndex={setOpen} onClose={() => setOpen(null)} onUse={loadRequest} count={draft.count} />
      <MoveDialog ids={moving} onClose={() => setMoving(null)} onMoved={() => setSelected(new Set())} />
      <Confirm
        opened={!!discarding}
        title={`Delete ${plural(discarding?.length ?? 0, "image")}?`}
        confirm="Delete"
        loading={discard.isPending}
        onClose={() => setDiscarding(null)}
        onConfirm={() =>
          discarding &&
          discard.mutate(discarding, {
            onSuccess: () => {
              setDiscarding(null);
              setSelected(new Set());
            },
            onError: (e) => notifications.show({ color: "red", title: "Not deleted", message: errorMessage(e) }),
          })
        }
      >
        <Text size="sm">They are not in the library, so they are gone for good.</Text>
      </Confirm>
      <Confirm
        opened={deleting}
        title={`Delete the experiment “${exp.name}”?`}
        confirm="Delete experiment"
        onClose={() => setDeleting(false)}
        onConfirm={async () => {
          try {
            await api.del(`/api/experiments/${exp.id}`);
            invalidateTopics(qc, ["experiments", "generations"]);
            qc.removeQueries({ queryKey: gk.experiment(exp.id) });
            navigate("/generate");
          } catch (e) {
            notifications.show({ color: "red", title: "Not deleted", message: errorMessage(e) });
          }
          setDeleting(false);
        }}
      >
        <Text size="sm">
          {exp.held
            ? `Its ${plural(exp.held, "image")} still in the experiment are deleted. `
            : ""}
          Images moved to the library stay there.
        </Text>
      </Confirm>
    </Stack>
  );
}
