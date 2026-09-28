import {
  ActionIcon,
  Alert,
  Anchor,
  Autocomplete,
  Button,
  Card,
  Code,
  Group,
  JsonInput,
  NumberInput,
  PasswordInput,
  SimpleGrid,
  Stack,
  Text,
  Textarea,
  TextInput,
  Tooltip,
} from "@mantine/core";
import { IconCheck, IconPlugConnected, IconRefresh, IconX } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useEffect } from "react";
import { api, errorMessage } from "../../api/client";
import type { AnalysisSettings, AnalysisSettingsView, CheckResult } from "../../api/types";

interface Props {
  view: AnalysisSettingsView;
  draft: AnalysisSettings;
  onChange: (patch: Partial<AnalysisSettings>) => void;
  /** Unsaved API key (undefined: keep the stored one, "": forget it). */
  apiKey: string | undefined;
  onApiKey: (k: string | undefined) => void;
}

const EXTRA_EXAMPLES = [
  {
    label: "Turn thinking off (Qwen and similar on llama.cpp, vLLM)",
    value: { chat_template_kwargs: { enable_thinking: false } },
  },
  { label: "Low reasoning effort (OpenAI reasoning models)", value: { reasoning_effort: "low" } },
];

function mergeExtra(current: string, add: Record<string, unknown>): string {
  let base: Record<string, unknown> = {};
  try {
    const v = current.trim() ? JSON.parse(current) : {};
    if (v && typeof v === "object" && !Array.isArray(v)) base = v;
  } catch {
    // replace invalid JSON
  }
  return JSON.stringify({ ...base, ...add }, null, 2);
}

