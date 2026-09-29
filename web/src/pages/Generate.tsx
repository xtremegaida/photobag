import { Alert, Badge, Button, Group, ScrollArea, Stack, Tabs, Text, Title } from "@mantine/core";
import { IconFlask, IconPlugConnected, IconSitemap } from "@tabler/icons-react";
import { useSearchParams } from "react-router";
import { useComfySettings } from "../api/generate";
import { ComfyTab } from "../components/generate/ComfyTab";
import { ExperimentsTab } from "../components/generate/ExperimentsTab";
import { WorkflowsTab } from "../components/generate/WorkflowsTab";

/** Image generation with ComfyUI: experiments, workflow templates and the connection. */
export function GeneratePage() {
  const [sp, setSp] = useSearchParams();
  const tab = sp.get("tab") ?? "experiments";
  const setTab = (t: string | null) => {
    const next = new URLSearchParams(sp);
    if (!t || t === "experiments") next.delete("tab");
    else next.set("tab", t);
    setSp(next, { replace: true });
  };
  const { data: settings } = useComfySettings();
  return (
    <ScrollArea style={{ flex: 1 }}>
      <Stack p="md" gap="md" maw={1400}>
        <div>
          <Group gap="sm">
            <Title order={3}>Generate</Title>
            {settings &&
              (settings.endpoint ? (
                <Badge variant="light" color="teal" tt="none">
                  ComfyUI · {settings.endpoint.replace(/^https?:\/\//, "")}
                </Badge>
              ) : (
                <Badge variant="light" color="orange">
                  no ComfyUI connected
                </Badge>
              ))}
          </Group>
          <Text c="dimmed" size="sm" mt={4} maw={760}>
            Run ComfyUI workflows with changed settings, compare the results side by side, and keep the images that work.
          </Text>
        </div>
        {settings && !settings.endpoint && tab !== "comfy" && (
          <Alert color="orange" variant="light">
            <Group justify="space-between">
              <Text size="sm">Set ComfyUI's address before generating.</Text>
              <Button size="xs" variant="light" onClick={() => setTab("comfy")}>
                Connect ComfyUI
              </Button>
            </Group>
          </Alert>
        )}
        <Tabs value={tab} onChange={setTab} keepMounted={false}>
          <Tabs.List mb="md">
            <Tabs.Tab value="experiments" leftSection={<IconFlask size={16} />}>
              Experiments
            </Tabs.Tab>
            <Tabs.Tab value="workflows" leftSection={<IconSitemap size={16} />}>
              Workflows
            </Tabs.Tab>
            <Tabs.Tab value="comfy" leftSection={<IconPlugConnected size={16} />}>
              ComfyUI
            </Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="experiments">
            <ExperimentsTab />
          </Tabs.Panel>
          <Tabs.Panel value="workflows">
            <WorkflowsTab />
          </Tabs.Panel>
          <Tabs.Panel value="comfy">
            <ComfyTab />
          </Tabs.Panel>
        </Tabs>
      </Stack>
    </ScrollArea>
  );
}
