import { Alert, Badge, Button, Center, Group, Loader, Paper, ScrollArea, Stack, Tabs, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDeviceFloppy, IconListDetails, IconPlayerPlay, IconPlugConnected } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { api, errorMessage } from "../api/client";
import { qk, useAnalysisSettings } from "../api/hooks";
import type { AnalysisSettingsView } from "../api/types";
import { ConnectionTab } from "../components/analysis/ConnectionTab";
import { PipelinesTab } from "../components/analysis/PipelinesTab";
import { RunTab } from "../components/analysis/RunTab";
import { useAnalysisDraft } from "../stores/analysisDraft";

/** Model analysis: run pipelines on images, and configure the model and pipelines. */
export function AnalysisPage() {
  const [sp, setSp] = useSearchParams();
  const tab = sp.get("tab") ?? "run";
  const setTab = (t: string | null) => {
    const next = new URLSearchParams(sp);
    if (!t || t === "run") next.delete("tab");
    else next.set("tab", t);
    setSp(next, { replace: true });
  };
  const { data: view, error } = useAnalysisSettings();
  const { draft, apiKey, setDraft, patch: change, setApiKey } = useAnalysisDraft();
  const [saving, setSaving] = useState(false);
  const qc = useQueryClient();

  useEffect(() => {
    if (view && !draft) setDraft(view.settings);
  }, [view, draft, setDraft]);

  const dirty = !!view && !!draft && (JSON.stringify(view.settings) !== JSON.stringify(draft) || apiKey !== undefined);
  // Warn before closing the tab with unsaved settings.
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const save = async () => {
    if (!draft) return;
    setSaving(true);
    try {
      const v = await api.put<AnalysisSettingsView>("/api/analysis/settings", { settings: draft, apiKey });
      qc.setQueryData(qk.analysisSettings, v);
      qc.invalidateQueries({ queryKey: ["analysis-plan"] });
      setDraft(v.settings);
      setApiKey(undefined);
      notifications.show({ message: "Analysis settings saved" });
    } catch (e) {
      notifications.show({ color: "red", title: "Settings not saved", message: errorMessage(e) });
    } finally {
      setSaving(false);
    }
  };
  const discard = () => {
    if (view) setDraft(view.settings);
    setApiKey(undefined);
  };

  const configured = !!view?.settings.endpoint;
  return (
    <Stack gap={0} style={{ flex: 1, minHeight: 0 }}>
      <ScrollArea style={{ flex: 1 }}>
        <Stack p="md" gap="md" maw={1400}>
          <div>
            <Group gap="sm">
              <Title order={3}>Analysis</Title>
              {view &&
                (configured ? (
                  <Badge variant="light" color="teal" tt="none">
                    {view.settings.model || "default model"} · {view.settings.endpoint}
                  </Badge>
                ) : (
                  <Badge variant="light" color="orange">
                    not connected
                  </Badge>
                ))}
            </Group>
            <Text c="dimmed" size="sm" mt={4} maw={760}>
              A vision model describes your images: captions for people who cannot see them, the text they contain,
              Danbooru tags and categories. Results are stored in the bag, can be searched from the library, and
              categories (and optionally Danbooru tags) become tags.
            </Text>
          </div>
          {error && <Alert color="red">{errorMessage(error)}</Alert>}
          {!view || !draft ? (
            <Center p="xl">
              <Loader />
            </Center>
          ) : (
            <Tabs value={tab} onChange={setTab} keepMounted={false}>
              <Tabs.List mb="md">
                <Tabs.Tab value="run" leftSection={<IconPlayerPlay size={16} />}>
                  Run
                </Tabs.Tab>
                <Tabs.Tab value="connection" leftSection={<IconPlugConnected size={16} />}>
                  Connection
                </Tabs.Tab>
                <Tabs.Tab value="pipelines" leftSection={<IconListDetails size={16} />}>
                  Pipelines &amp; prompts
                </Tabs.Tab>
              </Tabs.List>
              <Tabs.Panel value="run">
                <RunTab configured={configured} onSetup={() => setTab("connection")} />
              </Tabs.Panel>
              <Tabs.Panel value="connection">
                <ConnectionTab view={view} draft={draft} onChange={change} apiKey={apiKey} onApiKey={setApiKey} />
              </Tabs.Panel>
              <Tabs.Panel value="pipelines">
                <PipelinesTab view={view} draft={draft} onChange={change} apiKey={apiKey} dirty={dirty} />
              </Tabs.Panel>
            </Tabs>
          )}
        </Stack>
      </ScrollArea>
      {dirty && (
        <Paper withBorder shadow="md" px="md" py="xs" radius={0} style={{ borderLeft: 0, borderRight: 0, borderBottom: 0 }}>
          <Group justify="space-between">
            <Text size="sm" fw={500}>
              Unsaved analysis settings
            </Text>
            <Group gap="xs">
              <Button size="xs" variant="default" onClick={discard} disabled={saving}>
                Discard
              </Button>
              <Button size="xs" leftSection={<IconDeviceFloppy size={16} />} onClick={save} loading={saving}>
                Save
              </Button>
            </Group>
          </Group>
        </Paper>
      )}
    </Stack>
  );
}
