import {
  Alert,
  Anchor,
  Button,
  Card,
  Checkbox,
  Code,
  Group,
  Image,
  NumberInput,
  SegmentedControl,
  SimpleGrid,
  Spoiler,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
  Title,
  Tooltip,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconFlask, IconRefresh, IconTags } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api, errorMessage, thumbUrl } from "../../api/client";
import { useAnalysisStats, useSubmitJob, useTaggerStatus } from "../../api/hooks";
import type { AnalysisOutcome, AnalysisSettings, AnalysisSettingsView } from "../../api/types";
import { formatMillis, notReady, PIPELINES, usesTagger, type PipelineId } from "../../lib/pipelines";
import { useSelection } from "../../stores/selection";
import { DanbooruTags } from "../DanbooruTags";
import { TaggerSettings } from "./TaggerSettings";

interface Props {
  view: AnalysisSettingsView;
  draft: AnalysisSettings;
  onChange: (patch: Partial<AnalysisSettings>) => void;
  apiKey: string | undefined;
  /** Whether the settings have unsaved changes. */
  dirty: boolean;
}

/** Re-applies tag options to results already stored (no model requests). */
function RetagButton({ pipeline, label, dirty, what }: { pipeline: "danbooru" | "category"; label: string; dirty: boolean; what?: string }) {
  const { data: stats } = useAnalysisStats();
  const submit = useSubmitJob();
  const analysed = stats?.find((s) => s.pipeline === pipeline)?.analysed ?? 0;
  if (!analysed) return null;
  return (
    <Group gap="xs">
      <Tooltip label="Save the settings first" disabled={!dirty}>
        <Button
          size="xs"
          variant="default"
          leftSection={<IconTags size={16} />}
          disabled={dirty}
          loading={submit.isPending}
          onClick={() =>
            submit.mutate(
              { path: "/api/analysis/retag", body: { pipeline } },
              {
                onSuccess: () => notifications.show({ message: "Updating tags in the background (see Jobs)" }),
                onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
              },
            )
          }
        >
          {label}
        </Button>
      </Tooltip>
      <Text size="xs" c="dimmed">
        For the {analysed.toLocaleString()} image(s) already analysed, using the saved {what ?? "options"}; no requests are sent.
      </Text>
    </Group>
  );
}

/** The built-in instructions for a pipeline under the draft settings. */
function defaultPrompt(view: AnalysisSettingsView, draft: AnalysisSettings, id: PipelineId): string {
  if (id !== "category") return view.defaults[id];
  const hasList = draft.category.categories.split("\n").some((l) => l.trim() && !l.trim().startsWith("#"));
  const two = draft.category.levels === 2;
  return view.defaults[hasList ? (two ? "categoryList2" : "categoryList") : two ? "categoryFree2" : "categoryFree"];
}

function promptOf(draft: AnalysisSettings, id: PipelineId): string {
  return draft[id].prompt;
}

