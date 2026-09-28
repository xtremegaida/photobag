import { Button, Card, Checkbox, Select, Stack } from "@mantine/core";
import { useLocalStorage } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconUpload } from "@tabler/icons-react";
import { useState } from "react";
import { useSearchParams } from "react-router";
import { errorMessage } from "../api/client";
import { useCount, useSubmitJob } from "../api/hooks";
import type { ImageQuery } from "../api/types";
import { FolderPicker } from "../components/FolderPicker";
import { Page } from "../components/Page";
import { QueryBuilder, type Source } from "../components/QueryBuilder";
import { RecentJobs } from "../components/RecentJobs";

export function ExportPage() {
  const [sp] = useSearchParams();
  const [path, setPath] = useLocalStorage({ key: "pb-export-path", defaultValue: "" });
  const [query, setQuery] = useState<ImageQuery>({});
  const [keepStructure, setKeepStructure] = useState(false);
  const [manifest, setManifest] = useState(false);
  const [existing, setExisting] = useState("rename");
  const submit = useSubmitJob();
  const { data: count } = useCount(query);
  const start = () =>
    submit.mutate(
      { path: "/api/jobs/export", body: { path, options: { query, keepStructure, manifest, existing } } },
      { onError: (e) => notifications.show({ color: "red", title: "Export not started", message: errorMessage(e) }) },
    );
  return (
    <Page
      title="Export"
      description="Write the original files, byte for byte, to a folder on this machine. Names are made safe for Windows and Linux; clashing names get a numeric suffix."
      side={<RecentJobs kinds={["export"]} />}
    >
      <Card withBorder>
        <Stack>
          <QueryBuilder
            value={query}
            onChange={setQuery}
            initialSource={(sp.get("source") as Source) ?? "all"}
            label="Images to export"
          />
          <FolderPicker label="Destination folder" description="Created if it does not exist" value={path} onChange={setPath} />
          <Select
            label="When a file already exists"
            data={[
              { value: "rename", label: "Keep both (add a number)" },
              { value: "skip", label: "Skip the image" },
              { value: "overwrite", label: "Overwrite the file" },
            ]}
            value={existing}
            onChange={(v) => v && setExisting(v)}
            allowDeselect={false}
          />
          <Checkbox
            label="Recreate the original folder structure"
            description="Uses each image's path relative to the folder it was imported from"
            checked={keepStructure}
            onChange={(e) => setKeepStructure(e.currentTarget.checked)}
          />
          <Checkbox
            label="Write a manifest (photobag-manifest.json)"
            description="Lists each file with its id, tags and scores"
            checked={manifest}
            onChange={(e) => setManifest(e.currentTarget.checked)}
          />
          <Button
            leftSection={<IconUpload size={18} />}
            onClick={start}
            disabled={!path.trim() || !count?.count}
            loading={submit.isPending}
          >
            Export {count ? count.count.toLocaleString() : ""} image{count?.count === 1 ? "" : "s"}
          </Button>
        </Stack>
      </Card>
    </Page>
  );
}
