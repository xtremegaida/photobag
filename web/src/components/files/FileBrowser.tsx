import {
  ActionIcon,
  Anchor,
  Button,
  Checkbox,
  EmptyState,
  Group,
  Menu,
  ScrollArea,
  Stack,
  Table,
  Text,
  Tooltip,
} from "@mantine/core";
import { useHotkeys } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import {
  IconArrowsMove,
  IconChevronDown,
  IconCloudUpload,
  IconDots,
  IconDownload,
  IconFileExport,
  IconFileImport,
  IconFolderPlus,
  IconFolderUp,
  IconPencil,
  IconSortAscending,
  IconSortDescending,
  IconTrash,
  IconUpload,
  IconX,
} from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState, type DragEvent, type MouseEvent } from "react";
import { Link, useNavigate } from "react-router";
import { api, errorMessage } from "../../api/client";
import {
  checkExisting,
  fileContentUrl,
  filesRoute,
  joinPath,
  useDeleteFiles,
  useFileSummary,
  useMakeFolder,
  useMoveFiles,
  useRenameFile,
  zipUrl,
} from "../../api/files";
import { invalidateTopics } from "../../api/hooks";
import type { FileListing, FileNode } from "../../api/types";
import { gatherDrop, gatherInput, hasOsFiles, type Gathered } from "../../lib/files";
import { formatBytes, formatDate, plural } from "../../lib/format";
import { useUploads } from "../../stores/uploads";
import { Confirm } from "../Confirm";
import { FilePath, KindIcon, kindLabel, type FolderDrop } from "./common";
import { ConflictDialog, MoveDialog, NameDialog, TransferDialog } from "./dialogs";
import classes from "./Files.module.css";

const DRAG_TYPE = "application/x-photobag-files";

type SortKey = "name" | "size" | "modified";

function sortNodes(nodes: FileNode[], key: SortKey, desc: boolean): FileNode[] {
  const cmp = (a: FileNode, b: FileNode) => {
    let c = 0;
    if (key === "size") c = a.size - b.size;
    else if (key === "modified") c = a.modifiedAt - b.modifiedAt;
    if (c === 0) c = a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: "base" });
    return desc ? -c : c;
  };
  return [...nodes].sort((a, b) => (a.dir === b.dir ? cmp(a, b) : a.dir ? -1 : 1));
}

/** Downloads a URL without leaving the page. */
function download(url: string) {
  const a = document.createElement("a");
  a.href = url;
  a.download = "";
  document.body.appendChild(a);
  a.click();
  a.remove();
}

interface PendingUpload {
  gathered: Gathered;
  parent: number;
  dest: string;
  existing: string[];
}

