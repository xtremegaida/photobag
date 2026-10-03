import { Button, Group, List, Modal, NavLink, ScrollArea, Select, Stack, Text, TextInput } from "@mantine/core";
import { useLocalStorage } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconFolder, IconFolderOpen, IconFolders } from "@tabler/icons-react";
import { useEffect, useMemo, useState } from "react";
import { errorMessage } from "../../api/client";
import { useFileFolders } from "../../api/files";
import { useSubmitJob } from "../../api/hooks";
import type { FileNode } from "../../api/types";
import { splitExt } from "../../lib/files";
import { FolderPicker } from "../FolderPicker";

/** Asks for a name: a new folder's, or a new name for something. */
export function NameDialog({
  opened,
  title,
  initial = "",
  confirm,
  loading,
  error,
  onSubmit,
  onClose,
}: {
  opened: boolean;
  title: string;
  initial?: string;
  confirm: string;
  loading?: boolean;
  error?: string;
  onSubmit: (name: string) => void;
  onClose: () => void;
}) {
  const [name, setName] = useState(initial);
  useEffect(() => {
    if (opened) setName(initial);
  }, [opened, initial]);
  return (
    <Modal opened={opened} onClose={onClose} title={title}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (name.trim()) onSubmit(name.trim());
        }}
      >
        <Stack>
          <TextInput
            label="Name"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            error={error}
            data-autofocus
            // Select the name without its extension, as file managers do.
            onFocus={(e) => e.currentTarget.setSelectionRange(0, splitExt(e.currentTarget.value)[0].length)}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={loading} disabled={!name.trim() || name.trim() === initial}>
              {confirm}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
}

interface TreeNode {
  folder: FileNode;
  kids: TreeNode[];
}

function FolderBranch({
  node,
  depth,
  value,
  blocked,
  onPick,
}: {
  node: TreeNode;
  depth: number;
  value: number;
  blocked: Set<number>;
  onPick: (id: number) => void;
}) {
  const off = blocked.has(node.folder.id);
  return (
    <NavLink
      label={node.folder.name}
      leftSection={value === node.folder.id ? <IconFolderOpen size={16} /> : <IconFolder size={16} />}
      active={value === node.folder.id}
      disabled={off}
      onClick={() => onPick(node.folder.id)}
      childrenOffset={18}
      defaultOpened={depth < 1}
      py={4}
    >
      {!off && node.kids.length > 0
        ? node.kids.map((k) => <FolderBranch key={k.folder.id} node={k} depth={depth + 1} value={value} blocked={blocked} onPick={onPick} />)
        : undefined}
    </NavLink>
  );
}

