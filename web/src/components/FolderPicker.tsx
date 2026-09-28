import { ActionIcon, Button, Group, Modal, NavLink, ScrollArea, Select, Stack, Text, TextInput, Tooltip } from "@mantine/core";
import { IconArrowUp, IconFolder, IconFolderOpen, IconHome } from "@tabler/icons-react";
import { useState } from "react";
import { errorMessage } from "../api/client";
import { useFsList } from "../api/hooks";

interface Props {
  label: string;
  description?: string;
  value: string;
  onChange: (path: string) => void;
  placeholder?: string;
}

/** A path field with a server-side folder browser (paths are on the machine running PhotoBag). */
export function FolderPicker({ label, description, value, onChange, placeholder }: Props) {
  const [open, setOpen] = useState(false);
  const [browse, setBrowse] = useState("");
  const { data, error, isFetching } = useFsList(browse, open);
  return (
    <>
      <Group align="flex-end" gap="xs" wrap="nowrap">
        <TextInput
          label={label}
          description={description}
          placeholder={placeholder ?? "Folder path on the PhotoBag machine"}
          value={value}
          onChange={(e) => onChange(e.currentTarget.value)}
          style={{ flex: 1 }}
        />
        <Button
          variant="default"
          leftSection={<IconFolderOpen size={16} />}
          onClick={() => {
            setBrowse(value);
            setOpen(true);
          }}
        >
          Browse…
        </Button>
      </Group>
      <Modal opened={open} onClose={() => setOpen(false)} title="Choose a folder" size="lg">
        <Stack gap="xs">
          <Group gap="xs" wrap="nowrap">
            <Tooltip label="Parent folder">
              <ActionIcon
                variant="default"
                size="lg"
                disabled={!data?.parent}
                onClick={() => data && setBrowse(data.parent)}
                aria-label="Parent folder"
              >
                <IconArrowUp size={16} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="Home folder">
              <ActionIcon variant="default" size="lg" onClick={() => data && setBrowse(data.home)} aria-label="Home folder">
                <IconHome size={16} />
              </ActionIcon>
            </Tooltip>
            {data && data.roots.length > 1 && (
              <Select
                data={data.roots.map((r) => ({ value: r.path, label: r.name }))}
                value={data.roots.find((r) => data.path.toUpperCase().startsWith(r.path.toUpperCase()))?.path ?? null}
                onChange={(v) => v && setBrowse(v)}
                w={90}
                allowDeselect={false}
              />
            )}
            <TextInput
              value={browse || data?.path || ""}
              onChange={(e) => setBrowse(e.currentTarget.value)}
              style={{ flex: 1 }}
            />
          </Group>
          {error && (
            <Text c="red" size="sm">
              {errorMessage(error)}
            </Text>
          )}
          <ScrollArea h={360} type="auto">
            {data?.dirs.length === 0 && (
              <Text c="dimmed" size="sm" p="sm">
                No sub-folders.
              </Text>
            )}
            {data?.dirs.map((d) => (
              <NavLink
                key={d.path}
                label={d.name}
                leftSection={<IconFolder size={16} />}
                onClick={() => setBrowse(d.path)}
                py={4}
              />
            ))}
          </ScrollArea>
          <Group justify="space-between">
            <Text size="sm" c="dimmed">
              {isFetching ? "Loading…" : data ? `${data.images} image file(s) directly in this folder` : ""}
            </Text>
            <Group gap="xs">
              <Button variant="default" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button
                disabled={!data}
                onClick={() => {
                  if (data) onChange(data.path);
                  setOpen(false);
                }}
              >
                Use this folder
              </Button>
            </Group>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}
