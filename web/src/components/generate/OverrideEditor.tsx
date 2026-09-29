import {
  ActionIcon,
  Button,
  Chip,
  Group,
  MultiSelect,
  NumberInput,
  Paper,
  SegmentedControl,
  Select,
  Stack,
  Switch,
  TagsInput,
  Text,
  Textarea,
  TextInput,
  Tooltip,
} from "@mantine/core";
import { IconPlus, IconX } from "@tabler/icons-react";
import type { InputSpec, Override, Sweep } from "../../api/types";
import { defaultSweep, formatValue, rangeValues, type ValueKind } from "../../lib/generate";

interface EditorProps {
  kind: ValueKind;
  spec?: InputSpec;
}

function decimalsOf(step: number | undefined): number {
  if (!step) return 3;
  const s = String(step);
  const i = s.indexOf(".");
  return i < 0 ? 0 : Math.min(6, s.length - i - 1);
}

function numberProps(kind: ValueKind, spec?: InputSpec) {
  const float = kind === "float";
  return {
    min: spec?.min !== undefined && spec.min > -1e15 ? spec.min : undefined,
    max: spec?.max !== undefined && spec.max < 1e15 ? spec.max : undefined,
    step: float ? (spec?.step ?? 0.1) : 1,
    decimalScale: float ? Math.max(2, decimalsOf(spec?.step)) : 0,
    allowDecimal: float,
  };
}

function options(spec?: InputSpec): string[] {
  return (spec?.options ?? []).map((o) => String(o));
}

/** Edits one value of an input. */
export function ValueInput({ kind, spec, value, onChange }: EditorProps & { value: unknown; onChange: (v: unknown) => void }) {
  switch (kind) {
    case "combo": {
      const opts = options(spec);
      const cur = value === undefined ? null : String(value);
      const data = cur !== null && !opts.includes(cur) ? [cur, ...opts] : opts;
      return (
        <Select
          size="xs"
          data={data}
          value={cur}
          onChange={(v) => v !== null && onChange(v)}
          searchable
          allowDeselect={false}
          nothingFoundMessage="Not offered by ComfyUI"
          error={cur !== null && opts.length > 0 && !opts.includes(cur) ? "ComfyUI does not offer this value" : undefined}
          comboboxProps={{ withinPortal: true }}
        />
      );
    }
    case "int":
    case "float":
      return (
        <NumberInput
          size="xs"
          {...numberProps(kind, spec)}
          value={typeof value === "number" ? value : Number(value) || 0}
          onChange={(v) => typeof v === "number" && onChange(v)}
        />
      );
    case "boolean":
      return <Switch checked={value === true} onChange={(e) => onChange(e.currentTarget.checked)} label={String(value === true)} />;
    case "multiline":
      return (
        <Textarea
          size="xs"
          autosize
          minRows={2}
          maxRows={10}
          value={typeof value === "string" ? value : String(value ?? "")}
          onChange={(e) => onChange(e.currentTarget.value)}
        />
      );
    default:
      return (
        <TextInput size="xs" value={typeof value === "string" ? value : String(value ?? "")} onChange={(e) => onChange(e.currentTarget.value)} />
      );
  }
}

