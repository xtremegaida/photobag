import {
  Alert,
  Button,
  Checkbox,
  Group,
  Modal,
  NumberInput,
  Radio,
  SegmentedControl,
  SimpleGrid,
  Slider,
  Stack,
  Switch,
  Text,
} from "@mantine/core";
import { useLocalStorage } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconFlask } from "@tabler/icons-react";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { errorMessage } from "../../api/client";
import { useCount } from "../../api/hooks";
import { useCreateReencode, useTryReencode, type ReencodeImages } from "../../api/reencode";
import type { ImageQuery, ReencodeSettings } from "../../api/types";
import { formatBytes, plural } from "../../lib/format";
import { DEFAULT_REENCODE, defaultMode, describeReencode, isLossless, psnrLabel, sizeChange } from "../../lib/reencode";
import { QueryBuilder } from "../QueryBuilder";

const SIZE_PRESETS = [3840, 2560, 1920, 1280];

/** The settings of a re-encode. */
function SettingsForm({ value, onChange }: { value: ReencodeSettings; onChange: (s: ReencodeSettings) => void }) {
  const set = (patch: Partial<ReencodeSettings>) => onChange({ ...value, ...patch });
  const lossless = isLossless(value);
  const scaling = value.maxWidth > 0 || value.maxHeight > 0;
  return (
    <Stack gap="sm">
      <SimpleGrid cols={2} spacing="sm">
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            Compression
          </Text>
          <SegmentedControl
            data={[
              { value: "lossless", label: "Lossless" },
              { value: "lossy", label: "Lossy" },
            ]}
            value={lossless ? "lossless" : "lossy"}
            onChange={(v) =>
              v === "lossless"
                ? set({ lossless: true, format: value.format === "jpeg" ? "webp" : value.format })
                : set({ lossless: false, format: value.format === "png" ? "webp" : value.format })
            }
          />
        </Stack>
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            Format
          </Text>
          <SegmentedControl
            data={lossless ? ["webp", "png"].map((f) => ({ value: f, label: f === "png" ? "PNG" : "WebP" })) : ["webp", "jpeg"].map((f) => ({ value: f, label: f === "jpeg" ? "JPEG" : "WebP" }))}
            value={value.format}
            onChange={(f) => set({ format: f as ReencodeSettings["format"], effort: f === "png" ? 2 : value.format === "png" ? 4 : value.effort })}
          />
        </Stack>
      </SimpleGrid>
      <Text size="xs" c="dimmed">
        {lossless
          ? "Lossless keeps every pixel exactly; files shrink only where the old encoding was wasteful. Photos usually come out larger than as JPEG."
          : "Lossy throws away detail the eye is least likely to miss. Re-encoding a JPEG loses a little more each time."}
      </Text>
      {!lossless && (
        <div>
          <Group justify="space-between">
            <Text size="sm" fw={500}>
              Quality
            </Text>
            <Text size="sm" c="dimmed">
              {value.quality}
            </Text>
          </Group>
          <Slider
            min={1}
            max={100}
            value={value.quality}
            onChange={(q) => set({ quality: q })}
            marks={[{ value: 50 }, { value: 75 }, { value: 90 }]}
            label={null}
          />
        </div>
      )}
      {value.format === "webp" && (
        <div>
          <Group justify="space-between">
            <Text size="sm" fw={500}>
              Effort
            </Text>
            <Text size="xs" c="dimmed">
              {value.effort <= 1 ? "fastest" : value.effort >= 5 ? "smallest files, slowest" : "balanced"}
            </Text>
          </Group>
          <Slider min={0} max={6} step={1} value={value.effort} onChange={(e) => set({ effort: e })} marks={[0, 2, 4, 6].map((v) => ({ value: v, label: String(v) }))} label={null} mb="md" />
        </div>
      )}
      {value.format === "png" && (
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            Compression effort
          </Text>
          <SegmentedControl
            data={[
              { value: "0", label: "Fast" },
              { value: "1", label: "Default" },
              { value: "2", label: "Smallest" },
            ]}
            value={String(value.effort)}
            onChange={(v) => set({ effort: Number(v) })}
          />
        </Stack>
      )}
      {value.format === "jpeg" && (
        <Group gap="xl">
          <Switch label="Progressive" checked={value.progressive} onChange={(e) => set({ progressive: e.currentTarget.checked })} />
          <Switch
            label="Full-resolution colour (4:4:4)"
            description="Sharper coloured edges and text, larger files"
            checked={value.chroma444}
            onChange={(e) => set({ chroma444: e.currentTarget.checked })}
          />
        </Group>
      )}
      <Checkbox
        label="Scale larger images down"
        description="Keeps proportions; images already small enough are left at their size"
        checked={scaling}
        onChange={(e) => set(e.currentTarget.checked ? { maxWidth: 2560, maxHeight: 2560 } : { maxWidth: 0, maxHeight: 0 })}
      />
      {scaling && (
        <Group align="flex-end" gap="sm">
          <NumberInput
            label="Largest width"
            suffix=" px"
            min={0}
            max={100000}
            w={150}
            value={value.maxWidth || ""}
            placeholder="any"
            onChange={(v) => set({ maxWidth: Number(v) || 0 })}
          />
          <NumberInput
            label="Largest height"
            suffix=" px"
            min={0}
            max={100000}
            w={150}
            value={value.maxHeight || ""}
            placeholder="any"
            onChange={(v) => set({ maxHeight: Number(v) || 0 })}
          />
          <Group gap={4}>
            {SIZE_PRESETS.map((p) => (
              <Button key={p} size="compact-xs" variant="default" onClick={() => set({ maxWidth: p, maxHeight: p })}>
                {p}
              </Button>
            ))}
          </Group>
        </Group>
      )}
      <Switch
        label="Keep the camera metadata (EXIF, XMP)"
        description="The colour profile is always kept. Without EXIF the date taken still stays in PhotoBag."
        checked={value.keepMetadata}
        onChange={(e) => set({ keepMetadata: e.currentTarget.checked })}
      />
      <Switch
        label="Only when the new file is smaller"
        description="Otherwise the original stays as it is"
        checked={value.onlySmaller}
        onChange={(e) => set({ onlySmaller: e.currentTarget.checked })}
      />
    </Stack>
  );
}