/** Chooses a folder to move things into. */
export function MoveDialog({
  opened,
  moving,
  from,
  loading,
  onMove,
  onClose,
}: {
  opened: boolean;
  /** What is being moved: folders among them cannot go into themselves. */
  moving: FileNode[];
  /** The folder they are in now. */
  from: number;
  loading?: boolean;
  onMove: (parent: number, name: string) => void;
  onClose: () => void;
}) {
  const { data: folders } = useFileFolders(opened);
  const [target, setTarget] = useState(from);
  useEffect(() => {
    if (opened) setTarget(from);
  }, [opened, from]);
  const tree = useMemo(() => {
    const byParent = new Map<number, FileNode[]>();
    for (const f of folders ?? []) byParent.set(f.parentId, [...(byParent.get(f.parentId) ?? []), f]);
    const build = (parent: number): TreeNode[] => (byParent.get(parent) ?? []).map((f) => ({ folder: f, kids: build(f.id) }));
    return build(0);
  }, [folders]);
  const blocked = useMemo(() => new Set(moving.filter((m) => m.dir).map((m) => m.id)), [moving]);
  const what = moving.length === 1 ? `“${moving[0].name}”` : `${moving.length} items`;
  return (
    <Modal opened={opened} onClose={onClose} title={`Move ${what}`}>
      <Stack>
        <ScrollArea.Autosize mah="50vh">
          <NavLink
            label="Files (top level)"
            leftSection={<IconFolders size={16} />}
            active={target === 0}
            onClick={() => setTarget(0)}
            py={4}
          />
          {tree.map((n) => (
            <FolderBranch key={n.folder.id} node={n} depth={0} value={target} blocked={blocked} onPick={setTarget} />
          ))}
        </ScrollArea.Autosize>
        <Text size="xs" c="dimmed">
          Anything whose name is taken there is kept apart with a number, such as “notes (2).md”.
        </Text>
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Cancel
          </Button>
          <Button
            onClick={() => onMove(target, target === 0 ? "Files" : (folders?.find((f) => f.id === target)?.name ?? "the folder"))}
            loading={loading}
            disabled={target === from}
          >
            Move here
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

/** Asks what to do about names that are already taken before uploading. */
export function ConflictDialog({
  existing,
  total,
  onChoose,
  onClose,
}: {
  existing: string[] | null;
  total: number;
  onChoose: (conflict: "rename" | "replace" | "skip") => void;
  onClose: () => void;
}) {
  const n = existing?.length ?? 0;
  return (
    <Modal opened={!!existing} onClose={onClose} title={n === 1 ? "This file is already here" : `${n} files are already here`}>
      <Stack>
        <List size="sm" spacing={2}>
          {(existing ?? []).slice(0, 8).map((p) => (
            <List.Item key={p}>{p}</List.Item>
          ))}
          {n > 8 && <List.Item>… and {n - 8} more</List.Item>}
        </List>
        <Text size="sm" c="dimmed">
          {total > n ? `The other ${total - n} upload as they are. ` : ""}Replacing a file keeps its place but changes its
          content; folders of the same name are merged either way.
        </Text>
        <Group justify="flex-end">
          <Button variant="default" onClick={() => onChoose("skip")}>
            Skip {n === 1 ? "it" : "them"}
          </Button>
          <Button variant="default" onClick={() => onChoose("rename")}>
            Keep both
          </Button>
          <Button color="orange" onClick={() => onChoose("replace")} data-autofocus>
            Replace
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

const conflictChoices = [
  { value: "rename", label: "Keep both (add a number)" },
  { value: "skip", label: "Skip the file" },
  { value: "replace", label: "Replace the file" },
];

/** Copies a folder or file from the server's disk into the files, or files out to its disk, as a job. */
export function TransferDialog({
  mode,
  opened,
  parent,
  parentName,
  ids,
  what,
  onClose,
}: {
  mode: "import" | "export";
  opened: boolean;
  /** Import: the folder to copy into. */
  parent: number;
  parentName: string;
  /** Export: what to copy out (nothing: everything). */
  ids?: number[];
  what?: string;
  onClose: () => void;
}) {
  const [path, setPath] = useLocalStorage({ key: `pb-files-${mode}-path`, defaultValue: "" });
  const [conflict, setConflict] = useState<string>(mode === "import" ? "rename" : "rename");
  const submit = useSubmitJob();
  const go = () =>
    submit.mutate(
      mode === "import"
        ? { path: "/api/jobs/files-import", body: { path, parent, conflict } }
        : { path: "/api/jobs/files-export", body: { path, ids: ids ?? [], conflict } },
      {
        onSuccess: () => {
          notifications.show({
            message: mode === "import" ? `Importing into ${parentName}; follow it under Jobs` : "Exporting; follow it under Jobs",
          });
          onClose();
        },
        onError: (e) => notifications.show({ color: "red", title: "Not started", message: errorMessage(e) }),
      },
    );
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={mode === "import" ? `Import into ${parentName}` : `Export ${what ?? "files"}`}
      size="lg"
    >
      <Stack>
        <Text size="sm" c="dimmed">
          {mode === "import"
            ? "Copies a file, or a folder with everything in it, from the machine running PhotoBag. A folder arrives as itself; hidden files and system clutter are left out."
            : "Writes the files, keeping their folders and modification times, to a folder on the machine running PhotoBag."}
        </Text>
        <FolderPicker
          label={mode === "import" ? "File or folder to import" : "Destination folder"}
          description={mode === "export" ? "Created if it does not exist" : undefined}
          value={path}
          onChange={setPath}
          placeholder={mode === "import" ? "Path on the PhotoBag machine" : undefined}
        />
        <Select
          label="When a file of the same name is already there"
          data={conflictChoices}
          value={conflict}
          onChange={(v) => v && setConflict(v)}
          allowDeselect={false}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={go} loading={submit.isPending} disabled={!path.trim()}>
            {mode === "import" ? "Import" : "Export"}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
