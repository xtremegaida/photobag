import { Button, Center, EmptyState } from "@mantine/core";
import { IconDownload, IconFileUnknown } from "@tabler/icons-react";
import { fileContentUrl } from "../../api/files";
import type { FileListing, FileNode } from "../../api/types";
import { formatBytes } from "../../lib/format";
import { kindLabel } from "./common";
import { FileActions, FileHeader } from "./FileHeader";
import classes from "./Files.module.css";
import { TextFile } from "./TextFile";

function Preview({ node }: { node: FileNode }) {
  const url = fileContentUrl(node);
  switch (node.kind) {
    case "image":
      return (
        <div className={classes.picture}>
          <img src={url} alt={node.name} />
        </div>
      );
    case "pdf":
      return <iframe className={classes.frame} src={url} title={node.name} />;
    case "audio":
      return (
        <Center p="xl">
          <audio controls src={url} style={{ width: "min(640px, 100%)" }} />
        </Center>
      );
    case "video":
      return (
        <div className={classes.picture}>
          <video controls src={url} style={{ maxWidth: "100%", maxHeight: "100%" }} />
        </div>
      );
  }
  return (
    <EmptyState
      mt="xl"
      icon={<IconFileUnknown size={40} />}
      title="No preview for this kind of file"
      description={`${kindLabel(node)}, ${formatBytes(node.size)}. Download it to open it with another program.`}
    >
      <Button component="a" href={fileContentUrl(node, true)} download leftSection={<IconDownload size={16} />}>
        Download
      </Button>
    </EmptyState>
  );
}

/** One file of the bag, shown in the browser where it can be (and text edited). */
export function FileViewer({ listing, path }: { listing: FileListing; path: string }) {
  const node = listing.node!;
  if (node.kind === "text" || node.kind === "markdown") return <TextFile listing={listing} path={path} />;
  return (
    <div className={classes.viewer}>
      <FileHeader listing={listing}>
        <FileActions listing={listing} />
      </FileHeader>
      <div className={classes.viewer} style={{ borderTop: "1px solid var(--mantine-color-default-border)" }}>
        <Preview node={node} />
      </div>
    </div>
  );
}
