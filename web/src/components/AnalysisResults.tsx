import { ActionIcon, Badge, Button, Checkbox, Group, Menu, Spoiler, Stack, Text, Textarea, Tooltip } from "@mantine/core";
import { useLocalStorage } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconSparkles, IconX } from "@tabler/icons-react";
import { useState } from "react";
import { Link } from "react-router";
import { errorMessage } from "../api/client";
import { useAnalysisSettings, useAnalyzeImage, useDeleteAnalysis, useEditAnalysis } from "../api/hooks";
import type { Analysis } from "../api/types";
import { formatDate } from "../lib/format";
import { PIPELINES, pipelineInfo } from "../lib/pipelines";

const stop = (e: React.KeyboardEvent) => e.stopPropagation(); // keep lightbox shortcuts out of text fields

function TextResult({ id, a }: { id: number; a: Analysis }) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(a.text);
  const edit = useEditAnalysis();
  if (editing) {
    const save = () =>
      edit.mutate(
        { id, pipeline: a.pipeline, text: value },
        {
          onSuccess: () => setEditing(false),
          onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
        },
      );
    return (
      <Stack gap={4}>
        <Textarea
          value={value}
          onChange={(e) => setValue(e.currentTarget.value)}
          autosize
          minRows={2}
          maxRows={12}
          autoFocus
          onKeyDown={(e) => {
            stop(e);
            if (e.key === "Escape") setEditing(false);
            if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) save();
          }}
          aria-label={pipelineInfo(a.pipeline).heading}
        />
        <Group gap="xs">
          <Button size="compact-xs" onClick={save} loading={edit.isPending}>
            Save
          </Button>
          <Button size="compact-xs" variant="default" onClick={() => setEditing(false)}>
            Cancel
          </Button>
        </Group>
      </Stack>
    );
  }
  const empty = !a.text;
  return (
    <Group gap={4} wrap="nowrap" align="flex-start">
      <div style={{ flex: 1, minWidth: 0 }}>
        {empty ? (
          <Text size="sm" c="dimmed" fs="italic">
            {a.pipeline === "ocr" ? "No text found" : "Empty"}
          </Text>
        ) : (
          <Spoiler maxHeight={120} showLabel="More" hideLabel="Less">
            <Text size="sm" style={{ whiteSpace: a.pipeline === "ocr" ? "pre-wrap" : undefined, wordBreak: "break-word" }}>
              {a.text}
            </Text>
          </Spoiler>
        )}
      </div>
      <Tooltip label="Correct it">
        <ActionIcon
          variant="subtle"
          size="sm"
          onClick={() => {
            setValue(a.text);
            setEditing(true);
          }}
          aria-label="Edit"
        >
          <IconPencil size={14} />
        </ActionIcon>
      </Tooltip>
    </Group>
  );
}

function Result({ id, a }: { id: number; a: Analysis }) {
  const info = pipelineInfo(a.pipeline);
  const del = useDeleteAnalysis();
  return (
    <Stack gap={4}>
      <Group justify="space-between" gap={4} wrap="nowrap">
        <Group gap={6} wrap="nowrap">
          <info.icon size={15} />
          <Text size="sm" fw={500}>
            {info.heading}
          </Text>
        </Group>
        <Group gap={4} wrap="nowrap">
          <Tooltip label={`${a.edited ? "Edited by you" : a.model || "unknown model"} · ${formatDate(a.updatedAt)}`}>
            <Text size="xs" c="dimmed" truncate maw={150}>
              {a.edited ? "edited" : a.model}
            </Text>
          </Tooltip>
          <Tooltip label={a.pipeline === "category" || a.pipeline === "danbooru" ? "Remove this result and the tags it added" : "Remove this result"}>
            <ActionIcon
              variant="subtle"
              color="gray"
              size="sm"
              onClick={() => del.mutate({ id, pipeline: a.pipeline })}
              loading={del.isPending}
              aria-label={`Remove ${info.heading}`}
            >
              <IconX size={14} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>
      {a.pipeline === "danbooru" ? (
        <Group gap={4}>
          {(a.tags ?? []).map((t) => (
            <Badge
              key={t}
              component={Link}
              to={`/?text=${encodeURIComponent(`"${t}"`)}`}
              variant="light"
              color="gray"
              tt="none"
              style={{ cursor: "pointer" }}
            >
              {t}
            </Badge>
          ))}
        </Group>
      ) : a.pipeline === "category" ? (
        <Group gap={4}>
          <Badge component={Link} to={`/?text=${encodeURIComponent(`"${a.text}"`)}`} variant="light" tt="none" style={{ cursor: "pointer" }}>
            {a.text}
          </Badge>
        </Group>
      ) : (
        <TextResult key={`${a.updatedAt}`} id={id} a={a} />
      )}
    </Stack>
  );
}

