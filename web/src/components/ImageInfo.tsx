import { ActionIcon, Anchor, Badge, Button, CloseButton, Group, Stack, Table, Text, TextInput, Title, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDownload, IconExternalLink, IconPhotoSearch, IconPlus, IconRestore, IconTrash } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { api, errorMessage, originalUrl } from "../api/client";
import { invalidateTopics, useImageDetail, useRename, useRestore, useTrash } from "../api/hooks";
import type { DeckRef, Image } from "../api/types";
import { formatBytes, formatDate, formatScore, formatTaken } from "../lib/format";
import { formatName } from "../lib/reencode";
import { AnalysisResults } from "./AnalysisResults";
import { GenerationInfo } from "./generate/GenerationInfo";
import { AddToDeck } from "./slideshow/AddToDeck";
import { TagEditor } from "./TagEditor";

function NameEditor({ image }: { image: Image }) {
  const [value, setValue] = useState(image.name);
  const rename = useRename();
  useEffect(() => setValue(image.name), [image.id, image.name]);
  const save = () => {
    const v = value.trim();
    if (!v || v === image.name) {
      setValue(image.name);
      return;
    }
    rename.mutate(
      { id: image.id, name: v },
      {
        onError: (e) => {
          notifications.show({ color: "red", title: "Rename failed", message: errorMessage(e) });
          setValue(image.name);
        },
      },
    );
  };
  return (
    <TextInput
      label="Name"
      value={value}
      onChange={(e) => setValue(e.currentTarget.value)}
      onBlur={save}
      onKeyDown={(e) => {
        if (e.key === "Enter") e.currentTarget.blur();
        if (e.key === "Escape") {
          setValue(image.name);
          e.stopPropagation();
        }
        e.stopPropagation(); // keep lightbox shortcuts out of the text field
      }}
      disabled={rename.isPending}
    />
  );
}

/** The decks an image is in, with a way to add it to more. */
function ImageDecks({ imageId, decks }: { imageId: number; decks: DeckRef[] }) {
  const qc = useQueryClient();
  const takeOut = async (d: DeckRef) => {
    try {
      await api.post(`/api/decks/${d.id}/remove`, { ids: [imageId] });
      invalidateTopics(qc, ["decks"]);
    } catch (e) {
      notifications.show({ color: "red", message: errorMessage(e) });
    }
  };
  return (
    <div>
      <Text size="sm" fw={500} mb={4}>
        Slide decks
      </Text>
      <Group gap={6}>
        {decks.map((d) => (
          <Badge
            key={d.id}
            variant="light"
            color="gray"
            size="lg"
            style={{ textTransform: "none", fontWeight: 500 }}
            rightSection={<CloseButton size="xs" onClick={() => takeOut(d)} aria-label={`Take out of ${d.name}`} />}
          >
            <Anchor component={Link} to={`/decks/${d.id}`} size="xs" c="inherit">
              {d.name}
            </Anchor>
          </Badge>
        ))}
        <AddToDeck images={{ ids: [imageId] }} count={1}>
          <Button size="compact-xs" variant="subtle" leftSection={<IconPlus size={14} />}>
            {decks.length ? "Add" : "Add to a deck"}
          </Button>
        </AddToDeck>
      </Group>
    </div>
  );
}

