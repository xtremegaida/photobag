import {
  ActionIcon,
  Checkbox,
  Group,
  MultiSelect,
  Popover,
  SegmentedControl,
  Select,
  Slider,
  Stack,
  Text,
  TextInput,
  Tooltip,
} from "@mantine/core";
import { useDebouncedCallback } from "@mantine/hooks";
import {
  IconAdjustmentsHorizontal,
  IconArrowsShuffle,
  IconFileText,
  IconSearch,
  IconSortAscending,
  IconSortDescending,
  IconX,
} from "@tabler/icons-react";
import { useEffect, useState } from "react";
import { useMetrics, useStats } from "../api/hooks";
import type { ImageQuery, Sort } from "../api/types";
import { IMAGE_FORMATS, MIN_SIZES, SORT_FIELDS } from "../lib/query-url";
import { useTagNames } from "./TagEditor";

// Mantine reserves 100px for the search field, which wraps pills early.
const compactField = { inputField: { minWidth: 40 } };

interface Props {
  query: ImageQuery;
  sort: Sort;
  onChange: (q: ImageQuery, s: Sort) => void;
  thumbSize: number;
  onThumbSize: (n: number) => void;
  fit: boolean;
  onFit: (b: boolean) => void;
  captions: boolean;
  onCaptions: (b: boolean) => void;
}

