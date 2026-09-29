import { Alert, Anchor, Badge, Button, Code, Group, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDeviceFloppy, IconPlugConnected } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../../api/client";
import { gk, useComfySettings } from "../../api/generate";
import type { ComfyCheck, ComfySettings } from "../../api/types";
import { formatBytes } from "../../lib/format";

/** The ComfyUI server's address, with a connection check. */
export function ComfyTab() {
  const { data: settings } = useComfySettings();
  const [endpoint, setEndpoint] = useState("");
  const qc = useQueryClient();
  useEffect(() => {
    if (settings) setEndpoint(settings.endpoint);
  }, [settings]);
  const check = useMutation({ mutationFn: () => api.post<ComfyCheck>("/api/comfy/check", { endpoint }) });
  const save = useMutation({
    mutationFn: () => api.put<ComfySettings>("/api/comfy/settings", { endpoint }),
    onSuccess: (s) => {
      qc.setQueryData(gk.settings, s);
      setEndpoint(s.endpoint);
      notifications.show({ message: "ComfyUI address saved" });
      if (s.endpoint) check.mutate();
    },
    onError: (e) => notifications.show({ color: "red", title: "Not saved", message: errorMessage(e) }),
  });
  const dirty = !!settings && endpoint.trim() !== settings.endpoint;
  const info = check.data?.info;
  return (
    <Stack gap="md" maw={760}>
      <Text size="sm" c="dimmed">
        PhotoBag queues workflows on a ComfyUI server and receives the images over its websocket. Start ComfyUI with{" "}
        <Code>--listen</Code> when it runs on another computer.
      </Text>
      <Group align="flex-end" gap="xs">
        <TextInput
          label="ComfyUI address"
          description="host:port, as in ComfyUI's address bar"
          placeholder="127.0.0.1:8188"
          value={endpoint}
          onChange={(e) => {
            setEndpoint(e.currentTarget.value);
            check.reset();
          }}
          onKeyDown={(e) => e.key === "Enter" && dirty && save.mutate()}
          w={360}
        />
        <Button
          variant="light"
          leftSection={<IconPlugConnected size={16} />}
          onClick={() => check.mutate()}
          loading={check.isPending}
          disabled={!endpoint.trim()}
        >
          Check
        </Button>
        <Button leftSection={<IconDeviceFloppy size={16} />} onClick={() => save.mutate()} loading={save.isPending} disabled={!dirty}>
          Save
        </Button>
      </Group>
      {check.error && <Alert color="red">{errorMessage(check.error)}</Alert>}
      {check.data && (
        <Alert color={check.data.ok ? "teal" : "red"} variant="light" title={check.data.message}>
          {info && (
            <Stack gap="xs">
              <Table withRowBorders={false} verticalSpacing={2} fz="sm" w="auto">
                <Table.Tbody>
                  <Table.Tr>
                    <Table.Td c="dimmed">Software</Table.Td>
                    <Table.Td>
                      ComfyUI {info.version} · Python {info.python} · PyTorch {info.pytorch} · {info.os}
                    </Table.Td>
                  </Table.Tr>
                  {info.devices.map((d) => (
                    <Table.Tr key={d.name}>
                      <Table.Td c="dimmed">Device</Table.Td>
                      <Table.Td>
                        {d.name}
                        {d.vramTotal > 0 && ` · ${formatBytes(d.vramFree)} of ${formatBytes(d.vramTotal)} free`}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                  <Table.Tr>
                    <Table.Td c="dimmed">Queue</Table.Td>
                    <Table.Td>{info.queue ? `${info.queue} prompt(s) running or waiting` : "idle"}</Table.Td>
                  </Table.Tr>
                  <Table.Tr>
                    <Table.Td c="dimmed">Websocket node</Table.Td>
                    <Table.Td>
                      {info.websocketNode ? (
                        <Badge color="teal" variant="light" tt="none">
                          Send Image (WebSocket) is installed
                        </Badge>
                      ) : (
                        <Badge color="orange" variant="light" tt="none">
                          not installed
                        </Badge>
                      )}
                    </Table.Td>
                  </Table.Tr>
                </Table.Tbody>
              </Table>
              {!info.websocketNode && (
                <Text size="sm">
                  Workflows ending in a “Send Image (WebSocket)” node hand their images straight to PhotoBag. It comes with{" "}
                  <Anchor href="https://github.com/Acly/comfyui-tooling-nodes" target="_blank" size="sm">
                    comfyui-tooling-nodes
                  </Anchor>{" "}
                  (installable from ComfyUI's manager). Without it, workflows can end in “Save Image”: PhotoBag then
                  downloads what ComfyUI saved.
                </Text>
              )}
            </Stack>
          )}
        </Alert>
      )}
    </Stack>
  );
}
