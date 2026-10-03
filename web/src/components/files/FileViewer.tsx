import {
  ActionIcon,
  Alert,
  Button,
  Center,
  EmptyState,
  Group,
  Loader,
  Menu,
  ScrollArea,
  SegmentedControl,
  Stack,
  Text,
  Tooltip,
} from "@mantine/core";
import { useLocalStorage } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import {
  IconArrowsMove,
  IconDots,
  IconDownload,
  IconExternalLink,
  IconFileUnknown,
  IconPencil,
  IconTextWrap,
  IconTrash,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { lazy, Suspense, useState } from "react";
import { useNavigate } from "react-router";
import { api, errorMessage } from "../../api/client";
import { fileContentUrl, filesRoute, joinPath, useDeleteFiles, useMoveFiles, useRenameFile } from "../../api/files";
import type { FileListing, FileNode } from "../../api/types";
import { decodeText } from "../../lib/files";
import { formatBytes, formatDate } from "../../lib/format";
import { Confirm } from "../Confirm";
import { FilePath, kindLabel } from "./common";
import { MoveDialog, NameDialog } from "./dialogs";
import classes from "./Files.module.css";

const MarkdownNote = lazy(() => import("./Markdown"));

/** Lines in a text; a final line break does not start another. */
const lineCount = (t: string) => (t ? t.split("\n").length - (t.endsWith("\n") ? 1 : 0) : 0);

/** How much of a text file is shown in the browser. */
const TEXT_LIMIT = 2 << 20;

function useText(node: FileNode) {
  return useQuery({
    // Keyed by content, outside the "files" topic: a change elsewhere does
    // not refetch it, a replaced file does.
    queryKey: ["file-text", node.id, node.sha256],
    queryFn: async () => {
      const res = await fetch(fileContentUrl(node.id), {
        credentials: "same-origin",
        headers: node.size > TEXT_LIMIT ? { Range: `bytes=0-${TEXT_LIMIT - 1}` } : {},
      });
      if (!res.ok) throw new Error(res.statusText || `HTTP ${res.status}`);
      return decodeText(await res.arrayBuffer());
    },
    staleTime: Infinity,
  });
}

function TextView({ node, path, markdown }: { node: FileNode; path: string; markdown: boolean }) {
  const { data, error, isLoading } = useText(node);
  const [wrap, setWrap] = useLocalStorage({ key: "pb-files-wrap", defaultValue: true });
  const [mode, setMode] = useState<"rendered" | "source">("rendered");
  if (isLoading) return <Loader m="md" />;
  if (error) return <Alert color="red" m="md">{errorMessage(error)}</Alert>;
  if (!data) return null;
  const cut = node.size > TEXT_LIMIT;
  const rendered = markdown && mode === "rendered";
  return (
    <Stack gap={0} style={{ flex: 1, minHeight: 0 }}>
      <Group gap="xs" px="md" py={6} justify="space-between" style={{ borderBottom: "1px solid var(--mantine-color-default-border)" }}>
        <Text size="xs" c="dimmed">
          {cut ? `Showing the first ${formatBytes(TEXT_LIMIT)} of ${formatBytes(node.size)} · ` : ""}
          {data.encoding}
          {!rendered && ` · ${lineCount(data.text).toLocaleString()} lines`}
        </Text>
        <Group gap="xs">
          {markdown && (
            <SegmentedControl
              size="xs"
              value={mode}
              onChange={(v) => setMode(v as "rendered" | "source")}
              data={[
                { value: "rendered", label: "Formatted" },
                { value: "source", label: "Source" },
              ]}
            />
          )}
          {!rendered && (
            <Tooltip label={wrap ? "Don't wrap long lines" : "Wrap long lines"}>
              <ActionIcon variant={wrap ? "light" : "subtle"} color="gray" onClick={() => setWrap(!wrap)} aria-label="Wrap long lines">
                <IconTextWrap size={16} />
              </ActionIcon>
            </Tooltip>
          )}
        </Group>
      </Group>
      <ScrollArea style={{ flex: 1 }}>
        {rendered ? (
          <Suspense fallback={<Loader m="md" />}>
            <MarkdownNote text={data.text} notePath={path} />
          </Suspense>
        ) : (
          <pre className={classes.pre} data-wrap={wrap || undefined}>
            {data.text}
          </pre>
        )}
      </ScrollArea>
    </Stack>
  );
}

function Preview({ node, path }: { node: FileNode; path: string }) {
  const url = fileContentUrl(node.id);
  switch (node.kind) {
    case "text":
    case "markdown":
      return <TextView key={node.id} node={node} path={path} markdown={node.kind === "markdown"} />;
    case "image":
      return (
        <div className={classes.picture}>
          <img src={url} alt={node.name} />
        </div>
      );
    case "pdf":
      return <iframe className={classes.frame} src={url} title={node.name} />;
    case "audio":
      return (
        <Center p="xl">
          <audio controls src={url} style={{ width: "min(640px, 100%)" }} />
        </Center>
      );
    case "video":
      return (
        <div className={classes.picture}>
          <video controls src={url} style={{ maxWidth: "100%", maxHeight: "100%" }} />
        </div>
      );
  }
  return (
    <EmptyState
      mt="xl"
      icon={<IconFileUnknown size={40} />}
      title="No preview for this kind of file"
      description={`${kindLabel(node)}, ${formatBytes(node.size)}. Download it to open it with another program.`}
    >
      <Button component="a" href={fileContentUrl(node.id, true)} download leftSection={<IconDownload size={16} />}>
        Download
      </Button>
    </EmptyState>
  );
}

/** One file of the bag, shown in the browser where it can be. */
export function FileViewer({ listing, path }: { listing: FileListing; path: string }) {
  const node = listing.node!;
  const navigate = useNavigate();
  const parentPath = joinPath(listing.path.slice(0, -1).map((p) => p.name));
  const [renaming, setRenaming] = useState(false);
  const [nameError, setNameError] = useState<string>();
  const [moving, setMoving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const rename = useRenameFile();
  const move = useMoveFiles();
  const del = useDeleteFiles();
  const inline = node.kind !== "other";

  return (
    <div className={classes.viewer}>
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm" p="md" pb="sm">
        <Stack gap={2} style={{ minWidth: 0, flex: 1 }}>
          <FilePath path={listing.path} current />
          <Text size="sm" c="dimmed">
            {kindLabel(node)} · {formatBytes(node.size)} · modified {formatDate(node.modifiedAt)}
          </Text>
        </Stack>
        <Group gap="xs">
          <Button
            component="a"
            href={fileContentUrl(node.id, true)}
            download
            variant="default"
            leftSection={<IconDownload size={16} />}
          >
            Download
          </Button>
          {inline && (
            <Tooltip label="Open in a new tab">
              <ActionIcon component="a" href={fileContentUrl(node.id)} target="_blank" rel="noreferrer" variant="default" size="lg" aria-label="Open in a new tab">
                <IconExternalLink size={18} />
              </ActionIcon>
            </Tooltip>
          )}
          <Menu position="bottom-end">
            <Menu.Target>
              <ActionIcon variant="default" size="lg" aria-label="More">
                <IconDots size={18} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item leftSection={<IconPencil size={16} />} onClick={() => setRenaming(true)}>
                Rename…
              </Menu.Item>
              <Menu.Item leftSection={<IconArrowsMove size={16} />} onClick={() => setMoving(true)}>
                Move to…
              </Menu.Item>
              <Menu.Divider />
              <Menu.Item color="red" leftSection={<IconTrash size={16} />} onClick={() => setDeleting(true)}>
                Delete…
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      </Group>
      <div className={classes.viewer} style={{ borderTop: "1px solid var(--mantine-color-default-border)" }}>
        <Preview node={node} path={path} />
      </div>

      <NameDialog
        opened={renaming}
        title={`Rename “${node.name}”`}
        initial={node.name}
        confirm="Rename"
        loading={rename.isPending}
        error={nameError}
        onClose={() => {
          setRenaming(false);
          setNameError(undefined);
        }}
        onSubmit={(name) =>
          rename.mutate(
            { id: node.id, name },
            {
              onSuccess: (n) => {
                setRenaming(false);
                navigate(filesRoute(joinPath([parentPath, n.name])), { replace: true });
              },
              onError: (e) => setNameError(errorMessage(e)),
            },
          )
        }
      />
      <MoveDialog
        opened={moving}
        moving={[node]}
        from={node.parentId}
        loading={move.isPending}
        onClose={() => setMoving(false)}
        onMove={(parent) =>
          move.mutate(
            { ids: [node.id], parent },
            {
              onSuccess: async () => {
                setMoving(false);
                const l = await api.get<FileListing>(`/api/files?id=${node.id}`);
                navigate(filesRoute(joinPath(l.path.map((p) => p.name))), { replace: true });
              },
              onError: (e) => notifications.show({ color: "red", title: "Not moved", message: errorMessage(e) }),
            },
          )
        }
      />
      <Confirm
        opened={deleting}
        title={`Delete “${node.name}”?`}
        confirm="Delete"
        loading={del.isPending}
        onClose={() => setDeleting(false)}
        onConfirm={() =>
          del.mutate([node.id], {
            onSuccess: () => navigate(filesRoute(parentPath), { replace: true }),
            onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
          })
        }
      >
        <Text size="sm">This cannot be undone.</Text>
      </Confirm>
    </div>
  );
}
