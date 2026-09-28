import { Alert, Badge, Button, Card, Center, Group, Loader, ScrollArea, SimpleGrid, Stack, Table, Text, Title, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconPhoto, IconPlus, IconRefresh } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useParams } from "react-router";
import { api, errorMessage, thumbUrl } from "../api/client";
import { invalidateTopics, useImage, useRankings } from "../api/hooks";
import type { Ranking } from "../api/types";
import { Lightbox } from "../components/Lightbox";
import { formatDate, plural } from "../lib/format";

function Row({ r, max, min, onOpen }: { r: Ranking; max: number; min: number; onOpen: () => void }) {
  const { data: im } = useImage(r.imageId);
  const width = max > min ? ((r.score - min) / (max - min)) * 100 : 50;
  return (
    <Table.Tr style={{ cursor: "pointer" }} onClick={onOpen}>
      <Table.Td w={50} ta="right" fw={600}>
        {r.rank}
      </Table.Td>
      <Table.Td w={72}>
        <img
          src={thumbUrl(r.imageId)}
          alt=""
          loading="lazy"
          style={{ width: 56, height: 56, objectFit: "cover", borderRadius: 6, display: "block" }}
        />
      </Table.Td>
      <Table.Td>
        <Text size="sm" truncate maw={320}>
          {im?.name ?? `#${r.imageId}`}
        </Text>
        <Text size="xs" c="dimmed">
          {im?.tags.join(", ")}
        </Text>
      </Table.Td>
      <Table.Td w={220}>
        <Group gap="xs" wrap="nowrap">
          <Text size="sm" fw={600} w={48} ta="right">
            {Math.round(r.score)}
          </Text>
          <Tooltip label={`standard error ±${Math.round(r.stderr)}${r.approx ? " (approximate)" : ""}`}>
            <Text size="xs" c="dimmed" w={40}>
              ±{Math.round(r.stderr)}
              {r.approx ? "~" : ""}
            </Text>
          </Tooltip>
          <div style={{ flex: 1, height: 6, borderRadius: 3, background: "var(--mantine-color-default-border)" }}>
            <div style={{ width: `${width}%`, height: 6, borderRadius: 3, background: "var(--mantine-color-teal-6)" }} />
          </div>
        </Group>
      </Table.Td>
      <Table.Td w={90} ta="center">
        <Tooltip label="Net wins–losses after contradicting answers cancel">
          <Text size="sm">
            {r.wins}–{r.losses}
          </Text>
        </Tooltip>
      </Table.Td>
      <Table.Td w={90} ta="center">
        <Text size="sm" c="dimmed">
          {r.rawWins}–{r.rawLosses}
        </Text>
      </Table.Td>
    </Table.Tr>
  );
}

export function RankingsPage() {
  const metricId = Number(useParams().id);
  const { data, error, isLoading, isFetching } = useRankings(metricId);
  const qc = useQueryClient();
  const [open, setOpen] = useState<number | null>(null);
  const ids = useMemo(() => data?.rankings.map((r) => r.imageId) ?? [], [data]);
  const recalc = async () => {
    try {
      await api.post(`/api/metrics/${metricId}/recalc`);
      invalidateTopics(qc, ["metrics"]);
    } catch (e) {
      notifications.show({ color: "red", message: errorMessage(e) });
    }
  };
  if (error)
    return (
      <Alert m="md" color="red">
        {errorMessage(error)}
      </Alert>
    );
  if (isLoading || !data)
    return (
      <Center style={{ flex: 1 }}>
        <Loader />
      </Center>
    );
  const { metric, rankings } = data;
  const max = rankings[0]?.score ?? 0;
  const min = rankings[rankings.length - 1]?.score ?? 0;
  return (
    <ScrollArea style={{ flex: 1 }}>
      <Stack p="md" gap="md" maw={1100}>
        <Group justify="space-between" align="flex-start">
          <div>
            <Group gap="xs">
              <Title order={3}>{metric.name}</Title>
              {isFetching && <Loader size="xs" />}
            </Group>
            {metric.description && (
              <Text c="dimmed" size="sm">
                {metric.description}
              </Text>
            )}
          </div>
          <Group gap="xs">
            <Button variant="default" leftSection={<IconPhoto size={16} />} component={Link} to={`/?sort=score&metric=${metric.id}`}>
              Open in library
            </Button>
            <Button variant="default" leftSection={<IconRefresh size={16} />} onClick={recalc}>
              Recalculate
            </Button>
            <Button leftSection={<IconPlus size={16} />} component={Link} to="/scoring">
              New run
            </Button>
          </Group>
        </Group>
        <Card withBorder>
          <SimpleGrid cols={{ base: 2, sm: 5 }}>
            {[
              ["Ranked images", metric.ranked],
              ["Comparisons", metric.comparisons],
              ["Distinct pairs", metric.pairCount],
              ["Cancelled pairs", metric.cancelledPairs],
              ["Runs", metric.runs],
            ].map(([k, v]) => (
              <div key={k}>
                <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
                  {k}
                </Text>
                <Text fw={600}>{Number(v).toLocaleString()}</Text>
              </div>
            ))}
          </SimpleGrid>
          <Text size="xs" c="dimmed" mt="sm">
            Scores are on an Elo-like scale (1000 = average; +200 ≈ 76% chance of being preferred), fitted with a
            Bradley–Terry model over every run. Answers for the same pair are netted, so contradicting answers cancel.
            {metric.scoredAt ? ` Computed ${formatDate(metric.scoredAt)}.` : ""}
          </Text>
        </Card>
        {rankings.length === 0 ? (
          <Text c="dimmed">No comparisons yet for this metric.</Text>
        ) : (
          <Table highlightOnHover verticalSpacing={4}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th ta="right">#</Table.Th>
                <Table.Th />
                <Table.Th>Image</Table.Th>
                <Table.Th>Score</Table.Th>
                <Table.Th ta="center">
                  <Tooltip label="After cancellation">
                    <span>Net W–L</span>
                  </Tooltip>
                </Table.Th>
                <Table.Th ta="center">All W–L</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rankings.map((r, i) => (
                <Row key={r.imageId} r={r} max={max} min={min} onOpen={() => setOpen(i)} />
              ))}
            </Table.Tbody>
          </Table>
        )}
        {rankings.length > 0 && (
          <Badge variant="light" color="gray">
            {plural(rankings.length, "image")} ranked
          </Badge>
        )}
      </Stack>
      <Lightbox ids={ids} index={open} onIndex={setOpen} onClose={() => setOpen(null)} />
    </ScrollArea>
  );
}
