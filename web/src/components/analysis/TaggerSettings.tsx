import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Code,
  Group,
  Loader,
  NumberInput,
  Radio,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { IconCpu, IconPlayerStop, IconPlugConnected, IconRefresh } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, errorMessage } from "../../api/client";
import { qk, useTaggerStatus } from "../../api/hooks";
import type { AnalysisSettings, TagFilter, TaggerCheck, TaggerOptions, TaggerStatus } from "../../api/types";
import { formatBytes } from "../../lib/format";

interface Props {
  draft: AnalysisSettings;
  onChange: (patch: Partial<AnalysisSettings>) => void;
}

const INSTALL = "photobag tagger install --download-model";

/** The tagger installed on this computer: whether it is ready, and running. */
function LocalTagger({ status }: { status: TaggerStatus | undefined }) {
  const qc = useQueryClient();
  const stop = useMutation({
    mutationFn: () => api.post<TaggerStatus>("/api/analysis/tagger/stop"),
    onSuccess: (s) => qc.setQueryData(qk.taggerStatus, s),
  });
  if (!status) return <Loader size="sm" />;
  const inst = status.installation;
  const refresh = (
    <Button size="compact-xs" variant="subtle" leftSection={<IconRefresh size={14} />} onClick={() => qc.invalidateQueries({ queryKey: qk.taggerStatus })}>
      Look again
    </Button>
  );
  if (!inst.ready) {
    return (
      <Alert color="orange" variant="light" title={inst.installed ? "The model is missing" : "Not installed on this computer"}>
        <Stack gap={6}>
          <Text size="sm">
            {inst.installed
              ? inst.problem
              : "In a terminal, run the command below. It finds Python, sets up ONNX Runtime (for an NVIDIA GPU when there is one, otherwise the CPU) and downloads the model, about 1.3 GB."}
          </Text>
          {!inst.installed && <Code block>{INSTALL}</Code>}
          <Group gap="xs">
            <Text size="xs" c="dimmed">
              Installation folder: {inst.dir}
            </Text>
            {refresh}
          </Group>
        </Stack>
      </Alert>
    );
  }
  return (
    <Stack gap={6}>
      <Group gap="xs">
        <Badge variant="light" color="teal" tt="none">
          {inst.model?.split("/").pop() ?? "WD tagger"}
        </Badge>
        <Badge variant="light" color="gray" tt="none" leftSection={<IconCpu size={12} />}>
          {status.state === "running" ? (status.onGpu ? "running on the GPU" : "running on the CPU") : inst.device === "cuda" ? "for the GPU" : "for the CPU"}
        </Badge>
        <Text size="xs" c="dimmed">
          Python {inst.python} · {formatBytes(inst.modelBytes)} · {inst.dir}
        </Text>
      </Group>
      <Group gap="xs">
        {status.state === "starting" && (
          <>
            <Loader size="xs" />
            <Text size="sm">Loading the model…</Text>
          </>
        )}
        {status.state === "running" && (
          <>
            <Text size="sm">
              Running; it stops after {status.idleMinutes} minutes without work{status.onGpu ? ", freeing the GPU memory" : ""}.
            </Text>
            <Button size="compact-xs" variant="default" leftSection={<IconPlayerStop size={14} />} onClick={() => stop.mutate()} loading={stop.isPending}>
              Stop now
            </Button>
          </>
        )}
        {status.state === "stopped" && (
          <Text size="sm" c="dimmed">
            Not running: PhotoBag starts it when it is needed (loading the model takes a moment).
          </Text>
        )}
      </Group>
      {status.state === "running" && inst.device === "cuda" && !status.onGpu && (
        <Alert color="orange" variant="light" p="xs">
          <Text size="sm">
            It was installed for the GPU but runs on the CPU, which is much slower. The reason is in tagger.log in the installation
            folder; updating the NVIDIA driver often helps.
          </Text>
        </Alert>
      )}
      {status.lastError && status.state !== "running" && (
        <Alert color="red" variant="light" p="xs" title="The tagger did not start">
          <Code block style={{ whiteSpace: "pre-wrap" }}>
            {status.lastError}
          </Code>
        </Alert>
      )}
    </Stack>
  );
}

function CheckResultView({ r }: { r: TaggerCheck }) {
  return (
    <Alert color={r.ok ? "teal" : "red"} variant="light" p="xs">
      <Stack gap={4}>
        <Text size="sm">{r.message}</Text>
        {r.ok && (
          <Text size="xs" c="dimmed">
            {r.startMillis ? `Started in ${(r.startMillis / 1000).toFixed(1)} s; ` : ""}a test image took {r.millis} ms
            {r.tags.length ? `: ${r.tags.join(", ")}` : ""}.
          </Text>
        )}
      </Stack>
    </Alert>
  );
}

interface Row {
  key: "general" | "character" | "rating";
  label: string;
  hint: string;
  min: number;
  prefix: string;
  setPrefix: (v: string) => void;
}

