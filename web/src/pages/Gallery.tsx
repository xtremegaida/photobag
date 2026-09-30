import { Alert, Badge, Button, Center, CloseButton, EmptyState, Group, Loader, Modal, Stack, Text, Title } from "@mantine/core";
import { useHotkeys, useLocalStorage } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconFolderUp, IconPhotoOff, IconTrash, IconTrashX } from "@tabler/icons-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { errorMessage } from "../api/client";
import { useImage, useImageIds, useStats, useSubmitJob } from "../api/hooks";
import type { ImageQuery, Sort } from "../api/types";
import { BulkBar } from "../components/BulkBar";
import { GalleryToolbar } from "../components/GalleryToolbar";
import { Lightbox } from "../components/Lightbox";
import { LibrarySlideshow } from "../components/slideshow/LibrarySlideshow";
import { useStartSlideshow } from "../components/slideshow/start";
import { ThumbGrid } from "../components/ThumbGrid";
import { plural } from "../lib/format";
import { galleryParams, hasFilters, parseGallery } from "../lib/query-url";
import { useSelection } from "../stores/selection";

function SimilarChip({ id, onClear }: { id: number; onClear: () => void }) {
  const { data } = useImage(id);
  return (
    <Badge
      variant="light"
      size="lg"
      rightSection={<CloseButton size="xs" onClick={onClear} aria-label="Clear" />}
      style={{ textTransform: "none" }}
    >
      Most similar to {data?.name ?? `#${id}`}
    </Badge>
  );
}

