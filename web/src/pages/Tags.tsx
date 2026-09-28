import { ActionIcon, Anchor, Button, Group, Modal, ScrollArea, Stack, Table, Text, TextInput, Title, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconPlus, IconSparkles, IconTrash } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link } from "react-router";
import { api, errorMessage } from "../api/client";
import { invalidateTopics, useTags } from "../api/hooks";
import type { Tag } from "../api/types";

function RenameModal({ tag, onClose, existing }: { tag: Tag | null; onClose: () => void; existing: Tag[] }) {
  const [name, setName] = useState("");
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const target = existing.find((t) => t.id !== tag?.id && t.name.toLocaleLowerCase() === name.trim().toLocaleLowerCase());
  const save = async () => {
    if (!tag) return;
    setBusy(true);
    try {
      const r = await api.patch<{ merged: boolean }>(`/api/tags/${tag.id}`, { name });
      notifications.show({ message: r.merged ? `Merged “${tag.name}” into “${target?.name ?? name}”` : "Tag renamed" });
      invalidateTopics(qc, ["tags"]);
      onClose();
    } catch (e) {
      notifications.show({ color: "red", message: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened={!!tag} onClose={onClose} title={`Rename “${tag?.name}”`}>
      <Stack>
        <TextInput
          label="New name"
          defaultValue={tag?.name}
          onChange={(e) => setName(e.currentTarget.value)}
          onKeyDown={(e) => e.key === "Enter" && save()}
          data-autofocus
        />
        {target && (
          <Text size="sm" c="orange">
            A tag called “{target.name}” exists: the two will be merged ({target.count} + {tag?.count} images).
          </Text>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={save} loading={busy} disabled={!name.trim()}>
            {target ? "Merge" : "Rename"}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

export function TagsPage() {
  const { data: tags = [] } = useTags();
  const qc = useQueryClient();
  const [filter, setFilter] = useState("");
  const [newTag, setNewTag] = useState("");
  const [renaming, setRenaming] = useState<Tag | null>(null);
  const [deleting, setDeleting] = useState<Tag | null>(null);
  const shown = useMemo(
    () => tags.filter((t) => t.name.toLocaleLowerCase().includes(filter.toLocaleLowerCase())),
    [tags, filter],
  );
  const create = async () => {
    if (!newTag.trim()) return;
    try {
      await api.post("/api/tags", { name: newTag });
      setNewTag("");
      invalidateTopics(qc, ["tags"]);
    } catch (e) {
      notifications.show({ color: "red", message: errorMessage(e) });
    }
  };
  const del = async () => {
    if (!deleting) return;
    try {
      await api.del(`/api/tags/${deleting.id}`);
      invalidateTopics(qc, ["tags"]);
      setDeleting(null);
    } catch (e) {
      notifications.show({ color: "red", message: errorMessage(e) });
    }
  };
  return (
    <Stack p="md" gap="md" style={{ flex: 1, minHeight: 0 }}>
      <Group justify="space-between">
        <Title order={3}>Tags</Title>
        <Group gap="xs">
          <TextInput placeholder="Filter" value={filter} onChange={(e) => setFilter(e.currentTarget.value)} w={200} />
          <TextInput
            placeholder="New tag"
            value={newTag}
            onChange={(e) => setNewTag(e.currentTarget.value)}
            onKeyDown={(e) => e.key === "Enter" && create()}
            w={200}
          />
          <Button leftSection={<IconPlus size={16} />} onClick={create} disabled={!newTag.trim()}>
            Create
          </Button>
        </Group>
      </Group>
      {tags.length === 0 ? (
        <Text c="dimmed">
          No tags yet. Tag images in the library (select images, then “Add tags”), or import with “tag with folder
          names”.
        </Text>
      ) : (
        <ScrollArea style={{ flex: 1 }}>
          <Table highlightOnHover verticalSpacing="xs" maw={720}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Tag</Table.Th>
                <Table.Th w={120} ta="right">
                  Images
                </Table.Th>
                <Table.Th w={100} />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {shown.map((t) => (
                <Table.Tr key={t.id}>
                  <Table.Td>
                    <Anchor component={Link} to={`/?tag=${encodeURIComponent(t.name)}`}>
                      {t.name}
                    </Anchor>
                  </Table.Td>
                  <Table.Td ta="right">
                    {t.auto > 0 && (
                      <Tooltip label={`${t.auto.toLocaleString()} of these added by analysis`}>
                        <IconSparkles size={14} style={{ verticalAlign: "-2px", marginRight: 6, opacity: 0.6 }} aria-label="added by analysis" />
                      </Tooltip>
                    )}
                    {t.count.toLocaleString()}
                  </Table.Td>
                  <Table.Td>
                    <Group gap={4} justify="flex-end">
                      <Tooltip label="Rename or merge">
                        <ActionIcon variant="subtle" onClick={() => setRenaming(t)}>
                          <IconPencil size={16} />
                        </ActionIcon>
                      </Tooltip>
                      <Tooltip label="Delete tag">
                        <ActionIcon variant="subtle" color="red" onClick={() => setDeleting(t)}>
                          <IconTrash size={16} />
                        </ActionIcon>
                      </Tooltip>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </ScrollArea>
      )}
      <RenameModal key={renaming?.id} tag={renaming} existing={tags} onClose={() => setRenaming(null)} />
      <Modal opened={!!deleting} onClose={() => setDeleting(null)} title={`Delete tag “${deleting?.name}”?`}>
        <Stack>
          <Text size="sm">
            The tag is removed from {deleting?.count ?? 0} image(s). The images themselves are not affected.
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button color="red" onClick={del}>
              Delete tag
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
}
