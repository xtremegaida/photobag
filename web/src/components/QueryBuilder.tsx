import { Group, MultiSelect, SegmentedControl, Stack, Text, TextInput } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useEffect, useMemo, useState } from "react";
import { useCount } from "../api/hooks";
import type { ImageQuery } from "../api/types";
import { plural } from "../lib/format";
import { useSelection } from "../stores/selection";
import { useTagNames } from "./TagEditor";

export type Source = "all" | "filter" | "selection";

interface Props {
  value: ImageQuery;
  onChange: (q: ImageQuery) => void;
  initialSource?: Source;
  /** Show the number of pairs (for scoring runs). */
  showPairs?: boolean;
  label?: string;
}

/**
 * Chooses a set of images: everything, a tag/name filter, or the current
 * gallery selection. Shows a live count.
 */
export function QueryBuilder({ value, onChange, initialSource = "all", showPairs, label = "Images" }: Props) {
  const names = useTagNames();
  const selected = useSelection((s) => s.selected);
  const [source, setSource] = useState<Source>(initialSource === "selection" && selected.size === 0 ? "all" : initialSource);
  const [all, setAll] = useState<string[]>(value.tagsAll ?? []);
  const [any, setAny] = useState<string[]>(value.tagsAny ?? []);
  const [none, setNone] = useState<string[]>(value.tagsNone ?? []);
  const [glob, setGlob] = useState(value.nameGlob ?? "");
  const [globD] = useDebouncedValue(glob, 300);

  const query = useMemo<ImageQuery>(() => {
    if (source === "selection") return { ids: [...selected] };
    if (source === "filter") {
      const q: ImageQuery = {};
      if (all.length) q.tagsAll = all;
      if (any.length) q.tagsAny = any;
      if (none.length) q.tagsNone = none;
      if (globD.trim()) q.nameGlob = globD.trim();
      return q;
    }
    return {};
  }, [source, selected, all, any, none, globD]);

  useEffect(() => onChange(query), [query]); // eslint-disable-line react-hooks/exhaustive-deps

  const { data: count, error } = useCount(query);
  const sources = [
    { value: "all", label: "All images" },
    { value: "filter", label: "Tags / name" },
    { value: "selection", label: `Selection (${selected.size})`, disabled: selected.size === 0 },
  ];
  return (
    <Stack gap="xs">
      <Text size="sm" fw={500}>
        {label}
      </Text>
      <SegmentedControl data={sources} value={source} onChange={(v) => setSource(v as Source)} />
      {source === "filter" && (
        <Stack gap="xs">
          <MultiSelect label="With all of these tags" data={names} value={all} onChange={setAll} searchable clearable selectFirstOptionOnChange />
          <MultiSelect label="With any of these tags" data={names} value={any} onChange={setAny} searchable clearable selectFirstOptionOnChange />
          <MultiSelect label="Without these tags" data={names} value={none} onChange={setNone} searchable clearable selectFirstOptionOnChange />
          <TextInput
            label="Name matches"
            description="Case-insensitive glob such as IMG_*.jpg; plain text matches anywhere in the name"
            value={glob}
            onChange={(e) => setGlob(e.currentTarget.value)}
          />
        </Stack>
      )}
      <Group gap="xs">
        {error ? (
          <Text size="sm" c="red">
            {String((error as Error).message)}
          </Text>
        ) : count ? (
          <Text size="sm" c="dimmed">
            {plural(count.count, "image")}
            {showPairs ? ` → ${count.pairs.toLocaleString()} comparisons for a full run` : ""}
          </Text>
        ) : null}
      </Group>
    </Stack>
  );
}