function ChosenChip({ ids, onClear }: { ids: number[]; onClear: () => void }) {
  const { data } = useImage(ids.length === 1 ? ids[0] : undefined);
  return (
    <Badge
      variant="light"
      size="lg"
      rightSection={<CloseButton size="xs" onClick={onClear} aria-label="Show all images" />}
      style={{ textTransform: "none" }}
    >
      {ids.length === 1 ? `Only ${data?.name ?? `#${ids[0]}`}` : `Only ${ids.length} chosen images`}
    </Badge>
  );
}

function EmptyTrashButton({ count }: { count: number }) {
  const [open, setOpen] = useState(false);
  const submit = useSubmitJob();
  return (
    <>
      <Button color="red" variant="light" size="xs" leftSection={<IconTrashX size={16} />} onClick={() => setOpen(true)} disabled={!count}>
        Empty trash
      </Button>
      <Modal opened={open} onClose={() => setOpen(false)} title="Empty the trash?">
        <Stack>
          <Text size="sm">
            {plural(count, "image")} will be permanently removed from the bag. Their comparison history is kept, and
            re-importing the same files later will skip them unless you choose “include removed”.
          </Text>
          <Text size="sm" c="dimmed">
            The file only shrinks after Compact (on the Backup page).
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              color="red"
              loading={submit.isPending}
              onClick={() =>
                submit.mutate(
                  { path: "/api/jobs/empty-trash", body: {} },
                  {
                    onSuccess: () => setOpen(false),
                    onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
                  },
                )
              }
            >
              Delete permanently
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}

export function GalleryPage({ trash = false }: { trash?: boolean }) {
  const [sp, setSp] = useSearchParams();
  const state = useMemo(() => parseGallery(sp, trash), [sp, trash]);
  const { data, isLoading, error, isFetching } = useImageIds(state.query, state.sort);
  const ids = useMemo(() => data?.ids ?? [], [data]);
  const { data: stats } = useStats();
  const [thumbSize, setThumbSize] = useLocalStorage({ key: "pb-thumb-size", defaultValue: 180 });
  const [fit, setFit] = useLocalStorage({ key: "pb-thumb-fit", defaultValue: false });
  const [captions, setCaptions] = useLocalStorage({ key: "pb-thumb-captions", defaultValue: false });
  const [open, setOpen] = useState<number | null>(null);
  const selected = useSelection((s) => s.selected);
  const toggle = useSelection((s) => s.toggle);
  const extend = useSelection((s) => s.extend);
  const setSel = useSelection((s) => s.set);
  const clear = useSelection((s) => s.clear);
  const startSlideshow = useStartSlideshow();

  useEffect(() => {
    clear();
  }, [trash, clear]);

  const onChange = useCallback(
    (query: ImageQuery, sort: Sort) => {
      setSp(galleryParams({ query, sort }), { replace: true });
    },
    [setSp],
  );

  const onSelect = useCallback((id: number, range: boolean) => (range ? extend(ids, id) : toggle(id)), [ids, extend, toggle]);

  useHotkeys([
    ["mod+A", () => setSel(ids)],
    ["Escape", () => open === null && clear()],
  ]);

  const filtered = hasFilters(state.query);
  const empty = !isLoading && ids.length === 0;

  return (
    <Stack gap={0} style={{ flex: 1, minHeight: 0 }}>
      <Group px="md" pt="sm" justify="space-between" wrap="nowrap">
        <Group gap="sm" wrap="nowrap">
          <Title order={3}>{trash ? "Trash" : "Library"}</Title>
          <Text c="dimmed" size="sm">
            {data ? plural(data.total, "image") : ""}
            {filtered && stats ? ` of ${(trash ? stats.trashed : stats.images).toLocaleString()}` : ""}
          </Text>
          {isFetching && !isLoading && <Loader size="xs" />}
          {state.sort.field === "similar" && state.sort.similarTo ? (
            <SimilarChip id={state.sort.similarTo} onClear={() => onChange(state.query, { field: "imported", desc: true })} />
          ) : null}
          {state.query.ids?.length ? (
            <ChosenChip ids={state.query.ids} onClear={() => onChange({ ...state.query, ids: undefined }, state.sort)} />
          ) : null}
        </Group>
        {trash ? (
          <EmptyTrashButton count={stats?.trashed ?? 0} />
        ) : (
          <LibrarySlideshow count={ids.length} params={galleryParams(state)} firstId={ids[0]} />
        )}
      </Group>
      <GalleryToolbar
        query={state.query}
        sort={state.sort}
        onChange={onChange}
        thumbSize={thumbSize}
        onThumbSize={setThumbSize}
        fit={fit}
        onFit={setFit}
        captions={captions}
        onCaptions={setCaptions}
      />
      {error ? (
        <Alert color="red" m="md" title="Could not list images">
          {errorMessage(error)}
        </Alert>
      ) : isLoading ? (
        <Center style={{ flex: 1 }}>
          <Loader />
        </Center>
      ) : empty ? (
        <Center style={{ flex: 1 }}>
          {trash ? (
            <EmptyState icon={<IconTrash size={40} />} title="The trash is empty" description="Images you remove, or duplicates you resolve, land here." />
          ) : filtered ? (
            <EmptyState icon={<IconPhotoOff size={40} />} title="No images match" description="Try removing some filters." />
          ) : (
            <EmptyState
              icon={<IconPhotoOff size={40} />}
              title="This bag is empty"
              description="Import a folder of images to get started."
            >
              <EmptyState.Actions>
                <Button component={Link} to="/import" leftSection={<IconFolderUp size={18} />}>
                  Import images
                </Button>
              </EmptyState.Actions>
            </EmptyState>
          )}
        </Center>
      ) : (
        <ThumbGrid
          ids={ids}
          size={thumbSize}
          selected={selected}
          onOpen={setOpen}
          onSelect={onSelect}
          fit={fit}
          captions={captions}
          focused={open ?? undefined}
          scrollToIndex={open ?? undefined}
        />
      )}
      <BulkBar allIds={ids} trash={trash} />
      <Lightbox
        ids={ids}
        index={open}
        onIndex={setOpen}
        onClose={() => setOpen(null)}
        onPlay={trash ? undefined : (i) => startSlideshow({ params: galleryParams(state), start: i })}
      />
    </Stack>
  );
}
