import { ColorInput, Group, NumberInput, SegmentedControl, Slider, Stack, Switch, Text } from "@mantine/core";
import { useElementSize } from "@mantine/hooks";
import { useEffect, useState } from "react";
import { thumbUrl } from "../../api/client";
import { useImage } from "../../api/hooks";
import type { SlideshowSettings } from "../../api/types";
import { fadeMillis, seconds } from "../../lib/slideshow";
import classes from "./SettingsForm.module.css";

const FIT_HELP: Record<SlideshowSettings["fit"], string> = {
  contain: "Each image is shown whole, as large as fits on the screen.",
  cover: "Images fill the screen; what sticks out is cut off.",
  stretch: "Images fill the screen, stretched out of proportion where need be.",
  center: "Images show at their actual size, centred. Larger ones shrink to fit.",
};

const SWATCHES = ["#000000", "#141517", "#25262b", "#5c5f66", "#adb5bd", "#f1f3f5", "#ffffff", "#0b1a2e", "#231a10"];

const HEX = /^#[0-9a-f]{6}$/i;

/** A small picture of the screen with the settings applied. */
function Preview({ settings, imageId }: { settings: SlideshowSettings; imageId?: number }) {
  const { ref, width } = useElementSize();
  const { data: im } = useImage(imageId);
  // "Actual size" scales with the screen: the box stands for it.
  const scale = width / (window.screen.width || 1920);
  const dpr = window.devicePixelRatio || 1;
  const actual =
    settings.fit === "center" && im ? { width: (im.width / dpr) * scale, height: (im.height / dpr) * scale } : undefined;
  return (
    <div ref={ref} className={classes.preview} style={{ background: settings.background }}>
      {imageId ? (
        <img src={thumbUrl(imageId)} alt="" data-fit={settings.fit} style={actual} draggable={false} />
      ) : (
        <Text size="xs" c="dimmed" className={classes.empty}>
          No images yet
        </Text>
      )}
      {settings.captions && im && <div className={classes.caption}>{im.caption || im.name}</div>}
    </div>
  );
}

/** Edits how a slideshow plays. */
export function SettingsForm({
  value,
  onChange,
  previewId,
  nested,
}: {
  value: SlideshowSettings;
  onChange: (s: SlideshowSettings) => void;
  /** The image the preview shows. */
  previewId?: number;
  /** In a popover: keep the colour picker inside it. */
  nested?: boolean;
}) {
  const set = (p: Partial<SlideshowSettings>) => onChange({ ...value, ...p });
  // Fields that pass through invalid text while typing keep their own.
  const [intervalText, setIntervalText] = useState<string | number>(value.interval);
  const [colour, setColour] = useState(value.background);
  useEffect(() => setIntervalText(value.interval), [value.interval]);
  useEffect(() => setColour(value.background), [value.background]);
  const timed = value.advance === "timed";
  const fade = fadeMillis(value) / 1000;
  return (
    <Stack gap="md">
      <Preview settings={value} imageId={previewId} />
      <div>
        <Text size="sm" fw={500} mb={4}>
          Next slide
        </Text>
        <SegmentedControl
          fullWidth
          size="xs"
          data={[
            { value: "timed", label: "Automatically" },
            { value: "manual", label: "By hand" },
          ]}
          value={value.advance}
          onChange={(v) => set({ advance: v as SlideshowSettings["advance"] })}
        />
        {timed ? (
          <NumberInput
            mt="xs"
            size="xs"
            label="Seconds per slide"
            min={1}
            max={3600}
            step={1}
            decimalScale={1}
            value={intervalText}
            onChange={(v) => {
              setIntervalText(v);
              if (typeof v === "number" && v >= 1 && v <= 3600) set({ interval: v });
            }}
          />
        ) : (
          <Text size="xs" c="dimmed" mt={4}>
            → or Space, a click or a swipe shows the next slide; ← goes back.
          </Text>
        )}
      </div>
      <div>
        <Switch
          label="Cross-fade"
          checked={value.crossfade}
          onChange={(e) => set({ crossfade: e.currentTarget.checked })}
          mb={6}
        />
        <Group gap="sm" wrap="nowrap">
          <Slider
            min={0.1}
            max={5}
            step={0.1}
            value={value.fade}
            onChange={(v) => set({ fade: Math.round(v * 10) / 10 })}
            disabled={!value.crossfade}
            label={null}
            style={{ flex: 1 }}
            aria-label="Cross-fade time"
          />
          <Text size="sm" w={44} ta="right" c={value.crossfade ? undefined : "dimmed"}>
            {seconds(value.fade)}
          </Text>
        </Group>
        {value.crossfade && fade < value.fade && (
          <Text size="xs" c="dimmed" mt={4}>
            Shortened to {seconds(fade)} so each slide settles before the next.
          </Text>
        )}
      </div>
      <div>
        <Text size="sm" fw={500} mb={4}>
          Images
        </Text>
        <SegmentedControl
          fullWidth
          size="xs"
          data={[
            { value: "contain", label: "Fit" },
            { value: "cover", label: "Fill" },
            { value: "stretch", label: "Stretch" },
            { value: "center", label: "Actual size" },
          ]}
          value={value.fit}
          onChange={(v) => set({ fit: v as SlideshowSettings["fit"] })}
        />
        <Text size="xs" c="dimmed" mt={4}>
          {FIT_HELP[value.fit]}
        </Text>
      </div>
      <ColorInput
        size="xs"
        label="Background"
        format="hex"
        swatches={SWATCHES}
        swatchesPerRow={9}
        withEyeDropper={false}
        popoverProps={{ withinPortal: !nested }}
        value={colour}
        onChange={(v) => {
          setColour(v);
          if (HEX.test(v)) set({ background: v.toLowerCase() });
        }}
        onBlur={() => setColour(value.background)}
      />
      <Stack gap="xs">
        <Switch
          label="Shuffle"
          description="Play the slides in a random order"
          checked={value.shuffle}
          onChange={(e) => set({ shuffle: e.currentTarget.checked })}
        />
        <Switch
          label="Loop"
          description="Start over after the last slide"
          checked={value.loop}
          onChange={(e) => set({ loop: e.currentTarget.checked })}
        />
        <Switch
          label="Captions"
          description="Show each image's name, or its description if it has one"
          checked={value.captions}
          onChange={(e) => set({ captions: e.currentTarget.checked })}
        />
      </Stack>
    </Stack>
  );
}