/** Metadata, name/tag editing, scores and actions for one image. */
export function ImageInfo({ id, onTrashed }: { id: number; onTrashed?: () => void }) {
  const { data } = useImageDetail(id);
  const trash = useTrash();
  const restore = useRestore();
  if (!data) return null;
  const im = data.image;
  const rows: [string, React.ReactNode][] = [
    ["Dimensions", `${im.width} × ${im.height}`],
    ["File", `${im.format.toUpperCase()} · ${formatBytes(im.size)}`],
    ["Taken", formatTaken(im.takenAt, im.takenOffset)],
    ["Imported", formatDate(im.importedAt)],
    ["File date", formatDate(im.fileMtime)],
    ["Original", im.originalName !== im.name ? im.originalName : "same as name"],
    ["Path", im.originalPath],
    ["ID", `#${im.id} · ${im.uid}`],
    ["SHA-256", im.sha256.slice(0, 16) + "…"],
  ];
  for (const r of data.reencodes ?? []) {
    rows.push([
      "Re-encoded",
      <>
        {formatDate(r.at)}: {formatName(r.oldFormat)} {formatBytes(r.oldSize)}, {r.oldWidth} × {r.oldHeight} →{" "}
        {formatName(r.newFormat)} {formatBytes(r.newSize)}
        {r.newWidth !== r.oldWidth ? `, ${r.newWidth} × ${r.newHeight}` : ""}
        {r.batchId ? (
          <>
            {" "}
            (
            <Anchor component={Link} to={`/reencode/${r.batchId}`} size="xs">
              batch
            </Anchor>
            )
          </>
        ) : null}
      </>,
    ]);
  }
  return (
    <Stack gap="md" p="md">
      <NameEditor image={im} />
      <div>
        <Text size="sm" fw={500} mb={4}>
          Tags
        </Text>
        <TagEditor imageIds={[im.id]} value={im.tags} />
      </div>
      {!im.deletedAt && <ImageDecks imageId={im.id} decks={data.decks ?? []} />}
      <AnalysisResults id={im.id} analyses={data.analyses ?? []} />
      {data.generation && <GenerationInfo g={data.generation} />}
      {im.deletedAt ? (
        <Badge color="orange" variant="light">
          In trash{im.mergedInto ? ` · duplicate of #${im.mergedInto}` : ""}
        </Badge>
      ) : null}
      <Table withRowBorders={false} verticalSpacing={3} fz="xs">
        <Table.Tbody>
          {rows.map(([k, v]) => (
            <Table.Tr key={k}>
              <Table.Td c="dimmed" w={80} style={{ verticalAlign: "top" }}>
                {k}
              </Table.Td>
              <Table.Td style={{ wordBreak: "break-all" }}>{v}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      {data.scores.length > 0 && (
        <div>
          <Title order={6} mb={4}>
            Scores
          </Title>
          <Table withRowBorders={false} verticalSpacing={3} fz="sm">
            <Table.Tbody>
              {data.scores.map((s) => (
                <Table.Tr key={s.metricId}>
                  <Table.Td>
                    <Anchor component={Link} to={`/scoring/metrics/${s.metricId}`} size="sm">
                      {s.metricName}
                    </Anchor>
                  </Table.Td>
                  <Table.Td>{formatScore(s.score, s.stderr)}</Table.Td>
                  <Table.Td c="dimmed">
                    #{s.rank} of {s.of}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </div>
      )}
      <Group gap="xs">
        <Button
          component={Link}
          to={`/?sort=similar&similar=${im.id}`}
          variant="light"
          size="xs"
          leftSection={<IconPhotoSearch size={16} />}
        >
          Find similar
        </Button>
        <Tooltip label="Open original">
          <ActionIcon component="a" href={originalUrl(im.id)} target="_blank" variant="light" size="lg">
            <IconExternalLink size={16} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label="Download original">
          <ActionIcon component="a" href={originalUrl(im.id, true)} variant="light" size="lg">
            <IconDownload size={16} />
          </ActionIcon>
        </Tooltip>
        {im.deletedAt ? (
          <Button size="xs" variant="light" leftSection={<IconRestore size={16} />} onClick={() => restore.mutate([im.id])}>
            Restore
          </Button>
        ) : (
          <Tooltip label="Move to trash">
            <ActionIcon
              variant="light"
              color="red"
              size="lg"
              onClick={() => trash.mutate([im.id], { onSuccess: () => onTrashed?.() })}
            >
              <IconTrash size={16} />
            </ActionIcon>
          </Tooltip>
        )}
      </Group>
    </Stack>
  );
}