/**
 * Re-encodes images: those given (a selection), or a set chosen here. The
 * results replace the originals straight away or wait for review.
 */
export function ReencodeDialog({
  opened,
  onClose,
  ids,
}: {
  opened: boolean;
  onClose: () => void;
  /** The images (a selection); without them the dialog asks which. */
  ids?: number[];
}) {
  const navigate = useNavigate();
  const [stored, setStored] = useLocalStorage<ReencodeSettings>({
    key: "pb-reencode",
    defaultValue: DEFAULT_REENCODE,
    getInitialValueInEffect: false,
  });
  const settings = { ...DEFAULT_REENCODE, ...stored };
  const [mode, setMode] = useState<"replace" | "review" | null>(null);
  const [query, setQuery] = useState<ImageQuery>({});
  const create = useCreateReencode();
  const trial = useTryReencode();
  const { data: count } = useCount(query, !ids && opened);
  const images: ReencodeImages = ids ? { ids } : { query };
  const n = ids ? ids.length : (count?.count ?? 0);
  const effectiveMode = mode ?? defaultMode(settings);

  useEffect(() => {
    if (opened) {
      setMode(null);
      trial.reset();
    }
  }, [opened]); // eslint-disable-line react-hooks/exhaustive-deps

  const change = (s: ReencodeSettings) => {
    setStored(s);
    trial.reset();
  };
  const start = () =>
    create.mutate(
      { ...images, settings, mode: effectiveMode },
      {
        onSuccess: (b) => {
          onClose();
          navigate(`/reencode/${b.id}`);
        },
        onError: (e) => notifications.show({ color: "red", title: "Not started", message: errorMessage(e) }),
      },
    );
  const est = trial.data;
  const q = est?.minPsnr !== undefined ? psnrLabel(est.minPsnr) : undefined;

  return (
    <Modal opened={opened} onClose={onClose} title={ids ? `Re-encode ${plural(ids.length, "image")}` : "Re-encode images"} size="lg">
      <Stack gap="md">
        {!ids && <QueryBuilder value={query} onChange={setQuery} initialSource="filter" label="Images to re-encode" />}
        <SettingsForm value={settings} onChange={change} />
        <Radio.Group label="Then" value={effectiveMode} onChange={(v) => setMode(v as "replace" | "review")}>
          <Stack gap="xs" mt={6}>
            <Radio
              value="review"
              label="Compare first, then choose"
              description="Each result waits beside its original; replace or keep them one by one or all at once"
            />
            <Radio value="replace" label="Replace the originals straight away" description="Each original is deleted as soon as its new file is made and checked" />
          </Stack>
        </Radio.Group>
        {effectiveMode === "replace" && !isLossless(settings) && (
          <Alert color="orange" icon={<IconAlertTriangle size={18} />}>
            Lossy {settings.maxWidth || settings.maxHeight ? "and scaled-down " : ""}results replace the originals without a
            review, and the originals cannot be brought back.
          </Alert>
        )}
        {effectiveMode === "replace" && isLossless(settings) && (
          <Text size="xs" c="dimmed">
            Lossless results are decoded and checked pixel for pixel before an original is deleted.
          </Text>
        )}
        {est && (
          <Alert color={est.tried ? "blue" : "red"} variant="light" title={est.tried ? `Tried on ${plural(est.tried, "image")}` : "Could not try"}>
            {est.tried > 0 && (
              <Text size="sm">
                {formatBytes(est.oldBytes)} → {formatBytes(est.newBytes)} ({sizeChange(est.oldBytes, est.newBytes)})
                {est.exact ? " · pixels identical" : q ? ` · worst ${est.minPsnr} dB PSNR (${q.label})` : ""} · {(est.millis / 1000).toFixed(1)} s
              </Text>
            )}
            {est.problems.map((p) => (
              <Text key={p} size="xs" c="red">
                {p}
              </Text>
            ))}
          </Alert>
        )}
        <Group justify="space-between">
          <Button
            variant="default"
            leftSection={<IconFlask size={16} />}
            onClick={() => trial.mutate({ ...images, settings })}
            loading={trial.isPending}
            disabled={!n}
          >
            Try on {n > 3 ? "3 of them" : n === 1 ? "it" : "them"}
          </Button>
          <Group gap="xs">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button onClick={start} loading={create.isPending} disabled={!n} color={effectiveMode === "replace" && !isLossless(settings) ? "orange" : undefined}>
              Re-encode {plural(n, "image")} as {describeReencode(settings)}
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  );
}
