import { Button, Card, Checkbox, Stack, TagsInput } from "@mantine/core";
import { useLocalStorage } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconFolderUp } from "@tabler/icons-react";
import { useState } from "react";
import { errorMessage } from "../api/client";
import { useSubmitJob } from "../api/hooks";
import type { ImportOptions } from "../api/types";
import { FolderPicker } from "../components/FolderPicker";
import { Page } from "../components/Page";
import { RecentJobs } from "../components/RecentJobs";
import { useTagNames } from "../components/TagEditor";

export function ImportPage() {
  const [path, setPath] = useLocalStorage({ key: "pb-import-path", defaultValue: "" });
  const [opts, setOpts] = useState<ImportOptions>({ recursive: true, tagFolders: false, skipIdentical: false, includeRemoved: false });
  const [tags, setTags] = useState<string[]>([]);
  const names = useTagNames();
  const submit = useSubmitJob();
  const set = (patch: Partial<ImportOptions>) => setOpts((o) => ({ ...o, ...patch }));
  const start = () =>
    submit.mutate(
      { path: "/api/jobs/import", body: { path, options: { ...opts, tags } } },
      { onError: (e) => notifications.show({ color: "red", title: "Import not started", message: errorMessage(e) }) },
    );
  return (
    <Page
      title="Import"
      description="Add images from a folder on this machine. Files are recognised by content (JPEG, PNG, GIF, WebP, BMP, TIFF); other files are skipped and listed in the report. Identical files share storage, so importing duplicates costs no space. Remove them later in Duplicates."
      side={<RecentJobs kinds={["import"]} />}
    >
      <Card withBorder>
        <Stack>
          <FolderPicker label="Folder" value={path} onChange={setPath} />
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
            description="By default, files identical to images you trashed or purged are skipped"
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
          <Button leftSection={<IconFolderUp size={18} />} onClick={start} disabled={!path.trim()} loading={submit.isPending}>
            Start import
          </Button>
        </Stack>
      </Card>
    </Page>
  );
}