/** Where Danbooru tags come from when the WD tagger is used, and which to keep. */
export function TaggerSettings({ draft, onChange }: Props) {
  const { data: status } = useTaggerStatus();
  const db = draft.danbooru;
  const tg = db.tagger;
  const setTg = (p: Partial<TaggerOptions>) => onChange({ danbooru: { ...db, tagger: { ...tg, ...p } } });
  const setFilter = (key: Row["key"], p: Partial<TagFilter>) => setTg({ [key]: { ...tg[key], ...p } });
  const check = useMutation({ mutationFn: () => api.post<TaggerCheck>("/api/analysis/tagger/check", { settings: draft }) });
  const rows: Row[] = [
    {
      key: "general",
      label: "General",
      hint: "What is in the image: 1girl, outdoors, smile…",
      min: 0.01,
      prefix: db.prefix,
      setPrefix: (v) => onChange({ danbooru: { ...db, prefix: v } }),
    },
    {
      key: "character",
      label: "Characters",
      hint: "Named characters, such as hatsune_miku",
      min: 0.01,
      prefix: tg.characterPrefix,
      setPrefix: (v) => setTg({ characterPrefix: v }),
    },
    {
      key: "rating",
      label: "Rating",
      hint: "The most likely of general, sensitive, questionable and explicit",
      min: 0,
      prefix: tg.ratingPrefix,
      setPrefix: (v) => setTg({ ratingPrefix: v }),
    },
  ];
  const example = (r: Row) => {
    const tag = r.key === "general" ? "long_hair" : r.key === "character" ? "hatsune_miku" : "general";
    return r.prefix + (db.spaces && r.key !== "rating" ? tag.replaceAll("_", " ") : tag);
  };
  return (
    <Stack gap="sm">
      <Radio.Group
        label="Tagger"
        value={tg.local ? "local" : "server"}
        onChange={(v) => {
          check.reset();
          setTg({ local: v === "local" });
        }}
      >
        <Group mt={4}>
          <Radio value="local" label={status && !status.installation.ready ? "This computer (not installed yet)" : "This computer"} />
          <Radio value="server" label="A tagger server" />
        </Group>
      </Radio.Group>
      {tg.local ? (
        <LocalTagger status={status} />
      ) : (
        <TextInput
          label="Address"
          description={
            <>
              host:port of a computer running the tagger server (<Code>photobag tagger serve --host 0.0.0.0</Code>, or the script
              from <Code>photobag tagger script</Code>)
            </>
          }
          placeholder="192.168.1.20:8000"
          value={tg.endpoint}
          onChange={(e) => {
            check.reset();
            setTg({ endpoint: e.currentTarget.value });
          }}
          maw={520}
        />
      )}
      <Group gap="xs" align="flex-start">
        <Button
          size="xs"
          variant="light"
          leftSection={<IconPlugConnected size={16} />}
          onClick={() => check.mutate()}
          loading={check.isPending}
          disabled={tg.local ? !status?.installation.ready : !tg.endpoint.trim()}
        >
          Check the tagger
        </Button>
        {check.isPending && tg.local && status?.state !== "running" && (
          <Text size="xs" c="dimmed" mt={6}>
            Starting it; loading the model can take a minute…
          </Text>
        )}
      </Group>
      {check.error && (
        <Text size="sm" c="red">
          {errorMessage(check.error)}
        </Text>
      )}
      {check.data && <CheckResultView r={check.data} />}

      <Table verticalSpacing={6} withRowBorders={false} fz="sm" maw={820} styles={{ td: { verticalAlign: "top" } }}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Keep</Table.Th>
            <Table.Th w={150}>Minimum confidence</Table.Th>
            <Table.Th w={260}>Tag name prefix</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((r) => (
            <Table.Tr key={r.key}>
              <Table.Td>
                <Checkbox
                  label={r.label}
                  description={r.hint}
                  checked={tg[r.key].include}
                  onChange={(e) => setFilter(r.key, { include: e.currentTarget.checked })}
                />
              </Table.Td>
              <Table.Td>
                <NumberInput
                  size="xs"
                  min={r.min}
                  max={1}
                  step={0.05}
                  decimalScale={2}
                  value={tg[r.key].threshold}
                  onChange={(v) => setFilter(r.key, { threshold: typeof v === "number" ? v : r.min })}
                  disabled={!tg[r.key].include}
                  aria-label={`Minimum confidence for ${r.label.toLowerCase()} tags`}
                />
              </Table.Td>
              <Table.Td>
                <TextInput
                  size="xs"
                  placeholder={r.key === "rating" ? "e.g. rating:" : "none"}
                  value={r.prefix}
                  onChange={(e) => r.setPrefix(e.currentTarget.value)}
                  disabled={!db.addTags || !tg[r.key].include}
                  aria-label={`Tag name prefix for ${r.label.toLowerCase()} tags`}
                  description={db.addTags && tg[r.key].include ? `Tags such as “${example(r)}”` : undefined}
                  inputWrapperOrder={["input", "description"]}
                />
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      <Text size="xs" c="dimmed">
        Confidence is the tagger's certainty, 0 to 1: lower values give more tags and more mistakes. The defaults (0.35 for
        general tags, 0.85 for characters) are the model author's.
      </Text>
    </Stack>
  );
}
