import { ActionIcon, Anchor, Badge, Button, Group, Stack, Table, Text, TextInput, Title, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDownload, IconExternalLink, IconPhotoSearch, IconRestore, IconTrash } from "@tabler/icons-react";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { errorMessage, originalUrl } from "../api/client";
import { useImageDetail, useRename, useRestore, useTrash } from "../api/hooks";
import type { Image } from "../api/types";
import { formatBytes, formatDate, formatScore, formatTaken } from "../lib/format";
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
  return (
    <Stack gap="md" p="md">
      <NameEditor image={im} />
      <div>
        <Text size="sm" fw={500} mb={4}>
          Tags
        </Text>
        <TagEditor imageIds={[im.id]} value={im.tags} />
      </div>
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
