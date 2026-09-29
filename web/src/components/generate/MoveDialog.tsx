import { Anchor, Button, Group, Modal, Stack, TagsInput, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useState } from "react";
import { useNavigate } from "react-router";
import { errorMessage } from "../../api/client";
import { useMoveGenerations } from "../../api/generate";
import { plural } from "../../lib/format";
import { useTagNames } from "../TagEditor";

/** Moves held images to the library, tagging them on the way. */
export function MoveDialog({ ids, onClose, onMoved }: { ids: number[] | null; onClose: () => void; onMoved?: () => void }) {
  const [tags, setTags] = useState<string[]>([]);
  const names = useTagNames();
  const move = useMoveGenerations();
  const navigate = useNavigate();
  const n = ids?.length ?? 0;
  const submit = () => {
    if (!ids) return;
    move.mutate(
      { ids, tags },
      {
        onSuccess: (r) => {
          // Notifications render outside the router: navigate from here.
          const to = `/?${r.imageIds.map((id) => `id=${id}`).join("&")}`;
          notifications.show({
            title: `${plural(r.moved, "image")} moved to the library`,
            message: (
              <Anchor
                href={to}
                size="sm"
                onClick={(e) => {
                  e.preventDefault();
                  navigate(to);
                }}
              >
                Show them in the library
              </Anchor>
            ),
          });
          onMoved?.();
          onClose();
        },
        onError: (e) => notifications.show({ color: "red", title: "Not moved", message: errorMessage(e) }),
      },
    );
  };
  return (
    <Modal opened={!!ids} onClose={onClose} title={`Move ${plural(n, "image")} to the library`}>
      <Stack>
        <Text size="sm" c="dimmed">
          They leave the experiment and join the library like imported images. How they were made stays with them, so the
          experiment can be picked up again from any of them.
        </Text>
        <TagsInput
          label="Tags"
          data={names}
          value={tags}
          onChange={setTags}
          placeholder="Add tags…"
          splitChars={[","]}
          acceptValueOnBlur
          limit={30}
          data-autofocus
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} loading={move.isPending}>
            Move {plural(n, "image")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