/** Edits the values an input sweeps over. */
function SweepInput({ kind, spec, sweep, onChange }: EditorProps & { sweep: Sweep; onChange: (s: Sweep) => void }) {
  const values = sweep.values ?? [];
  if (kind === "int" || kind === "float") {
    const mode = sweep.range ? "range" : "list";
    const np = numberProps(kind, spec);
    const r = sweep.range ?? { from: 0, to: 0, step: np.step };
    const n = sweep.range ? rangeValues(sweep.range) : [];
    return (
      <Stack gap={6}>
        <SegmentedControl
          size="xs"
          value={mode}
          onChange={(m) =>
            onChange(
              m === "range"
                ? { range: { from: Number(values[0] ?? 0), to: Number(values[values.length - 1] ?? 0), step: np.step } }
                : { values: n.length ? n : [r.from] },
            )
          }
          data={[
            { value: "range", label: "From… to…" },
            { value: "list", label: "These values" },
          ]}
        />
        {mode === "range" ? (
          <>
            <Group gap="xs" grow>
              <NumberInput size="xs" label="From" {...np} value={r.from} onChange={(v) => typeof v === "number" && onChange({ range: { ...r, from: v } })} />
              <NumberInput size="xs" label="To" {...np} value={r.to} onChange={(v) => typeof v === "number" && onChange({ range: { ...r, to: v } })} />
              <NumberInput
                size="xs"
                label="Step"
                {...np}
                min={kind === "int" ? 1 : undefined}
                value={r.step}
                onChange={(v) => typeof v === "number" && onChange({ range: { ...r, step: v } })}
              />
            </Group>
            <Text size="xs" c={n.length ? "dimmed" : "red"}>
              {n.length
                ? `${n.length} value${n.length === 1 ? "" : "s"}: ${n.slice(0, 12).join(", ")}${n.length > 12 ? ", …" : ""}`
                : "Choose a step above 0 (at most 500 values)."}
            </Text>
          </>
        ) : (
          <TagsInput
            size="xs"
            value={values.map(String)}
            onChange={(vs) => onChange({ values: vs.map(Number).filter((x) => Number.isFinite(x)) })}
            placeholder="Type a value, then Enter"
            splitChars={[",", " "]}
          />
        )}
      </Stack>
    );
  }
  if (kind === "combo") {
    return (
      <MultiSelect
        size="xs"
        data={[...new Set([...values.map(String), ...options(spec)])]}
        value={values.map(String)}
        onChange={(vs) => onChange({ values: vs })}
        searchable
        placeholder={values.length ? "" : "Choose the values to compare"}
        comboboxProps={{ withinPortal: true }}
        hidePickedOptions
      />
    );
  }
  if (kind === "boolean") {
    return (
      <Chip.Group multiple value={values.map(String)} onChange={(vs) => onChange({ values: vs.map((v) => v === "true") })}>
        <Group gap="xs">
          <Chip size="xs" value="true">
            true
          </Chip>
          <Chip size="xs" value="false">
            false
          </Chip>
        </Group>
      </Chip.Group>
    );
  }
  // Text: one box per value (prompts may span lines and contain commas).
  const set = (i: number, v: string) => onChange({ values: values.map((x, j) => (j === i ? v : x)) });
  return (
    <Stack gap={4}>
      {values.map((v, i) => (
        <Group key={i} gap={4} align="flex-start" wrap="nowrap">
          <Textarea
            size="xs"
            autosize
            minRows={1}
            maxRows={6}
            value={String(v ?? "")}
            onChange={(e) => set(i, e.currentTarget.value)}
            style={{ flex: 1 }}
          />
          <ActionIcon size="sm" variant="subtle" color="gray" mt={4} onClick={() => onChange({ values: values.filter((_, j) => j !== i) })} aria-label="Remove value">
            <IconX size={14} />
          </ActionIcon>
        </Group>
      ))}
      <Button size="compact-xs" variant="subtle" leftSection={<IconPlus size={14} />} onClick={() => onChange({ values: [...values, ""] })} w="fit-content">
        Add a value
      </Button>
    </Stack>
  );
}

/** One override: which input, and its value or sweep. */
export function OverrideEditor({
  override: o,
  kind,
  spec,
  current,
  missing,
  onChange,
  onRemove,
}: EditorProps & {
  override: Override;
  current: unknown;
  /** Why the override does not fit the workflow, if it does not. */
  missing?: string;
  onChange: (o: Override) => void;
  onRemove: () => void;
}) {
  const sweeping = !!o.sweep;
  return (
    <Paper withBorder p="xs" radius="md" style={missing ? { borderColor: "var(--mantine-color-red-5)" } : undefined}>
      <Stack gap={6}>
        <Group justify="space-between" wrap="nowrap" gap="xs">
          <Tooltip label={spec?.tooltip} disabled={!spec?.tooltip} multiline w={300} openDelay={400}>
            <Text size="sm" fw={500} truncate>
              {o.node} <Text span c="dimmed">›</Text> {o.input}
            </Text>
          </Tooltip>
          <Group gap={4} wrap="nowrap">
            <SegmentedControl
              size="xs"
              value={sweeping ? "sweep" : "value"}
              onChange={(m) =>
                onChange(
                  m === "sweep"
                    ? { node: o.node, input: o.input, sweep: defaultSweep(kind, o.value ?? current, spec) }
                    : { node: o.node, input: o.input, value: o.sweep?.values?.[0] ?? o.sweep?.range?.from ?? current },
                )
              }
              data={[
                { value: "value", label: "Value" },
                { value: "sweep", label: "Sweep" },
              ]}
            />
            <ActionIcon variant="subtle" color="gray" onClick={onRemove} aria-label="Remove override">
              <IconX size={16} />
            </ActionIcon>
          </Group>
        </Group>
        {missing ? (
          <Text size="xs" c="red">
            {missing}
          </Text>
        ) : sweeping ? (
          <SweepInput kind={kind} spec={spec} sweep={o.sweep!} onChange={(s) => onChange({ node: o.node, input: o.input, sweep: s })} />
        ) : (
          <ValueInput kind={kind} spec={spec} value={o.value} onChange={(v) => onChange({ node: o.node, input: o.input, value: v })} />
        )}
        {!missing && (
          <Text size="xs" c="dimmed" truncate>
            In the workflow: {formatValue(current, 80)}
          </Text>
        )}
      </Stack>
    </Paper>
  );
}
