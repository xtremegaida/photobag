import { ActionIcon, Center, Loader, Modal, ScrollArea } from "@mantine/core";
import { useHotkeys } from "@mantine/hooks";
import { IconChevronLeft, IconChevronRight, IconX } from "@tabler/icons-react";
import { useEffect } from "react";
import { preload } from "../../api/client";
import { generationPreview, useGeneration } from "../../api/generate";
import type { GenerateRequest } from "../../api/types";
import classes from "../Lightbox.module.css";
import { GenerationInfo } from "./GenerationInfo";

const SIZE = window.devicePixelRatio > 1.5 || window.innerWidth > 1800 ? 2560 : 1600;

/** Full-screen viewer for an experiment's images; ←/→ to browse. */
export function GenerationViewer({
  ids,
  shas,
  index,
  onIndex,
  onClose,
  onUse,
  count,
}: {
  ids: number[];
  /** The content of each generation, which versions its image URLs. */
  shas: Map<number, string>;
  index: number | null;
  onIndex: (i: number) => void;
  onClose: () => void;
  onUse: (r: GenerateRequest) => void;
  count: number;
}) {
  const open = index !== null && index >= 0 && index < ids.length;
  const id = open ? ids[index!] : undefined;
  const { data: g } = useGeneration(id);
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
    for (const d of [1, -1]) {
      const n = ids[index! + d];
      if (n) preload(generationPreview({ id: n, sha256: shas.get(n) }, SIZE));
    }
  }, [open, index, ids, shas]);
  // After a move or delete the list shrinks: stay at the same place.
  const gone = () => {
    if (ids.length <= 1) onClose();
    else if (index! >= ids.length - 1) onIndex(index! - 1);
  };
  return (
    <Modal opened={open} onClose={onClose} fullScreen withCloseButton={false} padding={0} transitionProps={{ duration: 0 }}>
      {id !== undefined && (
        <div className={classes.root}>
          <div className={classes.stage} onClick={(e) => e.target === e.currentTarget && onClose()}>
            <img key={id} src={generationPreview({ id, sha256: shas.get(id) }, SIZE)} alt="" />
            <span className={classes.counter}>
              {index! + 1} / {ids.length}
            </span>
            <div className={classes.top}>
              <ActionIcon variant="filled" color="dark" size="lg" onClick={onClose} aria-label="Close">
                <IconX size={18} />
              </ActionIcon>
            </div>
            {index! > 0 && (
              <ActionIcon className={`${classes.nav} ${classes.prev}`} variant="filled" color="dark" size="xl" radius="xl" onClick={() => go(-1)} aria-label="Previous">
                <IconChevronLeft />
              </ActionIcon>
            )}
            {index! < ids.length - 1 && (
              <ActionIcon className={`${classes.nav} ${classes.next}`} variant="filled" color="dark" size="xl" radius="xl" onClick={() => go(1)} aria-label="Next">
                <IconChevronRight />
              </ActionIcon>
            )}
          </div>
          <ScrollArea className={classes.panel}>
            <div style={{ padding: "var(--mantine-spacing-md)" }}>
              {g && g.id === id ? (
                <GenerationInfo
                  g={g}
                  count={count}
                  onUse={(r) => {
                    onUse(r);
                    onClose();
                  }}
                  onGone={gone}
                />
              ) : (
                <Center p="xl">
                  <Loader size="sm" />
                </Center>
              )}
            </div>
          </ScrollArea>
        </div>
      )}
    </Modal>
  );
}
