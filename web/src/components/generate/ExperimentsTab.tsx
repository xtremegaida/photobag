import { Alert, Badge, Button, Card, Group, Image, Loader, Modal, SimpleGrid, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconFlask, IconPlus } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { api, errorMessage, shaThumbUrl } from "../../api/client";
import { useExperiments } from "../../api/generate";
import { invalidateTopics } from "../../api/hooks";
import type { ExperimentView, GenerateRequest } from "../../api/types";
import { formatDate, plural } from "../../lib/format";

/** Creates an experiment (optionally starting from a request) and opens it. */
export function useCreateExperiment() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  return async (name: string, request?: GenerateRequest) => {
    try {
      const e = await api.post<ExperimentView>("/api/experiments", { name, request });
      invalidateTopics(qc, ["experiments"]);
      navigate(`/generate/${e.id}`);
    } catch (err) {
      notifications.show({ color: "red", title: "Could not create the experiment", message: errorMessage(err) });
    }
  };
}

function NewExperiment({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const [name, setName] = useState("");
  const create = useCreateExperiment();
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    setBusy(true);
    await create(name);
    setBusy(false);
  };
  return (
    <Modal opened={opened} onClose={onClose} title="New experiment">
      <Stack>
        <TextInput
          label="Name"
          description="Images moved to the library are named after it"
          placeholder="Sampler comparison"
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
          onKeyDown={(e) => e.key === "Enter" && name.trim() && submit()}
          data-autofocus
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!name.trim()} loading={busy}>
            Create
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function Covers({ shas }: { shas: string[] }) {
  if (shas.length === 0) {
    return (
      <Group h={120} justify="center" bg="var(--mantine-color-default-hover)">
        <IconFlask size={36} color="var(--mantine-color-dimmed)" stroke={1.2} />
      </Group>
    );
  }
  return (
    <SimpleGrid cols={shas.length === 1 ? 1 : 2} spacing={2} h={120} style={{ overflow: "hidden" }}>
      {shas.slice(0, shas.length === 3 ? 2 : 4).map((s) => (
        <Image key={s} src={shaThumbUrl(s)} h={shas.length > 2 ? 59 : 120} fit="cover" alt="" />
      ))}
    </SimpleGrid>
  );
}

/** The experiments of the bag. */
export function ExperimentsTab() {
  const { data, error, isLoading } = useExperiments();
  const [creating, setCreating] = useState(false);
  return (
    <Stack gap="md">
      <Group justify="space-between">
        <Text size="sm" c="dimmed" maw={720}>
          An experiment holds generated images apart from the library. Run a workflow with overrides a number of times, or
          sweep over values to compare them; then move the good images to the library and delete the rest.
        </Text>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
          New experiment
        </Button>
      </Group>
      {error && <Alert color="red">{errorMessage(error)}</Alert>}
      {isLoading && <Loader />}
      {data && data.length === 0 && (
        <Alert color="gray" variant="light">
          No experiments yet.
        </Alert>
      )}
      <SimpleGrid cols={{ base: 1, sm: 2, md: 3, xl: 4 }}>
        {data?.map((e) => (
          <Card key={e.id} withBorder padding="sm" component={Link} to={`/generate/${e.id}`}>
            <Card.Section>
              <Covers shas={e.covers} />
            </Card.Section>
            <Group justify="space-between" mt="sm" wrap="nowrap">
              <Text fw={600} truncate>
                {e.name}
              </Text>
              {e.job && (
                <Badge color="blue" variant="light" leftSection={<Loader size={10} color="blue" />}>
                  generating
                </Badge>
              )}
            </Group>
            <Text size="xs" c="dimmed">
              {plural(e.held, "image")} here{e.moved ? ` · ${e.moved} moved to the library` : ""} · {plural(e.runs, "run")}
            </Text>
            <Text size="xs" c="dimmed">
              {formatDate(e.updatedAt)}
            </Text>
          </Card>
        ))}
      </SimpleGrid>
      <NewExperiment opened={creating} onClose={() => setCreating(false)} />
    </Stack>
  );
}
