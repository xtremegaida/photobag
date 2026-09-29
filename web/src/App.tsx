import {
  ActionIcon,
  AppShell,
  Badge,
  Burger,
  Group,
  NavLink,
  ScrollArea,
  Text,
  Tooltip,
  useMantineColorScheme,
} from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import {
  IconArchive,
  IconChartBar,
  IconCopy,
  IconDownload,
  IconFolderUp,
  IconListCheck,
  IconMoon,
  IconPhoto,
  IconSparkles,
  IconSun,
  IconTags,
  IconTrash,
  IconUpload,
  IconWand,
} from "@tabler/icons-react";
import { Navigate, NavLink as RouterNavLink, Route, Routes, useLocation } from "react-router";
import { useServerEvents } from "./api/events";
import { useStats } from "./api/hooks";
import { JobIndicator } from "./components/JobIndicator";
import { formatBytes, plural } from "./lib/format";
import { AnalysisPage } from "./pages/Analysis";
import { BackupPage } from "./pages/Backup";
import { ComparePage } from "./pages/Compare";
import { DedupPage } from "./pages/Dedup";
import { ExperimentPage } from "./pages/Experiment";
import { ExportPage } from "./pages/Export";
import { GalleryPage } from "./pages/Gallery";
import { GeneratePage } from "./pages/Generate";
import { ImportPage } from "./pages/Import";
import { JobsPage } from "./pages/Jobs";
import { RankingsPage } from "./pages/Rankings";
import { ScoringPage } from "./pages/Scoring";
import { TagsPage } from "./pages/Tags";
import { useJobStore } from "./stores/jobs";

const nav = [
  { to: "/", label: "Library", icon: IconPhoto, end: true },
  { to: "/tags", label: "Tags", icon: IconTags },
  { to: "/duplicates", label: "Duplicates", icon: IconCopy },
  { to: "/scoring", label: "Scoring", icon: IconChartBar },
  { to: "/analysis", label: "Analysis", icon: IconSparkles },
  { to: "/generate", label: "Generate", icon: IconWand },
  { divider: "Transfer" },
  { to: "/import", label: "Import", icon: IconFolderUp },
  { to: "/export", label: "Export", icon: IconUpload },
  { to: "/backup", label: "Backup", icon: IconDownload },
  { divider: "Housekeeping" },
  { to: "/trash", label: "Trash", icon: IconTrash },
  { to: "/jobs", label: "Jobs", icon: IconListCheck },
] as const;

export function App() {
  useServerEvents();
  const [opened, { toggle, close }] = useDisclosure();
  const { data: stats } = useStats();
  const { colorScheme, toggleColorScheme } = useMantineColorScheme();
  const connected = useJobStore((s) => s.connected);
  const location = useLocation();
  const fullBleed = location.pathname.startsWith("/scoring/runs/");

  return (
    <AppShell
      header={{ height: 52 }}
      navbar={{ width: 210, breakpoint: "sm", collapsed: { mobile: !opened, desktop: fullBleed } }}
      padding={0}
    >
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between" wrap="nowrap">
          <Group gap="sm" wrap="nowrap">
            <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" />
            <IconArchive size={22} color="var(--mantine-color-teal-5)" />
            <Text fw={700}>PhotoBag</Text>
            {stats && (
              <Text size="sm" c="dimmed" truncate visibleFrom="xs">
                {stats.name} · {plural(stats.images, "image")} · {formatBytes(stats.file.sizeBytes)}
              </Text>
            )}
          </Group>
          <Group gap="xs" wrap="nowrap">
            <JobIndicator />
            {!connected && (
              <Tooltip label="Live updates disconnected; retrying">
                <Badge color="red" variant="light">
                  offline
                </Badge>
              </Tooltip>
            )}
            <ActionIcon variant="subtle" color="gray" onClick={toggleColorScheme} aria-label="Toggle colour scheme">
              {colorScheme === "dark" ? <IconSun size={18} /> : <IconMoon size={18} />}
            </ActionIcon>
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="xs">
        <AppShell.Section grow component={ScrollArea}>
          {nav.map((item, i) =>
            "divider" in item ? (
              <Text key={i} size="xs" c="dimmed" tt="uppercase" fw={600} mt="md" mb={4} px="sm">
                {item.divider}
              </Text>
            ) : (
              <NavLink
                key={item.to}
                component={RouterNavLink}
                to={item.to}
                end={"end" in item ? item.end : undefined}
                label={item.label}
                leftSection={<item.icon size={18} stroke={1.6} />}
                onClick={close}
                rightSection={
                  item.to === "/trash" && stats?.trashed ? (
                    <Badge size="xs" variant="light" color="gray">
                      {stats.trashed}
                    </Badge>
                  ) : item.to === "/generate" && stats?.generated ? (
                    <Tooltip label="Generated images not yet moved to the library">
                      <Badge size="xs" variant="light" color="grape">
                        {stats.generated}
                      </Badge>
                    </Tooltip>
                  ) : undefined
                }
                style={{ borderRadius: "var(--mantine-radius-md)" }}
              />
            ),
          )}
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main h="100dvh" style={{ display: "flex", flexDirection: "column" }}>
        <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
          <Routes>
            <Route path="/" element={<GalleryPage />} />
            <Route path="/trash" element={<GalleryPage trash />} />
            <Route path="/tags" element={<TagsPage />} />
            <Route path="/duplicates" element={<DedupPage />} />
            <Route path="/scoring" element={<ScoringPage />} />
            <Route path="/scoring/runs/:id" element={<ComparePage />} />
            <Route path="/scoring/metrics/:id" element={<RankingsPage />} />
            <Route path="/analysis" element={<AnalysisPage />} />
            <Route path="/generate" element={<GeneratePage />} />
            <Route path="/generate/:id" element={<ExperimentPage />} />
            <Route path="/import" element={<ImportPage />} />
            <Route path="/export" element={<ExportPage />} />
            <Route path="/backup" element={<BackupPage />} />
            <Route path="/jobs" element={<JobsPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </div>
      </AppShell.Main>
    </AppShell>
  );
}
