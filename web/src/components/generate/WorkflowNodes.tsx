import { Badge, Group, Table, Text, Tooltip } from "@mantine/core";
import type { WorkflowNode } from "../../api/types";
import { formatValue } from "../../lib/generate";

export function OutputBadge({ output }: { output: string }) {
  if (output === "websocket")
    return (
      <Badge color="teal" variant="light" tt="none">
        websocket
      </Badge>
    );
  if (output === "file")
    return (
      <Tooltip label="Images are saved by ComfyUI and downloaded; a Send Image (WebSocket) node skips the disk">
        <Badge color="blue" variant="light" tt="none">
          Save Image
        </Badge>
      </Tooltip>
    );
  return (
    <Badge color="red" variant="light" tt="none">
      no image output
    </Badge>
  );
}

/** The nodes of a workflow and the values that can be overridden. */
export function WorkflowNodes({ nodes }: { nodes: WorkflowNode[] }) {
  return (
    <Table verticalSpacing={4} fz="sm" striped withTableBorder>
      <Table.Thead>
        <Table.Tr>
          <Table.Th w={70}>Node</Table.Th>
          <Table.Th w={200}>Title</Table.Th>
          <Table.Th>Values that can be overridden</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {nodes.map((n) => (
          <Table.Tr key={n.id}>
            <Table.Td c="dimmed">#{n.id}</Table.Td>
            <Table.Td>
              <Group gap={6} wrap="nowrap">
                <Text size="sm" fw={500}>
                  {n.title}
                </Text>
                {n.output && <OutputBadge output={n.output} />}
              </Group>
              {n.title !== n.classType && (
                <Text size="xs" c="dimmed">
                  {n.classType}
                </Text>
              )}
              {n.ref !== n.title && (
                <Text size="xs" c="orange">
                  shared title: named {n.ref}
                </Text>
              )}
            </Table.Td>
            <Table.Td>
              {n.inputs.length === 0 ? (
                <Text size="xs" c="dimmed">
                  –
                </Text>
              ) : (
                <Text size="xs" style={{ wordBreak: "break-word" }}>
                  {n.inputs.map((i, k) => (
                    <span key={i.name}>
                      {k > 0 && " · "}
                      <Text span c="dimmed" size="xs">
                        {i.name}
                      </Text>{" "}
                      {formatValue(i.value, 50)}
                      {i.seed && (
                        <Text span size="xs" c="grape">
                          {" "}
                          (seed)
                        </Text>
                      )}
                    </span>
                  ))}
                </Text>
              )}
            </Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  );
}
