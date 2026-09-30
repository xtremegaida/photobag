import {
  ActionIcon,
  Alert,
  Anchor,
  Button,
  Center,
  Drawer,
  EmptyState,
  Group,
  Loader,
  Menu,
  Modal,
  ScrollArea,
  Select,
  Slider,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
  Tooltip,
} from "@mantine/core";
import { useDebouncedValue, useHotkeys, useLocalStorage, useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import {
  IconArrowBarToLeft,
  IconArrowBarToRight,
  IconArrowLeft,
  IconArrowsSort,
  IconDots,
  IconPhotoPlus,
  IconPlayerPlay,
  IconPresentation,
  IconSettings,
  IconTrash,
  IconX,
} from "@tabler/icons-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { errorMessage } from "../api/client";
import {
  useAddToDeck,
  useDeck,
  useDeleteDeck,
  useMoveInDeck,
  useRemoveFromDeck,
  useSortDeck,
  useUpdateDeck,
} from "../api/decks";
import { useCount, useMetrics } from "../api/hooks";
import type { DeckDetail, ImageQuery, Sort } from "../api/types";
import { Confirm } from "../components/Confirm";
import { Lightbox } from "../components/Lightbox";
import { QueryBuilder } from "../components/QueryBuilder";
import { DeckGrid } from "../components/slideshow/DeckGrid";
import { SettingsForm } from "../components/slideshow/SettingsForm";
import { useStartSlideshow } from "../components/slideshow/start";
import { plural } from "../lib/format";
import { SORT_FIELDS } from "../lib/query-url";
import { rangeBetween } from "../lib/selection";
import { sameSettings, validSettings } from "../lib/slideshow";

function NameEditor({ deck }: { deck: DeckDetail }) {
  const [value, setValue] = useState(deck.name);
  const update = useUpdateDeck(deck.id);
  useEffect(() => setValue(deck.name), [deck.name]);
  const save = () => {
    const v = value.trim();
    if (!v || v === deck.name) {
      setValue(deck.name);
      return;
    }
    update.mutate(
      { name: v },
      {
        onError: (e) => {
          notifications.show({ color: "red", title: "Not renamed", message: errorMessage(e) });
          setValue(deck.name);
        },
      },
    );
  };
  return (
    <TextInput
      variant="unstyled"
      value={value}
      onChange={(e) => setValue(e.currentTarget.value)}
      onBlur={save}
      onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
      styles={{ input: { fontSize: "var(--mantine-h3-font-size)", fontWeight: 700, height: 36 } }}
      w={Math.min(520, Math.max(140, value.length * 14 + 30))}
      aria-label="Deck name"
    />
  );
}

/** The deck's slideshow settings, saved as they change. */
function DeckSettings({ deck }: { deck: DeckDetail }) {
  const update = useUpdateDeck(deck.id);
  const [draft, setDraft] = useState(deck.settings);
  const [debounced] = useDebouncedValue(draft, 400);
  const saved = useRef(deck.settings);
  useEffect(() => {
    saved.current = deck.settings;
  }, [deck.settings]);
  useEffect(() => {
    if (!validSettings(debounced) || sameSettings(debounced, saved.current)) return;
    update.mutate(
      { settings: debounced },
      { onError: (e) => notifications.show({ color: "red", title: "Settings not saved", message: errorMessage(e) }) },
    );
  }, [debounced]); // eslint-disable-line react-hooks/exhaustive-deps
  return <SettingsForm value={draft} onChange={setDraft} previewId={deck.ids[0]} />;
}

function Notes({ deck }: { deck: DeckDetail }) {
  const [value, setValue] = useState(deck.notes);
  const update = useUpdateDeck(deck.id);
  useEffect(() => setValue(deck.notes), [deck.notes]);
  return (
    <Textarea
      label="Notes"
      placeholder="What the deck is for, where it was shown…"
      autosize
      minRows={2}
      maxRows={8}
      value={value}
      onChange={(e) => setValue(e.currentTarget.value)}
      onBlur={() => value !== deck.notes && update.mutate({ notes: value })}
    />
  );
}

/** Adds the images matching a query (or the library selection). */
function AddImages({ deck, opened, onClose }: { deck: DeckDetail; opened: boolean; onClose: () => void }) {
  const [query, setQuery] = useState<ImageQuery>({});
  const [field, setField] = useState("taken");
  const add = useAddToDeck();
  const { data: count } = useCount(query, opened);
  const sort: Sort = { field, desc: false };
  if (field === "random") sort.seed = Math.floor(Math.random() * 1e9);
  const submit = () =>
    add.mutate(
      { id: deck.id, query, sort },
      {
        onSuccess: (r) => {
          notifications.show({
            message: `Added ${plural(r.added, "image")}${r.present ? ` (${r.present} ${r.present === 1 ? "was" : "were"} in the deck already)` : ""}`,
          });
          onClose();
        },
        onError: (e) => notifications.show({ color: "red", title: "Not added", message: errorMessage(e) }),
      },
    );
  return (
    <Modal opened={opened} onClose={onClose} title={`Add images to ${deck.name}`} size="lg">
      {opened && (
        <Stack>
          <QueryBuilder value={{}} onChange={setQuery} initialSource="filter" />
          <Select
            label="Added in the order of"
            data={SORT_FIELDS.filter((f) => f.value !== "score" && f.value !== "similar").map((f) => ({
              value: f.value,
              label: f.value === "taken" ? "Date taken, oldest first" : f.label,
            }))}
            value={field}
            onChange={(v) => v && setField(v)}
            allowDeselect={false}
            comboboxProps={{ withinPortal: true }}
          />
          <Text size="xs" c="dimmed">
            New images go after the ones in the deck; images in it already stay where they are. To pick images one by one,
            select them in the{" "}
            <Anchor component={Link} to="/" size="xs">
              library
            </Anchor>{" "}
            and use “Add to deck”.
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button onClick={submit} loading={add.isPending} disabled={!count?.count}>
              Add {count ? plural(count.count, "image") : "images"}
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
}

/** Reorders the whole deck, with an undo. */
function SortMenu({ deck }: { deck: DeckDetail }) {
  const sort = useSortDeck(deck.id);
  const { data: metrics } = useMetrics();
  const run = (label: string, v: { sort: Sort } | { ids: number[] }) => {
    const before = deck.ids;
    sort.mutate(v, {
      onSuccess: () => {
        const id = notifications.show({
          message: (
            <Group justify="space-between" wrap="nowrap">
              <Text size="sm">{label}</Text>
              <Button
                size="compact-xs"
                variant="light"
                onClick={() => {
                  sort.mutate({ ids: before });
                  notifications.hide(id);
                }}
              >
                Undo
              </Button>
            </Group>
          ),
        });
      },
      onError: (e) => notifications.show({ color: "red", title: "Not sorted", message: errorMessage(e) }),
    });
  };
  const by = (label: string, s: Sort) => (
    <Menu.Item key={label} onClick={() => run(`Sorted by ${label.toLowerCase()}`, { sort: s })}>
      {label}
    </Menu.Item>
  );
  return (
    <Menu position="bottom-start" withinPortal>
      <Menu.Target>
        <Button size="xs" variant="default" leftSection={<IconArrowsSort size={16} />} loading={sort.isPending} disabled={deck.count < 2}>
          Sort
        </Button>
      </Menu.Target>
      <Menu.Dropdown>
        {by("Date taken, oldest first", { field: "taken" })}
        {by("Date taken, newest first", { field: "taken", desc: true })}
        {by("Date imported", { field: "imported" })}
        {by("Name", { field: "name" })}
        {by("Visual similarity", { field: "similar" })}
        {metrics?.map((m) => by(`Score: ${m.name}`, { field: "score", metricId: m.id }))}
        <Menu.Divider />
        {by("Shuffle", { field: "random", seed: Math.floor(Math.random() * 1e9) })}
        <Menu.Item onClick={() => run("Reversed", { ids: [...deck.ids].reverse() })}>Reverse</Menu.Item>
      </Menu.Dropdown>
    </Menu>
  );
}

/** A slide deck: its slides in order, and how they play. */
export function DeckPage() {
  const params = useParams();
  const id = Number(params.id);
  const { data: deck, error, isLoading } = useDeck(id);
  const navigate = useNavigate();
  const start = useStartSlideshow();
  const move = useMoveInDeck(id);
  const remove = useRemoveFromDeck(id);
  const add = useAddToDeck();
  const sort = useSortDeck(id);
  const del = useDeleteDeck();
  const wide = useMediaQuery("(min-width: 62em)", true);
  const [size, setSize] = useLocalStorage({ key: "pb-deck-thumb", defaultValue: 150 });
  const [open, setOpen] = useState<number | null>(null);
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const anchor = useRef<number | null>(null);

  const ids = useMemo(() => deck?.ids ?? [], [deck]);
  // Forget selected images that left the deck.
  useEffect(() => {
    setSelected((s) => {
      const keep = new Set(ids);
      const next = new Set([...s].filter((x) => keep.has(x)));
      return next.size === s.size ? s : next;
    });
  }, [ids]);

  const onSelect = useCallback(
    (x: number, e: { shiftKey: boolean }) => {
      setSelected((s) => {
        const next = new Set(s);
        if (e.shiftKey && anchor.current !== null) for (const y of rangeBetween(ids, anchor.current, x)) next.add(y);
        else if (next.has(x)) next.delete(x);
        else next.add(x);
        return next;
      });
      anchor.current = x;
    },
    [ids],
  );
  const onMove = useCallback(
    (moving: number[], before: number) =>
      move.mutate(
        { ids: moving, before },
        { onError: (e) => notifications.show({ color: "red", title: "Not moved", message: errorMessage(e) }) },
      ),
    [move],
  );
  const chosen = useMemo(() => ids.filter((x) => selected.has(x)), [ids, selected]);
  const removeChosen = () => {
    if (!chosen.length) return;
    const before = ids;
    const gone = chosen;
    remove.mutate(gone, {
      onSuccess: () => {
        setSelected(new Set());
        const nid = notifications.show({
          message: (
            <Group justify="space-between" wrap="nowrap">
              <Text size="sm">Took {plural(gone.length, "image")} out of the deck</Text>
              <Button
                size="compact-xs"
                variant="light"
                onClick={async () => {
                  notifications.hide(nid);
                  await add.mutateAsync({ id, ids: gone });
                  sort.mutate({ ids: before });
                }}
              >
                Undo
              </Button>
            </Group>
          ),
        });
      },
      onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
    });
  };

  useHotkeys(
    open === null
      ? [
          ["mod+A", () => setSelected(new Set(ids))],
          ["Escape", () => setSelected(new Set())],
          ["Delete", removeChosen],
        ]
      : [],
  );

  if (error) {
    return (
      <Stack p="md">
        <Alert color="red" title="No such deck">
          {errorMessage(error)}
        </Alert>
        <Anchor component={Link} to="/decks">
          All slide decks
        </Anchor>
      </Stack>
    );
  }
  if (isLoading || !deck) {
    return (
      <Center style={{ flex: 1 }}>
        <Loader />
      </Center>
    );
  }

  const side = (
    <Stack gap="lg">
      <DeckSettings key={deck.id} deck={deck} />
      <Notes deck={deck} />
    </Stack>
  );
  return (
    <Stack gap={0} style={{ flex: 1, minHeight: 0 }}>
      <Group px="md" pt="sm" pb={4} justify="space-between" wrap="nowrap">
        <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
          <Tooltip label="All slide decks">
            <ActionIcon component={Link} to="/decks" variant="subtle" color="gray" aria-label="All slide decks">
              <IconArrowLeft size={18} />
            </ActionIcon>
          </Tooltip>
          <NameEditor deck={deck} />
          <Text size="sm" c="dimmed" style={{ whiteSpace: "nowrap" }}>
            {plural(deck.count, "slide")}
            {deck.hidden ? (
              <Tooltip label="Images of this deck in the trash; restoring them puts them back in their place">
                <span> · {deck.hidden} in the trash</span>
              </Tooltip>
            ) : null}
          </Text>
        </Group>
        <Group gap="xs" wrap="nowrap">
          {!wide && (
            <Button variant="default" leftSection={<IconSettings size={16} />} onClick={() => setSettingsOpen(true)}>
              Settings
            </Button>
          )}
          <Button leftSection={<IconPlayerPlay size={16} />} disabled={!deck.count} onClick={() => start({ deck: deck.id })}>
            Play
          </Button>
          <Menu position="bottom-end" withinPortal>
            <Menu.Target>
              <ActionIcon variant="default" size="lg" aria-label="More">
                <IconDots size={18} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item color="red" leftSection={<IconTrash size={16} />} onClick={() => setDeleting(true)}>
                Delete deck
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      </Group>
      <div style={{ flex: 1, minHeight: 0, display: "flex" }}>
        <Stack gap={0} style={{ flex: 1, minWidth: 0 }}>
          <Group px="md" py={6} justify="space-between" wrap="nowrap" mih={44}>
            {chosen.length > 0 ? (
              <Group gap="xs" wrap="nowrap">
                <Text size="sm" fw={600} style={{ whiteSpace: "nowrap" }}>
                  {plural(chosen.length, "slide")} selected
                </Text>
                <Tooltip label="Move to the start">
                  <ActionIcon variant="light" onClick={() => onMove(chosen, ids.find((x) => !selected.has(x)) ?? 0)} aria-label="Move to the start">
                    <IconArrowBarToLeft size={16} />
                  </ActionIcon>
                </Tooltip>
                <Tooltip label="Move to the end">
                  <ActionIcon variant="light" onClick={() => onMove(chosen, 0)} aria-label="Move to the end">
                    <IconArrowBarToRight size={16} />
                  </ActionIcon>
                </Tooltip>
                <Button size="xs" variant="light" onClick={() => start({ ids: chosen })} leftSection={<IconPlayerPlay size={14} />}>
                  Play these
                </Button>
                <Button size="xs" variant="light" color="red" leftSection={<IconX size={14} />} onClick={removeChosen} loading={remove.isPending}>
                  Take out of deck
                </Button>
                <Button size="xs" variant="subtle" color="gray" onClick={() => setSelected(new Set())}>
                  Clear
                </Button>
              </Group>
            ) : (
              <Group gap="xs" wrap="nowrap">
                <Button size="xs" leftSection={<IconPhotoPlus size={16} />} onClick={() => setAdding(true)}>
                  Add images
                </Button>
                <SortMenu deck={deck} />
                {deck.count > 1 && (
                  <Text size="xs" c="dimmed" visibleFrom="md">
                    Drag slides to reorder; Ctrl- or Shift-click to select several.
                  </Text>
                )}
              </Group>
            )}
            <Group gap="xs" w={160} wrap="nowrap">
              <Text size="xs" c="dimmed">
                Size
              </Text>
              <Slider size="xs" min={90} max={320} step={10} value={size} onChange={setSize} style={{ flex: 1 }} label={null} />
            </Group>
          </Group>
          {deck.count === 0 ? (
            <Center style={{ flex: 1 }}>
              <EmptyState
                icon={<IconPresentation size={40} />}
                title="No slides yet"
                description="Add images by tag or name here, or select images in the library and use “Add to deck”."
              >
                <EmptyState.Actions>
                  <Button leftSection={<IconPhotoPlus size={16} />} onClick={() => setAdding(true)}>
                    Add images
                  </Button>
                  <Button variant="default" onClick={() => navigate("/")}>
                    Go to the library
                  </Button>
                </EmptyState.Actions>
              </EmptyState>
            </Center>
          ) : (
            <DeckGrid ids={ids} size={size} selected={selected} onSelect={onSelect} onOpen={setOpen} onMove={onMove} />
          )}
        </Stack>
        {wide && (
          <ScrollArea w={340} style={{ flex: "none", borderLeft: "1px solid var(--mantine-color-default-border)" }}>
            <Stack p="md" gap="md">
              <Title order={5}>Slideshow</Title>
              {side}
            </Stack>
          </ScrollArea>
        )}
      </div>
      {!wide && (
        <Drawer opened={settingsOpen} onClose={() => setSettingsOpen(false)} position="right" title="Slideshow" size={360}>
          {side}
        </Drawer>
      )}
      <Lightbox
        ids={ids}
        index={open}
        onIndex={setOpen}
        onClose={() => setOpen(null)}
        onPlay={(i) => start({ deck: deck.id, start: i })}
      />
      <AddImages deck={deck} opened={adding} onClose={() => setAdding(false)} />
      <Confirm
        opened={deleting}
        title={`Delete “${deck.name}”?`}
        confirm="Delete deck"
        loading={del.isPending}
        onClose={() => setDeleting(false)}
        onConfirm={() =>
          del.mutate(deck.id, {
            onSuccess: () => navigate("/decks", { replace: true }),
            onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
          })
        }
      >
        <Text size="sm">The deck and its order go; its images stay in the library.</Text>
      </Confirm>
    </Stack>
  );
}