function OutcomeView({ o }: { o: AnalysisOutcome }) {
  return (
    <Stack gap={6}>
      {o.error ? (
        <Alert color="red" variant="light" p="xs">
          <Text size="sm">{o.error}</Text>
        </Alert>
      ) : o.pipeline === "danbooru" ? (
        o.tags?.length || o.rating ? (
          <DanbooruTags tags={o.tags} characters={o.characters} rating={o.rating} ratingScore={o.ratingScore} scores={o.scores} />
        ) : (
          <Text size="sm" c="dimmed" fs="italic">
            No tags reached the minimum confidence
          </Text>
        )
      ) : o.pipeline === "ocr" && !o.text ? (
        <Text size="sm" c="dimmed" fs="italic">
          No text found
        </Text>
      ) : (
        <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
          {o.text}
        </Text>
      )}
      {o.tagNames && o.tagNames.length > 0 && (
        <Text size="xs" c="dimmed">
          Tags it would add: {o.tagNames.join(", ")}
        </Text>
      )}
      <Text size="xs" c="dimmed">
        {o.model} · {formatMillis(o.millis)}
        {o.promptTokens + o.completionTokens > 0 &&
          ` · ${o.promptTokens.toLocaleString()} + ${o.completionTokens.toLocaleString()} tokens`}
      </Text>
      {o.reply && o.reply !== o.text && (
        <Spoiler maxHeight={0} showLabel="Show the raw reply" hideLabel="Hide the raw reply">
          <Code block style={{ whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
            {o.reply}
          </Code>
        </Spoiler>
      )}
      {o.reasoning && (
        <Spoiler maxHeight={0} showLabel="Show the model's reasoning" hideLabel="Hide the reasoning">
          <Code block style={{ whiteSpace: "pre-wrap" }}>
            {o.reasoning}
          </Code>
        </Spoiler>
      )}
    </Stack>
  );
}

/** Runs one pipeline on one image with the draft settings, storing nothing. */
function TryPanel({ id, draft, apiKey }: { id: PipelineId; draft: AnalysisSettings; apiKey: string | undefined }) {
  const selected = useSelection((s) => s.selected);
  const { data: tagger } = useTaggerStatus();
  const blocked = notReady(draft, id, tagger);
  const [imageId, setImageId] = useState<number>(0);
  const tryIt = useMutation({
    mutationFn: (image: number) =>
      api.post<{ imageId: number; outcomes: AnalysisOutcome[] }>("/api/analysis/try", {
        settings: draft,
        apiKey,
        imageId: image,
        pipelines: [id],
      }),
    onSuccess: (r) => setImageId(r.imageId),
  });
  const first = selected.size > 0 ? [...selected][0] : 0;
  return (
    <Stack gap="xs">
      <Group gap="xs">
        <Button
          size="xs"
          variant="light"
          leftSection={<IconFlask size={16} />}
          onClick={() => tryIt.mutate(0)}
          loading={tryIt.isPending}
          disabled={!!blocked}
        >
          Try on a random image
        </Button>
        {imageId > 0 && (
          <Button size="xs" variant="subtle" leftSection={<IconRefresh size={16} />} onClick={() => tryIt.mutate(imageId)} disabled={tryIt.isPending}>
            Again on this image
          </Button>
        )}
        {first > 0 && first !== imageId && (
          <Button size="xs" variant="subtle" onClick={() => tryIt.mutate(first)} disabled={tryIt.isPending}>
            On the selected image
          </Button>
        )}
        <Text size="xs" c={blocked ? "orange" : "dimmed"}>
          {blocked ?? "Uses the settings on this page; nothing is stored."}
        </Text>
      </Group>
      {tryIt.isPending && usesTagger(draft, id) && draft.danbooru.tagger.local && tagger?.state !== "running" && (
        <Text size="xs" c="dimmed">
          Starting the local tagger; loading the model can take a minute…
        </Text>
      )}
      {tryIt.error && (
        <Text size="sm" c="red">
          {errorMessage(tryIt.error)}
        </Text>
      )}
      {tryIt.data && (
        <Group align="flex-start" wrap="nowrap" gap="md">
          <Image src={thumbUrl(tryIt.data.imageId)} w={120} h={120} fit="contain" radius="sm" alt="" style={{ flexShrink: 0 }} />
          <div style={{ flex: 1, minWidth: 0 }}>
            {tryIt.data.outcomes.map((o) => (
              <OutcomeView key={o.pipeline} o={o} />
            ))}
          </div>
        </Group>
      )}
    </Stack>
  );
}

function PromptField({ view, draft, id, onChange }: { view: AnalysisSettingsView; draft: AnalysisSettings; id: PipelineId; onChange: Props["onChange"] }) {
  const def = defaultPrompt(view, draft, id);
  const value = promptOf(draft, id);
  const set = (prompt: string) => onChange({ [id]: { ...draft[id], prompt } } as Partial<AnalysisSettings>);
  return (
    <Stack gap={4}>
      <Textarea
        label="Instructions"
        description={
          value.trim()
            ? "Your own instructions are used."
            : "The default instructions (shown faintly) are used; type to replace them."
        }
        placeholder={def}
        value={value}
        onChange={(e) => set(e.currentTarget.value)}
        autosize
        minRows={3}
      />
      <Group gap="md">
        {!value.trim() ? (
          <Anchor size="xs" component="button" type="button" onClick={() => set(def)}>
            Start from the default
          </Anchor>
        ) : (
          <Anchor size="xs" component="button" type="button" onClick={() => set("")}>
            Reset to the default
          </Anchor>
        )}
        {(id === "danbooru" || id === "category") && (
          <Text size="xs" c="dimmed">
            Placeholders: {id === "danbooru" ? <Code>{"{max}"}</Code> : <><Code>{"{categories}"}</Code> <Code>{"{existing}"}</Code></>}
          </Text>
        )}
      </Group>
    </Stack>
  );
}

function tagExample(prefix: string, spaces: boolean) {
  return `${prefix}${spaces ? "long hair" : "long_hair"}`;
}

/** Per-pipeline options, instructions and a try-out. */
export function PipelinesTab({ view, draft, onChange, apiKey, dirty }: Props) {
  const { data: tagger } = useTaggerStatus();
  const db = draft.danbooru;
  const cat = draft.category;
  const setDb = (p: Partial<AnalysisSettings["danbooru"]>) => onChange({ danbooru: { ...db, ...p } });
  const setCat = (p: Partial<AnalysisSettings["category"]>) => onChange({ category: { ...cat, ...p } });
  // Example tags, from the first listed category when there is a list.
  const [exMain, exSub] = (() => {
    const line = cat.categories
      .split("\n")
      .map((l) => l.trim().replace(/^[-*•]\s*/, ""))
      .find((l) => l && !l.startsWith("#"));
    if (!line) return ["Animals", "Dogs"];
    const [main, subs] = line.split(/:(.*)/s);
    const sub = subs?.split(/[,;|]/).map((s) => s.trim()).find(Boolean);
    return [main.trim(), sub ?? ""];
  })();
  const catPreview =
    cat.levels === 2 && exSub ? `${cat.prefix}${exMain}, ${cat.prefix}${exMain} / ${exSub}` : `${cat.prefix}${exMain}`;
  return (
    <Stack gap="md" maw={900}>
      {PIPELINES.map((p) => (
        <Card withBorder key={p.id}>
          <Stack gap="sm">
            <Group gap="xs">
              <p.icon size={20} />
              <Title order={5}>{p.label}</Title>
            </Group>
            <Text size="sm" c="dimmed">
              {p.description}
            </Text>
            {p.id === "danbooru" && (
              <Stack gap="xs">
                <Group gap="sm">
                  <Text size="sm" fw={500}>
                    Tags from
                  </Text>
                  <SegmentedControl
                    size="xs"
                    data={[
                      { value: "model", label: "The vision model" },
                      { value: "tagger", label: "A WD tagger" },
                    ]}
                    value={db.source}
                    onChange={(v) =>
                      // A tagger installed here is the natural choice when no server is set.
                      setDb({
                        source: v,
                        tagger: v === "tagger" && !db.tagger.endpoint && tagger?.installation.ready ? { ...db.tagger, local: true } : db.tagger,
                      })
                    }
                  />
                </Group>
                <Text size="xs" c="dimmed">
                  {db.source === "tagger"
                    ? "A WD tagger (SmilingWolf's WD v3 models) knows the real Danbooru tags, gives each a confidence and takes well under a second per image. The vision model is not used for these tags."
                    : "The vision model writes the tags, following the instructions below. A WD tagger is faster and knows the real Danbooru tags."}
                </Text>
                <Switch
                  label="Also add them as PhotoBag tags"
                  description="Re-running replaces the tags it added; tags you add yourself are never removed."
                  checked={db.addTags}
                  onChange={(e) => setDb({ addTags: e.currentTarget.checked })}
                />
                {db.source === "tagger" && <TaggerSettings draft={draft} onChange={onChange} />}
                <SimpleGrid cols={{ base: 1, sm: 3 }}>
                  {db.source === "model" && (
                    <TextInput
                      label="Tag name prefix"
                      description={`long_hair becomes “${tagExample(db.prefix, db.spaces)}”`}
                      placeholder="none, or e.g. db:"
                      value={db.prefix}
                      onChange={(e) => setDb({ prefix: e.currentTarget.value })}
                      disabled={!db.addTags}
                    />
                  )}
                  <NumberInput
                    label="At most"
                    description={db.source === "tagger" ? "General tags per image (most confident first)" : "Tags per image"}
                    min={1}
                    max={200}
                    value={db.maxTags}
                    onChange={(v) => setDb({ maxTags: Number(v) || 1 })}
                  />
                  <Checkbox
                    mt={30}
                    label="Spaces instead of underscores"
                    checked={db.spaces}
                    onChange={(e) => setDb({ spaces: e.currentTarget.checked })}
                    disabled={!db.addTags}
                  />
                </SimpleGrid>
                <RetagButton
                  pipeline="danbooru"
                  label={view.settings.danbooru.addTags ? "Update tags on analysed images" : "Remove their tags from analysed images"}
                  what={view.settings.danbooru.addTags ? "prefixes and, for tagger results, categories and confidences" : undefined}
                  dirty={dirty}
                />
              </Stack>
            )}
            {p.id === "category" && (
              <Stack gap="xs">
                <SegmentedControl
                  data={[
                    { value: "1", label: "One category" },
                    { value: "2", label: "Main and sub category" },
                  ]}
                  value={String(cat.levels)}
                  onChange={(v) => setCat({ levels: Number(v) })}
                  style={{ alignSelf: "flex-start" }}
                />
                <Textarea
                  label="Categories to choose from"
                  description={
                    cat.levels === 2
                      ? "One main category per line, with its subcategories after a colon. Leave empty to let the model name categories (it is shown the ones already in use)."
                      : "One per line. Leave empty to let the model name categories (it is shown the ones already in use)."
                  }
                  placeholder={
                    cat.levels === 2
                      ? "Animals: Dogs, Cats, Birds, Wildlife\nPeople: Portraits, Groups, Events\nPlaces: Cities, Landscapes, Interiors\nDocuments: Receipts, Screenshots, Notes"
                      : "Animals\nPeople\nPlaces\nFood\nDocuments"
                  }
                  value={cat.categories}
                  onChange={(e) => setCat({ categories: e.currentTarget.value })}
                  autosize
                  minRows={3}
                  maxRows={14}
                />
                <TextInput
                  label="Tag name prefix"
                  description={`Tags added: ${catPreview}`}
                  placeholder="none, or e.g. Category: "
                  value={cat.prefix}
                  onChange={(e) => setCat({ prefix: e.currentTarget.value })}
                  maw={360}
                />
                <RetagButton pipeline="category" label="Update category tags on analysed images" dirty={dirty} />
              </Stack>
            )}
            {!usesTagger(draft, p.id) && <PromptField view={view} draft={draft} id={p.id} onChange={onChange} />}
            <TryPanel id={p.id} draft={draft} apiKey={apiKey} />
          </Stack>
        </Card>
      ))}
    </Stack>
  );
}