/** Filter (tags, name), sort and view controls for the gallery. */
export function GalleryToolbar({ query, sort, onChange, thumbSize, onThumbSize, fit, onFit, captions, onCaptions }: Props) {
  const names = useTagNames();
  const { data: metrics } = useMetrics();
  const [search, setSearch] = useState(query.nameGlob ?? "");
  useEffect(() => setSearch(query.nameGlob ?? ""), [query.nameGlob]);
  const pushSearch = useDebouncedCallback((v: string) => onChange({ ...query, nameGlob: v || undefined }, sort), 300);
  const { data: stats } = useStats();
  const [text, setText] = useState(query.text ?? "");
  useEffect(() => setText(query.text ?? ""), [query.text]);
  const pushText = useDebouncedCallback((v: string) => onChange({ ...query, text: v.trim() ? v : undefined }, sort), 350);
  const showText = !!stats?.analysed || !!query.text;

  const tagMode = query.tagsAny?.length ? "any" : "all";
  const included = tagMode === "any" ? (query.tagsAny ?? []) : (query.tagsAll ?? []);
  const setIncluded = (tags: string[], mode = tagMode) =>
    onChange(
      {
        ...query,
        tagsAll: mode === "all" && tags.length ? tags : undefined,
        tagsAny: mode === "any" && tags.length ? tags : undefined,
      },
      sort,
    );

  const sortData = SORT_FIELDS.filter((f) => f.value !== "score" || (metrics?.length ?? 0) > 0).map((f) => ({
    value: f.value,
    label: f.label,
  }));

  return (
    <Group gap="sm" px="md" py="xs" wrap="wrap" align="flex-end">
      <TextInput
        placeholder="Name or glob (e.g. IMG_*.jpg)"
        leftSection={<IconSearch size={16} />}
        value={search}
        onChange={(e) => {
          setSearch(e.currentTarget.value);
          pushSearch(e.currentTarget.value);
        }}
        rightSection={
          search ? (
            <ActionIcon
              variant="subtle"
              color="gray"
              size="sm"
              onClick={() => {
                setSearch("");
                onChange({ ...query, nameGlob: undefined }, sort);
              }}
            >
              <IconX size={14} />
            </ActionIcon>
          ) : null
        }
        w={230}
      />
      {showText && (
        <Tooltip label="Searches captions, text in images, Danbooru tags and categories. Use quotes for phrases." openDelay={600}>
          <TextInput
            placeholder="Search descriptions & text"
            leftSection={<IconFileText size={16} />}
            value={text}
            onChange={(e) => {
              setText(e.currentTarget.value);
              pushText(e.currentTarget.value);
            }}
            rightSection={
              text ? (
                <ActionIcon
                  variant="subtle"
                  color="gray"
                  size="sm"
                  onClick={() => {
                    setText("");
                    onChange({ ...query, text: undefined }, sort);
                  }}
                  aria-label="Clear the description search"
                >
                  <IconX size={14} />
                </ActionIcon>
              ) : null
            }
            w={230}
            aria-label="Search descriptions and text"
          />
        </Tooltip>
      )}
      <Group gap={4} align="flex-end" wrap="nowrap">
        <MultiSelect
          placeholder={included.length ? "" : "Filter by tags"}
          data={names}
          value={included}
          onChange={(v) => setIncluded(v)}
          searchable
          selectFirstOptionOnChange
          clearable
          w={260}
          maxDropdownHeight={320}
          styles={compactField}
          nothingFoundMessage="No such tag"
        />
        <Tooltip label={tagMode === "all" ? "Images must have all selected tags" : "Images need any selected tag"}>
          <SegmentedControl
            size="xs"
            data={[
              { value: "all", label: "All" },
              { value: "any", label: "Any" },
            ]}
            value={tagMode}
            onChange={(m) => setIncluded(included, m)}
          />
        </Tooltip>
      </Group>
      <MultiSelect
        placeholder={query.tagsNone?.length ? "" : "Exclude tags"}
        data={names}
        value={query.tagsNone ?? []}
        onChange={(v) => onChange({ ...query, tagsNone: v.length ? v : undefined }, sort)}
        searchable
          selectFirstOptionOnChange
        clearable
        w={200}
        styles={compactField}
      />
      <Checkbox
        label="Untagged"
        checked={!!query.untagged}
        onChange={(e) => onChange({ ...query, untagged: e.currentTarget.checked || undefined }, sort)}
        mb={8}
      />
      <MultiSelect
        placeholder={query.formats?.length ? "" : "File types"}
        data={IMAGE_FORMATS}
        value={query.formats ?? []}
        onChange={(v) => onChange({ ...query, formats: v.length ? v : undefined }, sort)}
        clearable
        w={200}
        styles={compactField}
        aria-label="File types"
      />
      <Select
        placeholder="Any size"
        data={MIN_SIZES.map((mb) => ({ value: String(mb * (1 << 20)), label: `≥ ${mb} MB` }))}
        value={query.minSize ? String(query.minSize) : null}
        onChange={(v) => onChange({ ...query, minSize: v ? Number(v) : undefined }, sort)}
        clearable
        w={135}
        aria-label="Smallest file size"
      />
      <Group gap={4} wrap="nowrap" ml="auto" align="flex-end">
        <Select
          data={sortData}
          value={sort.field || "imported"}
          onChange={(f) => {
            if (!f) return;
            const next: Sort = { field: f, desc: f === "imported" };
            if (f === "score") next.metricId = sort.metricId ?? metrics?.[0]?.id;
            if (f === "random") next.seed = Math.floor(Math.random() * 1e9);
            if (f === "similar" && sort.similarTo) next.similarTo = sort.similarTo;
            onChange(query, next);
          }}
          w={170}
          allowDeselect={false}
        />
        {sort.field === "score" && (
          <Select
            data={(metrics ?? []).map((m) => ({ value: String(m.id), label: m.name }))}
            value={sort.metricId ? String(sort.metricId) : null}
            onChange={(v) => v && onChange(query, { ...sort, metricId: Number(v) })}
            w={150}
            allowDeselect={false}
          />
        )}
        {sort.field === "random" ? (
          <Tooltip label="Reshuffle">
            <ActionIcon
              variant="default"
              size="lg"
              onClick={() => onChange(query, { ...sort, seed: Math.floor(Math.random() * 1e9) })}
            >
              <IconArrowsShuffle size={18} />
            </ActionIcon>
          </Tooltip>
        ) : (
          <Tooltip label={sort.desc ? "Descending" : "Ascending"}>
            <ActionIcon variant="default" size="lg" onClick={() => onChange(query, { ...sort, desc: !sort.desc })}>
              {sort.desc ? <IconSortDescending size={18} /> : <IconSortAscending size={18} />}
            </ActionIcon>
          </Tooltip>
        )}
        <Popover position="bottom-end" shadow="md" withArrow>
          <Popover.Target>
            <ActionIcon variant="default" size="lg" aria-label="View options">
              <IconAdjustmentsHorizontal size={18} />
            </ActionIcon>
          </Popover.Target>
          <Popover.Dropdown>
            <Stack gap="sm" w={220}>
              <div>
                <Text size="sm" mb={4}>
                  Thumbnail size
                </Text>
                <Slider min={90} max={400} step={10} value={thumbSize} onChange={onThumbSize} label={null} />
              </div>
              <Checkbox label="Show whole image (no crop)" checked={fit} onChange={(e) => onFit(e.currentTarget.checked)} />
              <Checkbox label="Always show names" checked={captions} onChange={(e) => onCaptions(e.currentTarget.checked)} />
            </Stack>
          </Popover.Dropdown>
        </Popover>
      </Group>
    </Group>
  );
}
