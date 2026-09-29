import {
  ActionIcon,
  Anchor,
  Badge,
  Button,
  Group,
  Popover,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
  Tooltip,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconArrowBackUp,
  IconDeviceFloppy,
  IconDice5,
  IconDownload,
  IconFileCode,
  IconFolderPlus,
  IconTrash,
} from "@tabler/icons-react";
import { useState } from "react";
import { Link } from "react-router";
import { errorMessage } from "../../api/client";
import {
  generationOriginal,
  generationWorkflowUrl,
  useDiscardGenerations,
  useSaveGenerationWorkflow,
} from "../../api/generate";
import type { GenerateRequest, GenerationDetail } from "../../api/types";
import { formatBytes, formatDate } from "../../lib/format";
import { formatValue, inputLabel, isSeedName, requestFrom } from "../../lib/generate";
import { formatDuration } from "../../lib/pipelines";
import { Confirm } from "../Confirm";
import { useCreateExperiment } from "./ExperimentsTab";
import { MoveDialog } from "./MoveDialog";

function SaveWorkflow({ g }: { g: GenerationDetail }) {
  const [opened, setOpened] = useState(false);
  const [name, setName] = useState("");
  const save = useSaveGenerationWorkflow();
  const submit = () =>
    save.mutate(
      { id: g.id, name },
      {
        onSuccess: (d) => {
          notifications.show({ message: `Saved as the workflow “${d.workflow.name}”` });
          setOpened(false);
        },
        onError: (e) => notifications.show({ color: "red", title: "Not saved", message: errorMessage(e) }),
      },
    );
  return (
    <Popover opened={opened} onChange={setOpened} position="bottom-start" withArrow trapFocus>
      <Popover.Target>
        <Button
          size="xs"
          variant="light"
          leftSection={<IconDeviceFloppy size={16} />}
          onClick={() => {
            setName(`${g.workflowName} · ${g.experimentName || "image"} ${g.id}`);
            setOpened((o) => !o);
          }}
        >
          Save as a workflow
        </Button>
      </Popover.Target>
      <Popover.Dropdown>
        <Stack gap="xs" w={300}>
          <Text size="xs" c="dimmed">
            The workflow exactly as this image was made, with its overrides and seed set.
          </Text>
          <TextInput
            size="xs"
            label="Name"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            onKeyDown={(e) => {
              e.stopPropagation();
              if (e.key === "Enter" && name.trim()) submit();
            }}
          />
          <Button size="xs" onClick={submit} loading={save.isPending} disabled={!name.trim()}>
            Save
          </Button>
        </Stack>
      </Popover.Dropdown>
    </Popover>
  );
}

/**
 * How an image was generated, with actions: make more like it, save its
 * workflow, and (while it is held in its experiment) move or delete it.
 */
