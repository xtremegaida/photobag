import {
  ActionIcon,
  Alert,
  Button,
  Card,
  EmptyState,
  Group,
  Image,
  Loader,
  Menu,
  SimpleGrid,
  Stack,
  Text,
  Title,
  Tooltip,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDots, IconPlayerPlay, IconPlus, IconPresentation, IconTrash } from "@tabler/icons-react";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { errorMessage, thumbUrl } from "../api/client";
import { useDecks, useDeleteDeck } from "../api/decks";
import type { Deck } from "../api/types";
import { Confirm } from "../components/Confirm";
import { NewDeck } from "../components/slideshow/AddToDeck";
import { useStartSlideshow } from "../components/slideshow/start";
import { formatDate, plural } from "../lib/format";
import { describeSettings } from "../lib/slideshow";

function Covers({ ids, background }: { ids: number[]; background: string }) {
  if (ids.length === 0) {
    return (
      <Group h={130} justify="center" style={{ background }}>
        <IconPresentation size={36} color="var(--mantine-color-dimmed)" stroke={1.2} />
      </Group>
    );
  }
  const shown = ids.slice(0, ids.length === 3 ? 2 : 4);
  return (
    <SimpleGrid cols={shown.length === 1 ? 1 : 2} spacing={2} h={130} style={{ overflow: "hidden", background }}>
      {shown.map((id) => (
        <Image key={id} src={thumbUrl(id)} h={shown.length > 2 ? 64 : 130} fit="contain" alt="" />
      ))}
    </SimpleGrid>
  );
}

function DeckCard({ deck, onDelete }: { deck: Deck; onDelete: () => void }) {
  const start = useStartSlideshow();
  return (
    <Card withBorder padding="sm" component={Link} to={`/decks/${deck.id}`}>
      <Card.Section>
        <Covers ids={deck.covers} background={deck.settings.background} />
      </Card.Section>
      <Group justify="space-between" mt="sm" wrap="nowrap" gap="xs">
        <Text fw={600} truncate>
          {deck.name}
        </Text>
        <Group gap={2} wrap="nowrap" onClick={(e) => e.preventDefault()}>
          <Tooltip label="Play">
            <ActionIcon variant="subtle" disabled={!deck.count} onClick={() => start({ deck: deck.id })} aria-label="Play">
              <IconPlayerPlay size={18} />
            </ActionIcon>
          </Tooltip>
          <Menu position="bottom-end" withinPortal>
            <Menu.Target>
              <ActionIcon variant="subtle" color="gray" aria-label="More">
                <IconDots size={18} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item color="red" leftSection={<IconTrash size={16} />} onClick={onDelete}>
                Delete deck
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      </Group>
      <Text size="xs" c="dimmed">
        {plural(deck.count, "slide")}
        {deck.hidden ? ` · ${deck.hidden} in the trash` : ""} · {formatDate(deck.updatedAt)}
      </Text>
      <Text size="xs" c="dimmed" truncate>
        {describeSettings(deck.settings)}
      </Text>
    </Card>
  );
}

/** The bag's slide decks. */
export function DecksPage() {
  const { data, error, isLoading } = useDecks();
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<Deck | null>(null);
  const del = useDeleteDeck();
  const navigate = useNavigate();
  return (
    <Stack gap="md" p="md" style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
      <Group justify="space-between">
        <div>
          <Title order={3}>Slide decks</Title>
          <Text size="sm" c="dimmed" maw={680}>
            A deck is a set of images in an order you choose, with the settings of its slideshow. Add images from the
            library (select them and use “Add to deck”) or by tags, then drag them into order.
          </Text>
        </div>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
          New deck
        </Button>
      </Group>
      {error && <Alert color="red">{errorMessage(error)}</Alert>}
      {isLoading && <Loader />}
      {data && data.length === 0 && (
        <EmptyState
          icon={<IconPresentation size={40} />}
          title="No slide decks yet"
          description="Make one here, or choose images in the library and add them to a new deck. The library can also play its images as a slideshow directly."
        />
      )}
      <SimpleGrid cols={{ base: 1, sm: 2, md: 3, xl: 4 }}>
        {data?.map((d) => (
          <DeckCard key={d.id} deck={d} onDelete={() => setDeleting(d)} />
        ))}
      </SimpleGrid>
      <NewDeck opened={creating} onClose={() => setCreating(false)} onCreated={(d) => navigate(`/decks/${d.id}`)} />
      <Confirm
        opened={!!deleting}
        title={`Delete “${deleting?.name}”?`}
        confirm="Delete deck"
        loading={del.isPending}
        onClose={() => setDeleting(null)}
        onConfirm={() =>
          deleting &&
          del.mutate(deleting.id, {
            onSuccess: () => setDeleting(null),
            onError: (e) => notifications.show({ color: "red", message: errorMessage(e) }),
          })
        }
      >
        <Text size="sm">The deck and its order go; its images stay in the library.</Text>
      </Confirm>
    </Stack>
  );
}
