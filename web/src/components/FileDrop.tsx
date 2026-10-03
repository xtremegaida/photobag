import { Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconCloudUpload } from "@tabler/icons-react";
import { useRef, useState, type DragEvent, type ReactNode } from "react";
import { errorMessage } from "../api/client";
import { gatherDrop, hasOsFiles, type Gathered } from "../lib/files";
import classes from "./FileDrop.module.css";

export const fileDropClasses = classes;

/**
 * Makes an element (give it fileDropClasses.zone) take files and folders
 * dragged in from the computer. While not enabled, drops are refused rather
 * than left to the browser, which would open the file in place of the app.
 */
export function useFileDrop(onFiles: (g: Gathered) => void, enabled = true) {
  const [over, setOver] = useState(false);
  const depth = useRef(0);
  const handlers = {
    onDragEnter: (e: DragEvent) => {
      if (!hasOsFiles(e.dataTransfer)) return;
      depth.current++;
      setOver(true);
    },
    onDragLeave: (e: DragEvent) => {
      if (!hasOsFiles(e.dataTransfer)) return;
      depth.current = Math.max(0, depth.current - 1);
      if (depth.current === 0) setOver(false);
    },
    onDragOver: (e: DragEvent) => {
      if (!hasOsFiles(e.dataTransfer)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = enabled ? "copy" : "none";
    },
    onDrop: (e: DragEvent) => {
      if (!hasOsFiles(e.dataTransfer)) return;
      e.preventDefault();
      depth.current = 0;
      setOver(false);
      if (enabled) {
        gatherDrop(e.dataTransfer).then(onFiles, (err) =>
          notifications.show({ color: "red", title: "Could not read what was dropped", message: errorMessage(err) }),
        );
      }
    },
  };
  return { over: over && enabled, handlers };
}

/** Shown over a drop zone while files are dragged over it. */
export function DropOverlay({ title, children }: { title: ReactNode; children?: ReactNode }) {
  return (
    <div className={classes.overlay}>
      <Stack align="center" gap={4}>
        <IconCloudUpload size={40} color="var(--mantine-color-blue-6)" />
        <Text fw={600}>{title}</Text>
        {children && (
          <Text size="sm" c="dimmed">
            {children}
          </Text>
        )}
      </Stack>
    </div>
  );
}
