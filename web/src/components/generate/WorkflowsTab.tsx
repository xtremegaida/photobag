import {
  ActionIcon,
  Alert,
  Button,
  Center,
  FileButton,
  Group,
  Loader,
  Modal,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Tooltip,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconDownload, IconFileUpload, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../../api/client";
import { useWorkflow, useWorkflows } from "../../api/generate";
import { invalidateTopics } from "../../api/hooks";
import type { WorkflowDetail, WorkflowTemplate, WorkflowVersion } from "../../api/types";
import { Confirm } from "../Confirm";
import { formatDate } from "../../lib/format";
import { OutputBadge, WorkflowNodes } from "./WorkflowNodes";

/** Adds (id undefined) or edits a workflow template. */
function WorkflowEditor({ id, onClose }: { id?: number; onClose: () => void }) {
  const { data: existing } = useWorkflow(id);
  const [name, setName] = useState("");
  const [notes, setNotes] = useState("");
  const [json, setJson] = useState("");
  const [saving, setSaving] = useState(false);
  const qc = useQueryClient();
  useEffect(() => {
    if (existing) {
      setName(existing.workflow.name);
      setNotes(existing.workflow.notes);
      setJson(existing.version.json);
    }
  }, [existing]);
  const [debounced] = useDebouncedValue(json, 400);
  const parsed = useQuery({
    queryKey: ["workflow-parse", debounced],
    queryFn: () => api.post<WorkflowVersion>("/api/workflows/parse", { json: debounced }),
    enabled: debounced.trim() !== "",
    retry: false,
  });
  const loadFile = async (f: File | null) => {
    if (!f) return;
    setJson(await f.text());
    if (!name.trim()) setName(f.name.replace(/\.json$/i, "").replace(/[_-]+/g, " "));
  };
  const save = async () => {
    setSaving(true);
    try {
      const body = { name, notes, json };
      const d = id
        ? await api.patch<WorkflowDetail>(`/api/workflows/${id}`, body)
        : await api.post<WorkflowDetail>("/api/workflows", body);
      invalidateTopics(qc, ["workflows"]);
      notifications.show({ message: `Workflow “${d.workflow.name}” saved` });
      onClose();
    } catch (e) {
      notifications.show({ color: "red", title: "Not saved", message: errorMessage(e) });
    } finally {
      setSaving(false);
    }
  };
  if (id && !existing) {
    return (
      <Center p="xl">
        <Loader />
      </Center>
    );
  }
  const v = parsed.data;
  return (
    <Stack gap="sm">
      <Group grow align="flex-start">
        <TextInput label="Name" value={name} onChange={(e) => setName(e.currentTarget.value)} data-autofocus required />
        <TextInput label="Notes" value={notes} onChange={(e) => setNotes(e.currentTarget.value)} placeholder="optional" />
      </Group>
      <Textarea
        label={
          <Group gap="xs" component="span">
            <span>Workflow (API format)</span>
            <FileButton onChange={loadFile} accept="application/json,.json">
              {(props) => (
                <Button {...props} size="compact-xs" variant="light" leftSection={<IconFileUpload size={14} />}>
                  Load a file…
                </Button>
              )}
            </FileButton>
          </Group>
        }
        description="In ComfyUI: Workflow → Export (API). Each node is overridden by its title, so give the nodes you want to vary clear titles (Width, Positive, Checkpoint…)."
        value={json}
        onChange={(e) => setJson(e.currentTarget.value)}
        autosize
        minRows={6}
        maxRows={16}
        styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)", fontSize: 12 } }}
        placeholder='{ "3": { "inputs": { … }, "class_type": "KSampler", "_meta": { "title": "KSampler" } }, … }'
      />
      {parsed.error && <Alert color="red">{errorMessage(parsed.error)}</Alert>}
      {v && !parsed.error && (
        <>
          {v.problems.map((p) => (
            <Alert key={p} color="orange" variant="light" p="xs">
              <Text size="sm">{p}</Text>
            </Alert>
          ))}
          <Group gap="xs">
            <Text size="sm">
              {v.nodes.length} nodes · images come back by
            </Text>
            <OutputBadge output={v.output} />
          </Group>
          <WorkflowNodes nodes={v.nodes} />
        </>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          Cancel
        </Button>
        <Button onClick={save} loading={saving} disabled={!name.trim() || !json.trim() || !!parsed.error}>
          Save workflow
        </Button>
      </Group>
    </Stack>
  );
}

/** The saved workflow templates. */
export function WorkflowsTab() {
  const { data: workflows, error } = useWorkflows();
  const [editing, setEditing] = useState<number | "new" | null>(null);
  const [deleting, setDeleting] = useState<WorkflowTemplate | null>(null);
  const qc = useQueryClient();
  const remove = async (w: WorkflowTemplate) => {
    try {
      await api.del(`/api/workflows/${w.id}`);
      invalidateTopics(qc, ["workflows"]);
    } catch (e) {
      notifications.show({ color: "red", title: "Not deleted", message: errorMessage(e) });
    }
    setDeleting(null);
  };
  return (
    <Stack gap="md">
      <Group justify="space-between">
        <Text size="sm" c="dimmed" maw={720}>
          Workflow templates are ComfyUI workflows exported in the API format. Experiments run them with overrides:
          changed values of a node's inputs, found by the node's title.
        </Text>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setEditing("new")}>
          Add workflow
        </Button>
      </Group>
      {error && <Alert color="red">{errorMessage(error)}</Alert>}
      {workflows && workflows.length === 0 && (
        <Alert color="gray" variant="light">
          No workflows yet. In ComfyUI, open a workflow and use Workflow → Export (API), then add the file here.
        </Alert>
      )}
      {workflows && workflows.length > 0 && (
        <Table verticalSpacing="xs" highlightOnHover maw={1000}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Name</Table.Th>
              <Table.Th>Nodes</Table.Th>
              <Table.Th>Images come back by</Table.Th>
              <Table.Th>Changed</Table.Th>
              <Table.Th />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {workflows.map((w) => (
              <Table.Tr key={w.id}>
                <Table.Td>
                  <Text size="sm" fw={500}>
                    {w.name}
                  </Text>
                  {w.notes && (
                    <Text size="xs" c="dimmed">
                      {w.notes}
                    </Text>
                  )}
                </Table.Td>
                <Table.Td>{w.nodeCount}</Table.Td>
                <Table.Td>
                  <OutputBadge output={w.output} />
                </Table.Td>
                <Table.Td>
                  <Text size="xs" c="dimmed">
                    {formatDate(w.updatedAt)}
                  </Text>
                </Table.Td>
                <Table.Td>
                  <Group gap={4} justify="flex-end" wrap="nowrap">
                    <Tooltip label="Edit">
                      <ActionIcon variant="subtle" onClick={() => setEditing(w.id)} aria-label="Edit">
                        <IconPencil size={16} />
                      </ActionIcon>
                    </Tooltip>
                    <Tooltip label="Download the JSON">
                      <ActionIcon variant="subtle" component="a" href={`/api/workflows/${w.id}?download=1`} aria-label="Download">
                        <IconDownload size={16} />
                      </ActionIcon>
                    </Tooltip>
                    <Tooltip label="Delete">
                      <ActionIcon variant="subtle" color="red" onClick={() => setDeleting(w)} aria-label="Delete">
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Tooltip>
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      <Confirm
        opened={!!deleting}
        title={`Delete the workflow “${deleting?.name}”?`}
        confirm="Delete workflow"
        onConfirm={() => deleting && remove(deleting)}
        onClose={() => setDeleting(null)}
      >
        <Text size="sm">Images made with it keep the workflow they were made with, so they can still be made again.</Text>
      </Confirm>
      <Modal
        opened={editing !== null}
        onClose={() => setEditing(null)}
        title={editing === "new" ? "Add a workflow" : "Edit the workflow"}
        size="xl"
      >
        {editing !== null && <WorkflowEditor id={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      </Modal>
    </Stack>
  );
}
