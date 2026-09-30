import { Anchor, Button, Group, Menu, Modal, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconPlus, IconPresentation } from "@tabler/icons-react";
import { type ReactNode, useState } from "react";
import { useNavigate } from "react-router";
import { errorMessage } from "../../api/client";
import { type DeckImages, useAddToDeck, useCreateDeck, useDecks } from "../../api/decks";
import type { DeckAdded, DeckDetail } from "../../api/types";
import { plural } from "../../lib/format";
import { useLibrarySlideshowSettings } from "./start";

/** Shows what adding did, with a link to the deck. */
function useAddedNotice() {
  const navigate = useNavigate();
  return (deck: { id: number; name: string }, r: DeckAdded) => {
    const to = `/decks/${deck.id}`;
    notifications.show({
      title: r.added ? `Added ${plural(r.added, "image")} to ${deck.name}` : `Nothing added to ${deck.name}`,
      message: (
        <>
          {r.present ? `${plural(r.present, "image")} ${r.present === 1 ? "was" : "were"} in it already. ` : ""}
          {/* Notifications render outside the router: navigate from here. */}
          <Anchor
            href={to}
            size="sm"
            onClick={(e) => {
              e.preventDefault();
              navigate(to);
            }}
          >
            Open the deck
          </Anchor>
        </>
      ),
    });
  };
}

/** Names and creates a deck, optionally with images in it. */
export function NewDeck({
  opened,
  onClose,
  images,
  count = 0,
  onCreated,
}: {
  opened: boolean;
  onClose: () => void;
  images?: DeckImages;
  count?: number;
  onCreated: (d: DeckDetail) => void;
}) {
  const [name, setName] = useState("");
  const create = useCreateDeck();
  // New decks start out playing like library slideshows last did.
  const [settings] = useLibrarySlideshowSettings();
  const submit = () =>
    create.mutate(
      { name, settings, ...images },
      {
        onSuccess: (d) => {
          setName("");
          onClose();
          onCreated(d);
        },
        onError: (e) => notifications.show({ color: "red", title: "No deck made", message: errorMessage(e) }),
      },
    );
  return (
    <Modal opened={opened} onClose={onClose} title="New slide deck">
      <Stack>
        <TextInput
          label="Name"
          placeholder="Summer 2026"
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
          onKeyDown={(e) => {
            e.stopPropagation(); // keep the lightbox's keys out of the field
            if (e.key === "Enter" && name.trim()) submit();
          }}
          data-autofocus
        />
        {count > 0 && (
          <Text size="sm" c="dimmed">
            It starts with {plural(count, "image")}.
          </Text>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!name.trim()} loading={create.isPending}>
            Create
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

/** A menu that adds images to a deck, or to a new one. */
export function AddToDeck({
  images,
  count,
  children,
}: {
  images: DeckImages;
  count: number;
  /** The button that opens the menu. */
  children: ReactNode;
}) {
  const { data: decks } = useDecks();
  const add = useAddToDeck();
  const notice = useAddedNotice();
  const [creating, setCreating] = useState(false);
  return (
    <>
      <Menu position="top-start" shadow="md" withinPortal>
        <Menu.Target>{children}</Menu.Target>
        <Menu.Dropdown mah={360} style={{ overflowY: "auto" }}>
          <Menu.Label>Add {plural(count, "image")} to</Menu.Label>
          {decks?.map((d) => (
            <Menu.Item
              key={d.id}
              leftSection={<IconPresentation size={16} />}
              rightSection={
                <Text size="xs" c="dimmed">
                  {d.count}
                </Text>
              }
              onClick={() =>
                add.mutate(
                  { id: d.id, ...images },
                  {
                    onSuccess: (r) => notice(d, r),
                    onError: (e) => notifications.show({ color: "red", title: "Not added", message: errorMessage(e) }),
                  },
                )
              }
            >
              {d.name}
            </Menu.Item>
          ))}
          {!!decks?.length && <Menu.Divider />}
          <Menu.Item leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
            New deck…
          </Menu.Item>
        </Menu.Dropdown>
      </Menu>
      <NewDeck
        opened={creating}
        onClose={() => setCreating(false)}
        images={images}
        count={count}
        onCreated={(d) => notice(d, { added: d.count, present: 0 })}
      />
    </>
  );
}
