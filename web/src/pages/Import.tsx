import { Button, Card, Checkbox, CloseButton, Group, Paper, Progress, Stack, TagsInput, Text } from "@mantine/core";
import { useLocalStorage } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconCloudUpload, IconFiles, IconFolderUp } from "@tabler/icons-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { errorMessage } from "../api/client";
import { useSubmitJob } from "../api/hooks";
import type { ImportOptions } from "../api/types";
import { DropOverlay, fileDropClasses, useFileDrop } from "../components/FileDrop";
import { FolderPicker } from "../components/FolderPicker";
import { Page } from "../components/Page";
import { RecentJobs } from "../components/RecentJobs";
import { useTagNames } from "../components/TagEditor";
import { describeDrop, gatherInput, type Gathered, type UploadEntry } from "../lib/files";
import { formatBytes, plural } from "../lib/format";
import { useImportUpload } from "../stores/importUpload";

/** Progress of files uploading for import. */
function Uploading() {
  const u = useImportUpload();
  const label =
    u.phase === "checking"
      ? `Looking at ${plural(u.files, "file")}…`
      : u.phase === "starting"
        ? "Starting the import…"
        : `Uploading ${u.title}…`;
  return (
    <Paper withBorder p="sm" radius="md">
      <Stack gap={6}>
        <Group justify="space-between" wrap="nowrap" gap="xs">
          <Text size="sm" fw={600} truncate>
            {label}
          </Text>
          <Button size="compact-xs" variant="subtle" color="red" onClick={u.cancel} disabled={u.phase === "starting"}>
            Cancel
          </Button>
        </Group>
        <Progress
          value={u.phase === "uploading" && u.bytes ? (u.loaded / u.bytes) * 100 : 100}
          animated={u.phase !== "uploading"}
          striped={u.phase !== "uploading"}
        />
        {u.phase === "uploading" && (
          <Text size="xs" c="dimmed">
            {u.sent + u.failed} of {plural(u.files, "image")} · {formatBytes(u.loaded)} of {formatBytes(u.bytes)}
            {u.leftOut ? ` · ${plural(u.leftOut, "other file")} left out` : ""}
            {u.failed ? ` · ${u.failed} failed` : ""}
          </Text>
        )}
      </Stack>
    </Paper>
  );
}