export function GenerationInfo({
  g,
  onUse,
  onGone,
  count = 1,
}: {
  g: GenerationDetail;
  /** Loads settings into the experiment's form (omit outside an experiment). */
  onUse?: (r: GenerateRequest) => void;
  /** Called after the image was moved or deleted. */
  onGone?: () => void;
  count?: number;
}) {
  const [moving, setMoving] = useState<number[] | null>(null);
  const [deleting, setDeleting] = useState(false);
  const discard = useDiscardGenerations();
  const create = useCreateExperiment();
  const held = !g.imageId;
  const values = [...g.applied].sort((a, b) => (a.kind === b.kind ? 0 : a.kind === "sweep" ? -1 : b.kind === "sweep" ? 1 : 0));
  const rows: [string, React.ReactNode][] = [
    [
      "Workflow",
      <>
        {g.workflowName}
        {!g.templateExists ? " (template deleted)" : !g.current ? " (an earlier version)" : ""}
      </>,
    ],
    ["Size", `${g.width} × ${g.height} · ${g.format.toUpperCase()} · ${formatBytes(g.size)}`],
    ["Made", `${formatDate(g.createdAt)}${g.millis ? ` in ${formatDuration(g.millis)}` : ""}`],
  ];
  if (g.seed !== undefined) rows.push(["Seed", String(g.seed)]);
  if (g.experimentName) rows.push(["Experiment", g.experimentName]);
  const use = (withSeed: boolean) => onUse?.(requestFrom(g, withSeed, count));
  const seeded = g.seed !== undefined || g.applied.some((a) => isSeedName(a.input));
  const resumeLink = (withSeed: boolean) => `/generate/${g.experimentId}?use=${g.id}${withSeed ? "&seed=1" : ""}`;
  return (
    <Stack gap="sm">
      <Group gap="xs">
        <Title order={5}>Generated image #{g.id}</Title>
        {held ? (
          <Badge variant="light" color="grape">
            in the experiment
          </Badge>
        ) : (
          <Badge variant="light" color="teal">
            in the library
          </Badge>
        )}
      </Group>
      <div>
        <Text size="sm" fw={500} mb={4}>
          Values set on the workflow
        </Text>
        {values.length === 0 ? (
          <Text size="xs" c="dimmed">
            None: the workflow as it was.
          </Text>
        ) : (
          <Table withRowBorders={false} verticalSpacing={2} fz="xs">
            <Table.Tbody>
              {values.map((a) => (
                <Table.Tr key={a.node + a.input}>
                  <Table.Td c="dimmed" style={{ verticalAlign: "top", whiteSpace: "nowrap" }}>
                    {inputLabel(a)}
                  </Table.Td>
                  <Table.Td style={{ wordBreak: "break-word" }}>
                    {formatValue(a.value, 400)}
                    {a.kind && (
                      <Badge size="xs" variant="light" color={a.kind === "sweep" ? "blue" : "gray"} ml={6}>
                        {a.kind === "sweep" ? "swept" : "seed"}
                      </Badge>
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </div>
      <Table withRowBorders={false} verticalSpacing={2} fz="xs">
        <Table.Tbody>
          {rows.map(([k, v]) => (
            <Table.Tr key={k}>
              <Table.Td c="dimmed" w={80} style={{ verticalAlign: "top" }}>
                {k}
              </Table.Td>
              <Table.Td>{v}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      <Group gap="xs">
        {onUse ? (
          <>
            <Tooltip label="Put these values in the form; new images get new seeds">
              <Button size="xs" variant="light" leftSection={<IconArrowBackUp size={16} />} onClick={() => use(false)}>
                Use these settings
              </Button>
            </Tooltip>
            {seeded && (
              <Tooltip label="The same, keeping this seed: change a value to see its effect on this image">
                <Button size="xs" variant="light" leftSection={<IconDice5 size={16} />} onClick={() => use(true)}>
                  …and this seed
                </Button>
              </Tooltip>
            )}
          </>
        ) : g.experimentId ? (
          <>
            <Button size="xs" variant="light" component={Link} to={resumeLink(false)} leftSection={<IconArrowBackUp size={16} />}>
              Continue the experiment
            </Button>
            {seeded && (
              <Button size="xs" variant="light" component={Link} to={resumeLink(true)} leftSection={<IconDice5 size={16} />}>
                …with this seed
              </Button>
            )}
          </>
        ) : (
          <Tooltip label="Its experiment was deleted: start a new one with these settings">
            <Button
              size="xs"
              variant="light"
              leftSection={<IconArrowBackUp size={16} />}
              onClick={() => create(`More like ${g.workflowName} #${g.id}`, requestFrom(g, false, 1))}
            >
              New experiment from this
            </Button>
          </Tooltip>
        )}
        <SaveWorkflow g={g} />
      </Group>
      <Group gap="xs">
        <Tooltip label="Download the workflow as it made this image (API format)">
          <ActionIcon component="a" href={generationWorkflowUrl(g.id)} variant="light" size="lg" aria-label="Download workflow">
            <IconFileCode size={16} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label="Download the image (PNGs carry the workflow: drop them on ComfyUI)">
          <ActionIcon component="a" href={generationOriginal(g.id, true)} variant="light" size="lg" aria-label="Download image">
            <IconDownload size={16} />
          </ActionIcon>
        </Tooltip>
        {held && (
          <>
            <Button size="xs" leftSection={<IconFolderPlus size={16} />} onClick={() => setMoving([g.id])}>
              Move to the library
            </Button>
            <Tooltip label="Delete">
              <ActionIcon variant="light" color="red" size="lg" onClick={() => setDeleting(true)} aria-label="Delete">
                <IconTrash size={16} />
              </ActionIcon>
            </Tooltip>
          </>
        )}
        {!held && onUse && (
          <Anchor component={Link} to={`/?id=${g.imageId}`} size="xs">
            Show in the library
          </Anchor>
        )}
      </Group>
      <MoveDialog ids={moving} onClose={() => setMoving(null)} onMoved={onGone} />
      <Confirm
        opened={deleting}
        title="Delete this image?"
        confirm="Delete"
        loading={discard.isPending}
        onClose={() => setDeleting(false)}
        onConfirm={() =>
          discard.mutate([g.id], {
            onSuccess: () => {
              setDeleting(false);
              onGone?.();
            },
            onError: (e) => notifications.show({ color: "red", title: "Not deleted", message: errorMessage(e) }),
          })
        }
      >
        <Text size="sm">It is not in the library, so it is gone for good (its settings can still make it again).</Text>
      </Confirm>
    </Stack>
  );
}