/** Endpoint, key, model and request options. */
export function ConnectionTab({ view, draft, onChange, apiKey, onApiKey }: Props) {
  const body = { settings: draft, apiKey };
  const models = useMutation({
    mutationFn: () => api.post<{ models: string[] }>("/api/analysis/models", body),
  });
  const check = useMutation({ mutationFn: () => api.post<CheckResult>("/api/analysis/check", body) });
  const modelList = models.data?.models ?? check.data?.models ?? [];

  // Offer the model when the server has exactly one.
  useEffect(() => {
    if (!draft.model && modelList.length === 1) onChange({ model: modelList[0] });
  }, [modelList, draft.model]); // eslint-disable-line react-hooks/exhaustive-deps

  const key = view.key;
  const keyPlaceholder = key.fromEnv
    ? `Using $${key.env} from the environment`
    : key.set && apiKey === undefined
      ? `Saved (${key.hint}); type to replace`
      : "Not needed for most local servers";

  return (
    <Stack gap="md" maw={900}>
      <Alert variant="light" color="blue" icon={<IconPlugConnected size={18} />}>
        PhotoBag sends each image (scaled to {draft.imageSize}px) to this endpoint. With a server on this machine, such
        as llama.cpp, LM Studio or Ollama, nothing leaves the computer. The model must accept images.
      </Alert>
      <Card withBorder>
        <Stack>
          <TextInput
            label="API endpoint"
            description={
              <>
                Base URL of an OpenAI-compatible API, usually ending in <Code>/v1</Code>: <Code>http://127.0.0.1:1234/v1</Code>{" "}
                (LM Studio, llama.cpp), <Code>http://127.0.0.1:11434/v1</Code> (Ollama), <Code>https://api.openai.com/v1</Code>
              </>
            }
            placeholder="http://127.0.0.1:1234/v1"
            value={draft.endpoint}
            onChange={(e) => onChange({ endpoint: e.currentTarget.value })}
          />
          <Group align="flex-end" gap="xs" wrap="nowrap">
            <PasswordInput
              label="API key"
              description={
                <>
                  Kept on this computer in <Code>{key.store}</Code>, never in the bag or its backups. The{" "}
                  <Code>{key.env}</Code> environment variable overrides it.
                </>
              }
              placeholder={keyPlaceholder}
              value={apiKey ?? ""}
              onChange={(e) => onApiKey(e.currentTarget.value === "" ? undefined : e.currentTarget.value)}
              disabled={key.fromEnv}
              autoComplete="off"
              style={{ flex: 1 }}
            />
            {key.set && !key.fromEnv && (
              <Tooltip label={apiKey === "" ? "Keep the saved key" : "Forget the saved key (when you save)"}>
                <ActionIcon
                  variant={apiKey === "" ? "filled" : "default"}
                  color={apiKey === "" ? "red" : undefined}
                  size="lg"
                  onClick={() => onApiKey(apiKey === "" ? undefined : "")}
                  aria-label="Forget the saved key"
                >
                  <IconX size={16} />
                </ActionIcon>
              </Tooltip>
            )}
          </Group>
          <Group align="flex-end" gap="xs" wrap="nowrap">
            <Autocomplete
              label="Model"
              description="Must accept images (a vision or multimodal model). Servers that load one model accept any name."
              placeholder="e.g. gpt-4o-mini, qwen2.5vl:7b"
              data={modelList}
              value={draft.model}
              onChange={(v) => onChange({ model: v })}
              style={{ flex: 1 }}
              limit={50}
            />
            <Tooltip label="Load the endpoint's models">
              <ActionIcon
                variant="default"
                size="lg"
                onClick={() => models.mutate()}
                loading={models.isPending}
                disabled={!draft.endpoint}
                aria-label="Load models"
              >
                <IconRefresh size={16} />
              </ActionIcon>
            </Tooltip>
          </Group>
          {models.error && (
            <Text size="sm" c="red">
              {errorMessage(models.error)}
            </Text>
          )}
          <SimpleGrid cols={{ base: 2, sm: 3, md: 5 }}>
            <NumberInput
              label="Max tokens"
              description="Per reply, incl. thinking"
              min={16}
              max={131072}
              step={512}
              value={draft.maxTokens}
              onChange={(v) => onChange({ maxTokens: Number(v) || 0 })}
            />
            <NumberInput
              label="Temperature"
              description="Blank: server default"
              min={0}
              max={2}
              step={0.1}
              decimalScale={2}
              value={draft.temperature ?? ""}
              onChange={(v) => onChange({ temperature: v === "" ? undefined : Number(v) })}
            />
            <NumberInput
              label="Parallel requests"
              description="Match the server's slots"
              min={1}
              max={32}
              value={draft.concurrency}
              onChange={(v) => onChange({ concurrency: Number(v) || 1 })}
            />
            <NumberInput
              label="Timeout (s)"
              description="Per request"
              min={10}
              max={3600}
              step={30}
              value={draft.timeoutSeconds}
              onChange={(v) => onChange({ timeoutSeconds: Number(v) || 0 })}
            />
            <NumberInput
              label="Image size (px)"
              description="Long side sent"
              min={256}
              max={4096}
              step={128}
              value={draft.imageSize}
              onChange={(v) => onChange({ imageSize: Number(v) || 0 })}
            />
          </SimpleGrid>
          <JsonInput
            label="Extra request parameters"
            description="A JSON object added to every request, for options specific to your server or model."
            placeholder='{"chat_template_kwargs": {"enable_thinking": false}}'
            value={draft.extra}
            onChange={(v) => onChange({ extra: v })}
            validationError="Not valid JSON"
            formatOnBlur
            autosize
            minRows={2}
          />
          <Group gap="xs" mt={-8}>
            {EXTRA_EXAMPLES.map((ex) => (
              <Anchor key={ex.label} size="xs" component="button" type="button" onClick={() => onChange({ extra: mergeExtra(draft.extra, ex.value) })}>
                + {ex.label}
              </Anchor>
            ))}
          </Group>
          <Textarea
            label="System message"
            description="Sent before every request. Leave blank for the default shown."
            placeholder={view.defaults.system}
            value={draft.systemPrompt}
            onChange={(e) => onChange({ systemPrompt: e.currentTarget.value })}
            autosize
            minRows={2}
          />
          <Group>
            <Button
              leftSection={<IconPlugConnected size={18} />}
              onClick={() => check.mutate()}
              loading={check.isPending}
              disabled={!draft.endpoint}
              variant="light"
            >
              Check connection
            </Button>
            <Text size="xs" c="dimmed">
              Asks the model to read a number from a test image, using the values above (saved or not).
            </Text>
          </Group>
          {check.error && (
            <Alert color="red" variant="light" icon={<IconX size={18} />}>
              {errorMessage(check.error)}
            </Alert>
          )}
          {check.data && (
            <Alert
              color={check.data.ok ? "teal" : "orange"}
              variant="light"
              icon={check.data.ok ? <IconCheck size={18} /> : <IconX size={18} />}
              title={check.data.ok ? "Connected" : "Not working yet"}
            >
              <Stack gap={4}>
                <Text size="sm">{check.data.message}</Text>
                {check.data.model && (
                  <Text size="xs" c="dimmed">
                    Model: {check.data.model}
                  </Text>
                )}
                {check.data.models.length > 0 && (
                  <Text size="xs" c="dimmed">
                    {check.data.models.length} model(s) available.
                  </Text>
                )}
                {check.data.modelsError && (
                  <Text size="xs" c="dimmed">
                    Model list unavailable: {check.data.modelsError}
                  </Text>
                )}
              </Stack>
            </Alert>
          )}
        </Stack>
      </Card>
    </Stack>
  );
}
