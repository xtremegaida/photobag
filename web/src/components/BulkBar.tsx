import { Button, Group, Paper, Popover, Stack, TagsInput, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconChartBar, IconRestore, IconTagMinus, IconTagPlus, IconTrash, IconUpload, IconX } from "@tabler/icons-react";
import { useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { errorMessage } from "../api/client";
import { useBulkTags, useRestore, useTrash } from "../api/hooks";
import { plural } from "../lib/format";
import { useSelection } from "../stores/selection";
import { useTagNames } from "./TagEditor";

function TagAction({ mode, ids }: { mode: "add" | "remove"; ids: number[] }) {
  const names = useTagNames();
  const [tags, setTags] = useState<string[]>([]);
  const [open, setOpen] = useState(false);
  const bulk = useBulkTags();
  const apply = () => {
    if (!tags.length) return;
    bulk.mutate(
      { ids, [mode]: tags },
      {
        onSuccess: () => {
          notifications.show({
            message: `${mode === "add" ? "Tagged" : "Untagged"} ${plural(ids.length, "image")}: ${tags.join(", ")}`,
          });
          setTags([]);
          setOpen(false);
        },
        onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
      },
    );
  };
  return (
    <Popover opened={open} onChange={setOpen} position="top" shadow="md" withArrow trapFocus>
      <Popover.Target>
        <Button
          size="xs"
          variant="light"
          leftSection={mode === "add" ? <IconTagPlus size={16} /> : <IconTagMinus size={16} />}
          onClick={() => setOpen((o) => !o)}
        >
          {mode === "add" ? "Add tags" : "Remove tags"}
        </Button>
      </Popover.Target>
      <Popover.Dropdown>
        <Stack gap="xs" w={280}>
          <TagsInput
            data={names}
            value={tags}
            onChange={setTags}
            placeholder={mode === "add" ? "Choose or create tags" : "Tags to remove"}
            acceptValueOnBlur
            splitChars={[","]}
            autoFocus
          />
          <Button size="xs" onClick={apply} loading={bulk.isPending} disabled={!tags.length}>
            {mode === "add" ? "Add" : "Remove"} on {plural(ids.length, "image")}
          </Button>
        </Stack>
      </Popover.Dropdown>
    </Popover>
  );
}

/** Actions for the current selection. */
export function BulkBar({ allIds, trash }: { allIds: number[]; trash?: boolean }) {
  const selected = useSelection((s) => s.selected);
  const setSel = useSelection((s) => s.set);
  const clear = useSelection((s) => s.clear);
  const ids = useMemo(() => [...selected], [selected]);
  const trashM = useTrash();
  const restoreM = useRestore();
  const navigate = useNavigate();
  if (ids.length === 0) {
    return null;
  }
  return (
    <Paper withBorder shadow="md" px="md" py="xs" radius={0} style={{ borderLeft: 0, borderRight: 0, borderBottom: 0 }}>
      <Group justify="space-between" gap="xs">
        <Group gap="xs">
          <Text fw={600} size="sm">
            {plural(ids.length, "image")} selected
          </Text>
          {ids.length < allIds.length && (
            <Button size="xs" variant="subtle" onClick={() => setSel(allIds)}>
              Select all {allIds.length.toLocaleString()}
            </Button>
          )}
          <Button size="xs" variant="subtle" color="gray" leftSection={<IconX size={14} />} onClick={clear}>
            Clear
          </Button>
        </Group>
        <Group gap="xs">
          {trash ? (
            <Button
              size="xs"
              leftSection={<IconRestore size={16} />}
              loading={restoreM.isPending}
              onClick={() => restoreM.mutate(ids, { onSuccess: () => clear() })}
            >
              Restore
            </Button>
          ) : (
            <>
              <TagAction mode="add" ids={ids} />
              <TagAction mode="remove" ids={ids} />
              <Button size="xs" variant="light" leftSection={<IconUpload size={16} />} onClick={() => navigate("/export?source=selection")}>
                Export
              </Button>
              <Button
                size="xs"
                variant="light"
                leftSection={<IconChartBar size={16} />}
                disabled={ids.length < 2}
                onClick={() => navigate("/scoring?new=selection")}
              >
                Score
              </Button>
              <Button
                size="xs"
                variant="light"
                color="red"
                leftSection={<IconTrash size={16} />}
                loading={trashM.isPending}
                onClick={() =>
                  trashM.mutate(ids, {
                    onSuccess: (r) => {
                      notifications.show({ message: `Moved ${plural(r.trashed, "image")} to the trash` });
                      clear();
                    },
                  })
                }
              >
                Trash
              </Button>
            </>
          )}
        </Group>
      </Group>
    </Paper>
  );
}
