import { ActionIcon, Modal, ScrollArea } from "@mantine/core";
import { useHotkeys } from "@mantine/hooks";
import { IconChevronLeft, IconChevronRight, IconX } from "@tabler/icons-react";
import { useEffect } from "react";
import { preload, previewUrl } from "../api/client";
import { ImageInfo } from "./ImageInfo";
import classes from "./Lightbox.module.css";

interface LightboxProps {
  ids: number[];
  index: number | null;
  onIndex: (i: number) => void;
  onClose: () => void;
}

const SIZE = window.devicePixelRatio > 1.5 || window.innerWidth > 1800 ? 2560 : 1600;

/** Full-screen viewer with metadata panel; ←/→ to browse, Esc to close. */
export function Lightbox({ ids, index, onIndex, onClose }: LightboxProps) {
  const open = index !== null && index >= 0 && index < ids.length;
  const id = open ? ids[index!] : undefined;
  const go = (d: number) => {
    if (!open) return;
    const next = index! + d;
    if (next >= 0 && next < ids.length) onIndex(next);
  };
  useHotkeys(
    open
      ? [
          ["ArrowLeft", () => go(-1)],
          ["ArrowRight", () => go(1)],
        ]
      : [],
  );
  useEffect(() => {
    if (!open) return;
    for (const d of [1, -1, 2]) {
      const n = ids[index! + d];
      if (n) preload(previewUrl(n, SIZE));
    }
  }, [open, index, ids]);

  return (
    <Modal opened={open} onClose={onClose} fullScreen withCloseButton={false} padding={0} transitionProps={{ duration: 0 }}>
      {id !== undefined && (
        <div className={classes.root}>
          <div className={classes.stage} onClick={(e) => e.target === e.currentTarget && onClose()}>
            <img key={id} src={previewUrl(id, SIZE)} alt="" />
            <span className={classes.counter}>
              {index! + 1} / {ids.length}
            </span>
            <div className={classes.top}>
              <ActionIcon variant="filled" color="dark" size="lg" onClick={onClose} aria-label="Close">
                <IconX size={18} />
              </ActionIcon>
            </div>
            {index! > 0 && (
              <ActionIcon
                className={`${classes.nav} ${classes.prev}`}
                variant="filled"
                color="dark"
                size="xl"
                radius="xl"
                onClick={() => go(-1)}
                aria-label="Previous"
              >
                <IconChevronLeft />
              </ActionIcon>
            )}
            {index! < ids.length - 1 && (
              <ActionIcon
                className={`${classes.nav} ${classes.next}`}
                variant="filled"
                color="dark"
                size="xl"
                radius="xl"
                onClick={() => go(1)}
                aria-label="Next"
              >
                <IconChevronRight />
              </ActionIcon>
            )}
          </div>
          <ScrollArea className={classes.panel}>
            <ImageInfo
              id={id}
              onTrashed={() => {
                if (ids.length <= 1) onClose();
                else if (index! >= ids.length - 1) onIndex(index! - 1);
              }}
            />
          </ScrollArea>
        </div>
      )}
    </Modal>
  );
}