/** A folder of the bag's files: list, select, upload, move, rename, delete. */
export function FileBrowser({ listing, path }: { listing: FileListing; path: string }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const folder = listing.node ?? null;
  const folderId = folder?.id ?? 0;
  const folderName = folder?.name ?? "Files";
  const children = useMemo(() => listing.children ?? [], [listing.children]);
  const { data: summary } = useFileSummary();

  const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>({ key: "name", desc: false });
  const rows = useMemo(() => sortNodes(children, sort.key, sort.desc), [children, sort]);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const anchor = useRef<number | null>(null);
  useEffect(() => {
    setSelected(new Set());
    anchor.current = null;
  }, [folderId]);
  // Forget selected items that went away (moved, deleted elsewhere).
  useEffect(() => {
    setSelected((s) => {
      const ids = new Set(children.map((c) => c.id));
      const next = new Set([...s].filter((id) => ids.has(id)));
      return next.size === s.size ? s : next;
    });
  }, [children]);
  const chosen = rows.filter((n) => selected.has(n.id));

  const [naming, setNaming] = useState<"new" | FileNode | null>(null);
  const [nameError, setNameError] = useState<string>();
  const [moving, setMoving] = useState<FileNode[] | null>(null);
  const [deleting, setDeleting] = useState<FileNode[] | null>(null);
  const [transfer, setTransfer] = useState<"import" | "export" | null>(null);
  const [pending, setPending] = useState<PendingUpload | null>(null);
  const makeFolder = useMakeFolder();
  const rename = useRenameFile();
  const move = useMoveFiles();
  const del = useDeleteFiles();
  const enqueue = useUploads((s) => s.enqueue);

  // --- selection -----------------------------------------------------------
  const click = (n: FileNode, e: MouseEvent) => {
    const i = rows.findIndex((r) => r.id === n.id);
    if (e.shiftKey && anchor.current !== null) {
      const j = rows.findIndex((r) => r.id === anchor.current);
      if (j >= 0) {
        const [a, b] = i < j ? [i, j] : [j, i];
        const range = rows.slice(a, b + 1).map((r) => r.id);
        setSelected((s) => (e.ctrlKey || e.metaKey ? new Set([...s, ...range]) : new Set(range)));
        return;
      }
    }
    anchor.current = n.id;
    if (e.ctrlKey || e.metaKey) {
      setSelected((s) => {
        const next = new Set(s);
        if (next.has(n.id)) next.delete(n.id);
        else next.add(n.id);
        return next;
      });
    } else setSelected(new Set([n.id]));
  };
  const toggle = (id: number) =>
    setSelected((s) => {
      const next = new Set(s);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      anchor.current = id;
      return next;
    });
  const childPath = (n: FileNode) => joinPath([path, n.name]);
  const open = (n: FileNode) => navigate(filesRoute(childPath(n)));

  // Keys act on the list only while no dialog is open.
  const dialog = naming !== null || !!moving || !!deleting || !!transfer || !!pending;
  const key = (fn: () => unknown) => () => {
    if (!dialog) fn();
  };
  useHotkeys([
    ["mod+A", key(() => setSelected(new Set(rows.map((r) => r.id))))],
    ["Escape", key(() => setSelected(new Set()))],
    ["Delete", key(() => chosen.length && setDeleting(chosen))],
    ["F2", key(() => chosen.length === 1 && setNaming(chosen[0]))],
    ["Enter", key(() => chosen.length === 1 && open(chosen[0]))],
  ]);

  // --- uploads -------------------------------------------------------------
  const upload = async (g: Gathered, parent: number, dest: string) => {
    if (!g.files.length && !g.dirs.length) return;
    try {
      for (const d of g.dirs) await api.post("/api/files/folders", { parent, path: d });
      if (g.dirs.length) invalidateTopics(qc, ["files"]);
      if (!g.files.length) return;
      const existing = await checkExisting(parent, g.files.map((f) => f.path).slice(0, 5000));
      if (existing.length) setPending({ gathered: g, parent, dest, existing });
      else enqueue(g.files, parent, "rename", dest);
    } catch (e) {
      notifications.show({ color: "red", title: "Upload not started", message: errorMessage(e) });
    }
  };
  const fileInput = useRef<HTMLInputElement>(null);
  const dirInput = useRef<HTMLInputElement>(null);
  useEffect(() => dirInput.current?.setAttribute("webkitdirectory", ""), []);
  const onPicked = (list: FileList | null) => {
    if (list?.length) upload(gatherInput(list), folderId, folderName);
  };

  // --- drag and drop -------------------------------------------------------
  const dragged = useRef<number[]>([]);
  const [draggingIds, setDraggingIds] = useState<Set<number>>(new Set());
  const [over, setOver] = useState<number | null>(null);
  const [osDrag, setOsDrag] = useState(false);
  const depth = useRef(0);

  const moveInto = (ids: number[], parent: number, parentName: string) => {
    const real = ids.filter((id) => id !== parent);
    if (!real.length) return;
    move.mutate(
      { ids: real, parent },
      {
        onSuccess: (r) => {
          setSelected(new Set());
          notifications.show({
            message: `Moved ${plural(r.moved, "item")} to ${parentName}${r.renamed ? ` (${r.renamed} renamed to keep both)` : ""}`,
          });
        },
        onError: (e) => notifications.show({ color: "red", title: "Not moved", message: errorMessage(e) }),
      },
    );
  };

  const drop: FolderDrop = {
    over,
    handlers: (target: number) => ({
      onDragOver: (e: DragEvent) => {
        const internal = [...e.dataTransfer.types].includes(DRAG_TYPE);
        if (!internal && !hasOsFiles(e.dataTransfer)) return;
        if (internal && dragged.current.includes(target)) return;
        e.preventDefault();
        e.stopPropagation();
        e.dataTransfer.dropEffect = internal ? "move" : "copy";
        setOver(target);
      },
      onDragLeave: (e: DragEvent) => {
        if (!(e.currentTarget as Node).contains(e.relatedTarget as Node | null)) setOver((o) => (o === target ? null : o));
      },
      onDrop: (e: DragEvent) => {
        e.preventDefault();
        e.stopPropagation();
        setOver(null);
        setOsDrag(false);
        depth.current = 0;
        const name =
          target === 0
            ? "Files"
            : (listing.path.find((p) => p.id === target)?.name ?? rows.find((r) => r.id === target)?.name ?? "the folder");
        if ([...e.dataTransfer.types].includes(DRAG_TYPE)) {
          moveInto(dragged.current, target, name);
          return;
        }
        if (hasOsFiles(e.dataTransfer)) gatherDrop(e.dataTransfer).then((g) => upload(g, target, name));
      },
    }),
  };

  const rowDragStart = (n: FileNode, e: DragEvent) => {
    const ids = selected.has(n.id) ? rows.filter((r) => selected.has(r.id)).map((r) => r.id) : [n.id];
    dragged.current = ids;
    setDraggingIds(new Set(ids));
    e.dataTransfer.setData(DRAG_TYPE, JSON.stringify(ids));
    e.dataTransfer.effectAllowed = "move";
  };
  const rowDragEnd = () => {
    dragged.current = [];
    setDraggingIds(new Set());
    setOver(null);
  };

  const zone = {
    onDragEnter: (e: DragEvent) => {
      if (!hasOsFiles(e.dataTransfer)) return;
      depth.current++;
      setOsDrag(true);
    },
    onDragLeave: (e: DragEvent) => {
      if (!hasOsFiles(e.dataTransfer)) return;
      depth.current = Math.max(0, depth.current - 1);
      if (depth.current === 0) setOsDrag(false);
    },
    onDragOver: (e: DragEvent) => {
      if (!hasOsFiles(e.dataTransfer)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "copy";
    },
    onDrop: (e: DragEvent) => {
      if (!hasOsFiles(e.dataTransfer)) return;
      e.preventDefault();
      depth.current = 0;
      setOsDrag(false);
      gatherDrop(e.dataTransfer).then((g) => upload(g, folderId, folderName));
    },
  };

  // --- actions -------------------------------------------------------------
  const submitName = (name: string) => {
    setNameError(undefined);
    const done = { onError: (e: unknown) => setNameError(errorMessage(e)) };
    if (naming === "new") {
      makeFolder.mutate({ parent: folderId, name }, { ...done, onSuccess: () => setNaming(null) });
    } else if (naming) {
      rename.mutate({ id: naming.id, name }, { ...done, onSuccess: () => setNaming(null) });
    }
  };
  const downloadNodes = (nodes: FileNode[]) => {
    if (nodes.length === 1 && !nodes[0].dir) download(fileContentUrl(nodes[0].id, true));
    else download(zipUrl(nodes.map((n) => n.id)));
  };

  const sortHeader = (key: SortKey, label: string, w?: number) => (
    <Table.Th
      w={w}
      className={classes.sortable}
      onClick={() => setSort((s) => ({ key, desc: s.key === key ? !s.desc : key !== "name" }))}
    >
      <Group gap={4} wrap="nowrap">
        {label}
        {sort.key === key && (sort.desc ? <IconSortDescending size={14} /> : <IconSortAscending size={14} />)}
      </Group>
    </Table.Th>
  );

  const rowMenu = (n: FileNode) => (
    <Menu position="bottom-end" withinPortal>
      <Menu.Target>
        <ActionIcon variant="subtle" color="gray" size="sm" aria-label={`More for ${n.name}`} onClick={(e) => e.stopPropagation()}>
          <IconDots size={16} />
        </ActionIcon>
      </Menu.Target>
      <Menu.Dropdown onClick={(e) => e.stopPropagation()}>
        <Menu.Item leftSection={<IconDownload size={16} />} onClick={() => downloadNodes([n])}>
          {n.dir ? "Download as zip" : "Download"}
        </Menu.Item>
        <Menu.Item leftSection={<IconPencil size={16} />} onClick={() => setNaming(n)}>
          Rename…
        </Menu.Item>
        <Menu.Item leftSection={<IconArrowsMove size={16} />} onClick={() => setMoving([n])}>
          Move to…
        </Menu.Item>
        <Menu.Divider />
        <Menu.Item color="red" leftSection={<IconTrash size={16} />} onClick={() => setDeleting([n])}>
          Delete…
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  );

  const deletingFiles = (deleting ?? []).reduce((n, d) => n + (d.dir ? 0 : 1), 0);
  const deletingBytes = (deleting ?? []).reduce((n, d) => n + d.size, 0);
  const folderInfo = folder
    ? `${plural(children.length, "item")} · ${formatBytes(folder.size)}`
    : summary
      ? `${plural(summary.files, "file")} in ${plural(summary.folders, "folder")} · ${formatBytes(summary.bytes)}`
      : "";

  return (
    <div className={classes.dropzone} {...zone}>
      <Stack gap="sm" p="md" pb={0}>
        <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm">
          <Stack gap={2} style={{ minWidth: 0, flex: 1 }}>
            <FilePath path={listing.path} current drop={drop} />
            <Text size="sm" c="dimmed">
              {folder
                ? folderInfo
                : `Notes, documentation and other files that travel with the library. ${folderInfo}`}
            </Text>
          </Stack>
          <Group gap="xs">
            <Button variant="default" leftSection={<IconFolderPlus size={16} />} onClick={() => setNaming("new")}>
              New folder
            </Button>
            <Menu position="bottom-end">
              <Menu.Target>
                <Button leftSection={<IconUpload size={16} />} rightSection={<IconChevronDown size={14} />}>
                  Upload
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item leftSection={<IconCloudUpload size={16} />} onClick={() => fileInput.current?.click()}>
                  Files…
                </Menu.Item>
                <Menu.Item leftSection={<IconFolderUp size={16} />} onClick={() => dirInput.current?.click()}>
                  A folder…
                </Menu.Item>
                <Menu.Divider />
                <Menu.Item leftSection={<IconFileImport size={16} />} onClick={() => setTransfer("import")}>
                  Import from this machine…
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
            <Menu position="bottom-end">
              <Menu.Target>
                <ActionIcon variant="default" size="lg" aria-label="More">
                  <IconDots size={18} />
                </ActionIcon>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item leftSection={<IconDownload size={16} />} disabled={!children.length} onClick={() => download(zipUrl(folder ? [folder.id] : []))}>
                  Download {folder ? "this folder" : "everything"} as zip
                </Menu.Item>
                <Menu.Item leftSection={<IconFileExport size={16} />} disabled={!children.length} onClick={() => setTransfer("export")}>
                  Export {folder ? "this folder" : "everything"} to this machine…
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
          </Group>
        </Group>
        <input ref={fileInput} type="file" multiple hidden onChange={(e) => (onPicked(e.currentTarget.files), (e.currentTarget.value = ""))} />
        <input ref={dirInput} type="file" hidden onChange={(e) => (onPicked(e.currentTarget.files), (e.currentTarget.value = ""))} />
        {chosen.length > 0 && (
          <Group gap="xs" px="sm" py={6} bg="var(--mantine-primary-color-light)" style={{ borderRadius: "var(--mantine-radius-md)" }}>
            <Text size="sm" fw={600} mr="xs">
              {chosen.length} selected
            </Text>
            <Button size="compact-sm" variant="subtle" leftSection={<IconDownload size={15} />} onClick={() => downloadNodes(chosen)}>
              {chosen.length === 1 && !chosen[0].dir ? "Download" : "Download as zip"}
            </Button>
            <Button size="compact-sm" variant="subtle" leftSection={<IconArrowsMove size={15} />} onClick={() => setMoving(chosen)}>
              Move to…
            </Button>
            <Button size="compact-sm" variant="subtle" leftSection={<IconFileExport size={15} />} onClick={() => setTransfer("export")}>
              Export…
            </Button>
            {chosen.length === 1 && (
              <Button size="compact-sm" variant="subtle" leftSection={<IconPencil size={15} />} onClick={() => setNaming(chosen[0])}>
                Rename…
              </Button>
            )}
            <Button size="compact-sm" variant="subtle" color="red" leftSection={<IconTrash size={15} />} onClick={() => setDeleting(chosen)}>
              Delete…
            </Button>
            <Tooltip label="Clear the selection (Esc)">
              <ActionIcon variant="subtle" color="gray" ml="auto" onClick={() => setSelected(new Set())} aria-label="Clear the selection">
                <IconX size={16} />
              </ActionIcon>
            </Tooltip>
          </Group>
        )}
      </Stack>

      {children.length === 0 ? (
        <EmptyState
          mt="xl"
          icon={<IconCloudUpload size={40} />}
          title={folder ? "This folder is empty" : "No files yet"}
          description="Drop files or whole folders here, or use Upload. Text files and notes open right here in the browser."
        />
      ) : (
        <ScrollArea style={{ flex: 1 }} px="md">
          <Table className={classes.table} highlightOnHover verticalSpacing={6} stickyHeader>
            <Table.Thead>
              <Table.Tr>
                <Table.Th w={36}>
                  <Checkbox
                    size="xs"
                    aria-label="Select all"
                    checked={selected.size > 0 && selected.size === rows.length}
                    indeterminate={selected.size > 0 && selected.size < rows.length}
                    onChange={() => setSelected(selected.size === rows.length ? new Set() : new Set(rows.map((r) => r.id)))}
                  />
                </Table.Th>
                {sortHeader("name", "Name")}
                {sortHeader("size", "Size", 110)}
                {sortHeader("modified", "Modified", 170)}
                <Table.Th w={130} visibleFrom="sm">
                  Kind
                </Table.Th>
                <Table.Th w={40} />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows.map((n) => (
                <Table.Tr
                  key={n.id}
                  data-selected={selected.has(n.id) || undefined}
                  data-dragging={draggingIds.has(n.id) || undefined}
                  data-drop={(n.dir && over === n.id) || undefined}
                  draggable
                  onDragStart={(e) => rowDragStart(n, e)}
                  onDragEnd={rowDragEnd}
                  {...(n.dir ? drop.handlers(n.id) : {})}
                  onClick={(e) => click(n, e)}
                  onDoubleClick={() => open(n)}
                >
                  <Table.Td>
                    <Checkbox
                      size="xs"
                      aria-label={`Select ${n.name}`}
                      checked={selected.has(n.id)}
                      onClick={(e) => e.stopPropagation()}
                      onChange={() => toggle(n.id)}
                    />
                  </Table.Td>
                  <Table.Td className={classes.name}>
                    <Group gap="xs" wrap="nowrap">
                      <KindIcon node={n} />
                      <Anchor
                        component={Link}
                        to={filesRoute(childPath(n))}
                        c="var(--mantine-color-text)"
                        truncate
                        draggable={false}
                        onClick={(e) => e.stopPropagation()}
                      >
                        {n.name}
                      </Anchor>
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" c="dimmed">
                      {n.dir ? (n.items ? formatBytes(n.size) : "empty") : formatBytes(n.size)}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" c="dimmed">
                      {formatDate(n.modifiedAt)}
                    </Text>
                  </Table.Td>
                  <Table.Td visibleFrom="sm">
                    <Text size="sm" c="dimmed" truncate>
                      {n.dir ? plural(n.items ?? 0, "item") : kindLabel(n)}
                    </Text>
                  </Table.Td>
                  <Table.Td>{rowMenu(n)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </ScrollArea>
      )}
      {osDrag && (
        <div className={classes.overlay}>
          <Stack align="center" gap={4}>
            <IconCloudUpload size={40} color="var(--mantine-color-blue-6)" />
            <Text fw={600}>Drop to upload into {folderName}</Text>
            <Text size="sm" c="dimmed">
              Or onto a folder in the list to put it there
            </Text>
          </Stack>
        </div>
      )}

      <NameDialog
        opened={naming !== null}
        title={naming === "new" ? `New folder in ${folderName}` : `Rename “${naming?.name ?? ""}”`}
        initial={naming === "new" ? "" : (naming?.name ?? "")}
        confirm={naming === "new" ? "Create" : "Rename"}
        loading={makeFolder.isPending || rename.isPending}
        error={nameError}
        onSubmit={submitName}
        onClose={() => {
          setNaming(null);
          setNameError(undefined);
        }}
      />
      <MoveDialog
        opened={!!moving}
        moving={moving ?? []}
        from={folderId}
        loading={move.isPending}
        onClose={() => setMoving(null)}
        onMove={(parent, name) => {
          const ids = (moving ?? []).map((m) => m.id);
          setMoving(null);
          moveInto(ids, parent, name);
        }}
      />
      <Confirm
        opened={!!deleting}
        title={deleting?.length === 1 ? `Delete “${deleting[0].name}”?` : `Delete ${deleting?.length} items?`}
        confirm="Delete"
        loading={del.isPending}
        onClose={() => setDeleting(null)}
        onConfirm={() =>
          deleting &&
          del.mutate(
            deleting.map((d) => d.id),
            {
              onSuccess: (r) => {
                setDeleting(null);
                setSelected(new Set());
                notifications.show({
                  message: `Deleted ${plural(r.files, "file")}${r.folders ? ` and ${plural(r.folders, "folder")}` : ""} (${formatBytes(r.bytes)})`,
                });
              },
              onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
            },
          )
        }
      >
        <Text size="sm">
          {deleting?.some((d) => d.dir)
            ? "Folders go with everything in them. "
            : deletingFiles > 1
              ? `${deletingFiles} files, ${formatBytes(deletingBytes)}. `
              : ""}
          This cannot be undone.
        </Text>
      </Confirm>
      <TransferDialog
        mode="import"
        opened={transfer === "import"}
        parent={folderId}
        parentName={folderName}
        onClose={() => setTransfer(null)}
      />
      <TransferDialog
        mode="export"
        opened={transfer === "export"}
        parent={folderId}
        parentName={folderName}
        ids={chosen.length ? chosen.map((c) => c.id) : folder ? [folder.id] : []}
        what={chosen.length === 1 ? `“${chosen[0].name}”` : chosen.length ? `${chosen.length} items` : folder ? `“${folder.name}”` : "all files"}
        onClose={() => setTransfer(null)}
      />
      <ConflictDialog
        existing={pending?.existing ?? null}
        total={pending?.gathered.files.length ?? 0}
        onClose={() => setPending(null)}
        onChoose={(conflict) => {
          if (pending) enqueue(pending.gathered.files, pending.parent, conflict, pending.dest);
          setPending(null);
        }}
      />
    </div>
  );
}