export function ImportPage() {
  const [path, setPath] = useLocalStorage({ key: "pb-import-path", defaultValue: "" });
  const [opts, setOpts] = useState<ImportOptions>({ recursive: true, tagFolders: false, skipIdentical: false, includeRemoved: false });
  const [tags, setTags] = useState<string[]>([]);
  const names = useTagNames();
  const submit = useSubmitJob();
  const uploading = useImportUpload((s) => s.phase !== "idle");
  const startUpload = useImportUpload((s) => s.start);
  const set = (patch: Partial<ImportOptions>) => setOpts((o) => ({ ...o, ...patch }));

  // Files dropped or chosen in the browser, to upload in place of the folder.
  const [dropped, setDropped] = useState<UploadEntry[] | null>(null);
  const take = (g: Gathered) => {
    // A path once: the server keeps one file per path.
    const files = [...new Map(g.files.map((f) => [f.path, f])).values()];
    if (files.length) setDropped(files);
    else notifications.show({ message: "There are no files in what was dropped." });
  };
  const drop = useFileDrop(take, !uploading);
  const fileInput = useRef<HTMLInputElement>(null);
  const dirInput = useRef<HTMLInputElement>(null);
  useEffect(() => dirInput.current?.setAttribute("webkitdirectory", ""), []);
  const onPicked = (list: FileList | null) => {
    if (list?.length) take(gatherInput(list));
  };
  // Without sub-folders, a dropped folder gives the files directly in it.
  const chosen = useMemo(
    () => dropped && (opts.recursive ? dropped : dropped.filter((e) => e.path.split("/").length <= 2)),
    [dropped, opts.recursive],
  );
  const chosenBytes = chosen?.reduce((n, e) => n + e.file.size, 0) ?? 0;

  const start = () => {
    const options = { ...opts, tags };
    if (chosen) {
      startUpload(chosen, options).then((job) => job && setDropped(null));
      return;
    }
    submit.mutate(
      { path: "/api/jobs/import", body: { path, options } },
      { onError: (e) => notifications.show({ color: "red", title: "Import not started", message: errorMessage(e) }) },
    );
  };

  return (
    <div className={fileDropClasses.zone} {...drop.handlers}>
      <Page
        title="Import"
        description="Add images from a folder on this machine, or drop images and folders onto this page to upload them. Files are recognised by content (JPEG, PNG, GIF, WebP, BMP, TIFF); other files are skipped and listed in the report. Identical files share storage, so importing duplicates costs no space. Remove them later in Duplicates."
        side={<RecentJobs kinds={["import"]} />}
      >
        <Card withBorder>
          <Stack>
            {uploading ? (
              <Uploading />
            ) : chosen ? (
              <Paper withBorder p="sm" radius="md">
                <Group justify="space-between" wrap="nowrap" gap="sm">
                  <Group gap="sm" wrap="nowrap" style={{ minWidth: 0 }}>
                    <IconFiles size={28} stroke={1.5} style={{ flexShrink: 0 }} />
                    <div style={{ minWidth: 0 }}>
                      <Text size="sm" fw={600} truncate>
                        {describeDrop(dropped!)}
                      </Text>
                      <Text size="xs" c="dimmed">
                        {plural(chosen.length, "file")} · {formatBytes(chosenBytes)}
                        {chosen.length < dropped!.length ? ` · ${dropped!.length - chosen.length} in sub-folders left out` : ""}.
                        Only images are uploaded.
                      </Text>
                    </div>
                  </Group>
                  <CloseButton onClick={() => setDropped(null)} aria-label="Import a folder instead" />
                </Group>
              </Paper>
            ) : (
              <>
                <FolderPicker label="Folder" value={path} onChange={setPath} />
                <Paper withBorder p="sm" radius="md" style={{ borderStyle: "dashed" }}>
                  <Group gap="sm" wrap="nowrap">
                    <IconCloudUpload size={28} stroke={1.5} color="var(--mantine-color-dimmed)" style={{ flexShrink: 0 }} />
                    <div>
                      <Text size="sm">Or drop images and folders onto this page to upload them from this browser.</Text>
                      <Group gap={4} mt={2}>
                        <Button size="compact-sm" variant="subtle" onClick={() => fileInput.current?.click()}>
                          Choose files…
                        </Button>
                        <Button size="compact-sm" variant="subtle" onClick={() => dirInput.current?.click()}>
                          Choose a folder…
                        </Button>
                      </Group>
                    </div>
                  </Group>
                </Paper>
              </>
            )}
            <input
              ref={fileInput}
              type="file"
              multiple
              accept="image/*"
              hidden
              onChange={(e) => (onPicked(e.currentTarget.files), (e.currentTarget.value = ""))}
            />
            <input ref={dirInput} type="file" hidden onChange={(e) => (onPicked(e.currentTarget.files), (e.currentTarget.value = ""))} />
            <Checkbox label="Include sub-folders" checked={opts.recursive} onChange={(e) => set({ recursive: e.currentTarget.checked })} />
            <Checkbox
              label="Tag images with their folder names"
              description="2019/Holiday/beach.jpg gets the tags “2019” and “Holiday”"
              checked={!!opts.tagFolders}
              onChange={(e) => set({ tagFolders: e.currentTarget.checked })}
            />
            <Checkbox
              label="Skip files already in the bag"
              description="Bit-identical copies are not added again"
              checked={!!opts.skipIdentical}
              onChange={(e) => set({ skipIdentical: e.currentTarget.checked })}
            />
            <Checkbox
              label="Include previously removed images"
              description="By default, files identical to images you trashed or purged, or to originals replaced by re-encoding, are skipped"
              checked={!!opts.includeRemoved}
              onChange={(e) => set({ includeRemoved: e.currentTarget.checked })}
            />
            <TagsInput
              label="Tag every imported image"
              data={names}
              value={tags}
              onChange={setTags}
              placeholder="Optional tags"
              acceptValueOnBlur
              splitChars={[","]}
            />
            {chosen ? (
              <Button leftSection={<IconCloudUpload size={18} />} onClick={start} disabled={uploading || !chosen.length}>
                Upload and import {plural(chosen.length, "file")}
              </Button>
            ) : (
              <Button leftSection={<IconFolderUp size={18} />} onClick={start} disabled={!path.trim() || uploading} loading={submit.isPending}>
                Start import
              </Button>
            )}
          </Stack>
        </Card>
      </Page>
      {drop.over && <DropOverlay title="Drop to upload for import">Only images are uploaded; folders keep their names</DropOverlay>}
    </div>
  );
}