/** An image's analysis results, with editing and "analyse now". */
export function AnalysisResults({ id, analyses }: { id: number; analyses: Analysis[] }) {
  const { data: view } = useAnalysisSettings();
  const analyze = useAnalyzeImage();
  const [chosen, setChosen] = useLocalStorage<string[]>({ key: "pb-analyse-one", defaultValue: ["caption", "ocr"] });
  const configured = !!view?.settings.endpoint;
  if (!configured && analyses.length === 0) return null;
  const run = () =>
    analyze.mutate(
      { id, pipelines: chosen },
      {
        onSuccess: (r) => {
          const failed = r.outcomes.filter((o) => o.error);
          if (failed.length)
            notifications.show({
              color: "red",
              title: "Some analyses failed",
              message: failed.map((o) => `${pipelineInfo(o.pipeline).heading}: ${o.error}`).join("\n"),
            });
        },
        onError: (e) => notifications.show({ color: "red", title: "Analysis failed", message: errorMessage(e) }),
      },
    );
  return (
    <Stack gap="sm">
      <Group justify="space-between">
        <Text size="sm" fw={600}>
          Analysis
        </Text>
        {configured && (
          <Menu position="bottom-end" closeOnItemClick={false} withArrow shadow="md">
            <Menu.Target>
              <Button size="compact-xs" variant="light" leftSection={<IconSparkles size={14} />} loading={analyze.isPending}>
                Analyse
              </Button>
            </Menu.Target>
            <Menu.Dropdown>
              <Stack gap={6} p={6}>
                {PIPELINES.map((p) => (
                  <Checkbox
                    key={p.id}
                    size="xs"
                    label={p.label}
                    checked={chosen.includes(p.id)}
                    onChange={(e) =>
                      setChosen((c) =>
                        e.currentTarget.checked ? PIPELINES.map((x) => x.id).filter((x) => x === p.id || c.includes(x)) : c.filter((x) => x !== p.id),
                      )
                    }
                  />
                ))}
                <Menu.Item
                  component="button"
                  onClick={run}
                  disabled={!chosen.length || analyze.isPending}
                  leftSection={<IconSparkles size={14} />}
                  color="teal"
                >
                  Run now
                  {analyses.some((a) => chosen.includes(a.pipeline) && a.edited)
                    ? " (replaces results, including your corrections)"
                    : analyses.some((a) => chosen.includes(a.pipeline))
                      ? " (replaces results)"
                      : ""}
                </Menu.Item>
              </Stack>
            </Menu.Dropdown>
          </Menu>
        )}
      </Group>
      {analyze.isPending && (
        <Text size="xs" c="dimmed">
          Asking the model… (this can take a while)
        </Text>
      )}
      {analyses.length === 0 && !analyze.isPending && (
        <Text size="xs" c="dimmed">
          Not analysed yet.
        </Text>
      )}
      {analyses.map((a) => (
        <Result key={a.pipeline} id={id} a={a} />
      ))}
    </Stack>
  );
}
