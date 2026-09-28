import { Alert, Button, Center, Group, Kbd, Loader, Progress, Stack, Text, Title, Tooltip } from "@mantine/core";
import { useHotkeys } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconArrowBackUp, IconArrowLeft, IconPlayerPause, IconPlayerSkipForward, IconTrophy } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { ApiError, api, errorMessage, preload, previewUrl } from "../api/client";
import { invalidateTopics, qk, useImage, usePair } from "../api/hooks";
import type { Pair } from "../api/types";
import classes from "./Compare.module.css";

const SIZE = window.innerWidth > 2200 ? 2560 : 1600;

function Side({
  id,
  keyHint,
  chosen,
  busy,
  onChoose,
}: {
  id: number;
  keyHint: string;
  chosen: boolean;
  busy: boolean;
  onChoose: () => void;
}) {
  const { data: im } = useImage(id);
  return (
    <div className={classes.side} data-chosen={chosen || undefined} data-busy={busy || undefined} onClick={() => !busy && onChoose()}>
      <div className={classes.imgwrap}>
        <img key={id} src={previewUrl(id, SIZE)} alt={im?.name ?? ""} draggable={false} />
      </div>
      <div className={classes.label}>
        <Text size="sm" truncate>
          {im ? `${im.name} · ${im.width}×${im.height}` : ""}
        </Text>
        <Kbd>{keyHint}</Kbd>
      </div>
    </div>
  );
}

export function ComparePage() {
  const runId = Number(useParams().id);
  const { data: pair, error, isLoading } = usePair(runId);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [chosen, setChosen] = useState<number | null>(null);

  useEffect(() => {
    for (const p of pair?.upcoming ?? []) {
      preload(previewUrl(p.left, SIZE));
      preload(previewUrl(p.right, SIZE));
    }
  }, [pair]);

  const call = async (fn: () => Promise<Pair>) => {
    if (busy) return;
    setBusy(true);
    try {
      const next = await fn();
      qc.setQueryData(qk.pair(runId), next);
      if (next.done) invalidateTopics(qc, ["runs", "metrics"]);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        await qc.invalidateQueries({ queryKey: qk.pair(runId) }); // answered elsewhere; resync
      } else {
        notifications.show({ color: "red", message: errorMessage(e) });
      }
    } finally {
      setBusy(false);
      setChosen(null);
    }
  };
  const answer = (winner: number) => {
    if (!pair || pair.done) return;
    setChosen(winner);
    call(() => api.post<Pair>(`/api/runs/${runId}/answer`, { pos: pair.pos, winner }));
  };
  const skip = () => pair && !pair.done && call(() => api.post<Pair>(`/api/runs/${runId}/answer`, { pos: pair.pos, winner: 0 }));
  const undo = () => call(() => api.post<Pair>(`/api/runs/${runId}/undo`));
  const stop = async () => {
    if (pair && !pair.done) {
      await api.post(`/api/runs/${runId}/status`, { status: "stopped" }).catch(() => undefined);
    }
    invalidateTopics(qc, ["runs", "metrics"]);
    navigate("/scoring");
  };

  useHotkeys([
    ["ArrowLeft", () => pair && answer(pair.left!)],
    ["ArrowRight", () => pair && answer(pair.right!)],
    ["ArrowDown", skip],
    ["Space", skip],
    ["Backspace", undo],
    ["U", undo],
    ["Escape", stop],
  ]);

  if (error) {
    return (
      <Alert m="md" color="red" title="Run unavailable">
        {errorMessage(error)}
      </Alert>
    );
  }
  if (isLoading || !pair) {
    return (
      <Center style={{ flex: 1 }}>
        <Loader />
      </Center>
    );
  }
  const run = pair.run;
  const answered = run.compared + run.skipped;
  return (
    <div className={classes.root}>
      <Group px="md" py="xs" justify="space-between" wrap="nowrap">
        <Group gap="sm" wrap="nowrap">
          <Button variant="subtle" leftSection={<IconArrowLeft size={16} />} onClick={stop} size="xs">
            Scoring
          </Button>
          <div>
            <Title order={4}>
              Which is better for <Text span c="teal" inherit>{run.metricName}</Text>?
            </Title>
            <Text size="xs" c="dimmed">
              {run.description} · pair {Math.min(run.position + 1, run.totalPairs).toLocaleString()} of{" "}
              {run.totalPairs.toLocaleString()} · {run.compared} answered
            </Text>
          </div>
        </Group>
        <Group gap="xs" wrap="nowrap">
          <Tooltip label="Undo the last answer (Backspace)">
            <Button size="xs" variant="default" leftSection={<IconArrowBackUp size={16} />} onClick={undo} disabled={busy || answered === 0}>
              Undo
            </Button>
          </Tooltip>
          <Tooltip label="Skip this pair (↓ or Space)">
            <Button size="xs" variant="default" leftSection={<IconPlayerSkipForward size={16} />} onClick={skip} disabled={busy || pair.done}>
              Skip
            </Button>
          </Tooltip>
          <Tooltip label="Stop for now (Esc) — resume any time">
            <Button size="xs" variant="light" leftSection={<IconPlayerPause size={16} />} onClick={stop}>
              Stop
            </Button>
          </Tooltip>
        </Group>
      </Group>
      <Progress value={(run.position / Math.max(1, run.totalPairs)) * 100} size="xs" radius={0} />
      {pair.done ? (
        <Center style={{ flex: 1 }}>
          <Stack align="center">
            <IconTrophy size={48} color="var(--mantine-color-teal-5)" />
            <Title order={3}>Run complete</Title>
            <Text c="dimmed">
              All {run.totalPairs.toLocaleString()} pairs done ({run.compared} answered, {run.skipped} skipped).
            </Text>
            <Group>
              <Button component={Link} to={`/scoring/metrics/${run.metricId}`} leftSection={<IconTrophy size={16} />}>
                See rankings
              </Button>
              <Button variant="default" onClick={undo} disabled={busy}>
                Undo last answer
              </Button>
            </Group>
          </Stack>
        </Center>
      ) : (
        <div className={classes.pair}>
          <Side id={pair.left!} keyHint="←" chosen={chosen === pair.left} busy={busy} onChoose={() => answer(pair.left!)} />
          <Side id={pair.right!} keyHint="→" chosen={chosen === pair.right} busy={busy} onChoose={() => answer(pair.right!)} />
        </div>
      )}
    </div>
  );
}
