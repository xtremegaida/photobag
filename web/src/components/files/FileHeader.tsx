import { ActionIcon, Button, Group, Menu, Stack, Text, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconArrowsMove, IconDots, IconDownload, IconExternalLink, IconPencil, IconTrash } from "@tabler/icons-react";
import { type ReactNode, useState } from "react";
import { useNavigate } from "react-router";
import { api, errorMessage } from "../../api/client";
import { fileContentUrl, filesRoute, joinPath, useDeleteFiles, useMoveFiles, useRenameFile } from "../../api/files";
import type { FileListing } from "../../api/types";
import { formatBytes, formatDate } from "../../lib/format";
import { Confirm } from "../Confirm";
import { FilePath, kindLabel } from "./common";
import { MoveDialog, NameDialog } from "./dialogs";

/** The path of the folder holding the file of a listing. */
export const parentPathOf = (listing: FileListing) => joinPath(listing.path.slice(0, -1).map((p) => p.name));

/** A file's path and particulars, with actions on the right. */
export function FileHeader({ listing, children }: { listing: FileListing; children: ReactNode }) {
  const node = listing.node!;
  return (
    <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm" p="md" pb="sm">
      <Stack gap={2} style={{ minWidth: 0, flex: 1 }}>
        <FilePath path={listing.path} current />
        <Text size="sm" c="dimmed">
          {kindLabel(node)} · {formatBytes(node.size)} · modified {formatDate(node.modifiedAt)}
        </Text>
      </Stack>
      <Group gap="xs">{children}</Group>
    </Group>
  );
}

/** Download, open in a new tab, rename, move and delete. */
export function FileActions({ listing }: { listing: FileListing }) {
  const node = listing.node!;
  const navigate = useNavigate();
  const parentPath = parentPathOf(listing);
  const [renaming, setRenaming] = useState(false);
  const [nameError, setNameError] = useState<string>();
  const [moving, setMoving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const rename = useRenameFile();
  const move = useMoveFiles();
  const del = useDeleteFiles();
  const inline = node.kind !== "other";

  return (
    <>
      <Button component="a" href={fileContentUrl(node, true)} download variant="default" leftSection={<IconDownload size={16} />}>
        Download
      </Button>
      {inline && (
        <Tooltip label="Open in a new tab">
          <ActionIcon component="a" href={fileContentUrl(node)} target="_blank" rel="noreferrer" variant="default" size="lg" aria-label="Open in a new tab">
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
    </>
  );
}
