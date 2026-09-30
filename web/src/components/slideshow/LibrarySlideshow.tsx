import { Button, Popover, ScrollArea, Stack, Text } from "@mantine/core";
import { IconChevronDown, IconPlayerPlay } from "@tabler/icons-react";
import { plural } from "../../lib/format";
import { SettingsForm } from "./SettingsForm";
import { useLibrarySlideshowSettings, useStartSlideshow } from "./start";

/** Plays the gallery's images; the arrow opens the settings. */
export function LibrarySlideshow({ count, params, firstId }: { count: number; params: URLSearchParams; firstId?: number }) {
  const start = useStartSlideshow();
  const [settings, setSettings] = useLibrarySlideshowSettings();
  return (
    <Button.Group>
      <Button size="xs" variant="light" leftSection={<IconPlayerPlay size={16} />} disabled={!count} onClick={() => start({ params })}>
        Slideshow
      </Button>
      <Popover position="bottom-end" shadow="md" withArrow width={340}>
        <Popover.Target>
          <Button size="xs" variant="light" px={6} aria-label="Slideshow settings">
            <IconChevronDown size={16} />
          </Button>
        </Popover.Target>
        <Popover.Dropdown p={0}>
          <ScrollArea.Autosize mah="75dvh">
            <Stack gap="sm" p="md">
              <div>
                <Text size="sm" fw={600}>
                  Library slideshow
                </Text>
                <Text size="xs" c="dimmed">
                  Plays the {plural(count, "image")} shown here, in this order. The settings are kept in this browser; new
                  slide decks start with them too.
                </Text>
              </div>
              <SettingsForm value={settings} onChange={setSettings} previewId={firstId} nested />
            </Stack>
          </ScrollArea.Autosize>
        </Popover.Dropdown>
      </Popover>
    </Button.Group>
  );
}
