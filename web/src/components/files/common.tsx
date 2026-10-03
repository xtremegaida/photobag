import { Anchor, Breadcrumbs, Text } from "@mantine/core";
import {
  IconFile,
  IconFileCode,
  IconFileText,
  IconFileTypePdf,
  IconFileZip,
  IconFolderFilled,
  IconMarkdown,
  IconMovie,
  IconMusic,
  IconPhoto,
} from "@tabler/icons-react";
import type { DragEvent } from "react";
import { Link } from "react-router";
import { filesRoute, joinPath } from "../../api/files";
import type { FileNode } from "../../api/types";
import { splitExt } from "../../lib/files";
import classes from "./Files.module.css";

const codeTypes = new Set(["application/json", "application/xml", "application/yaml", "application/toml", "text/html", "text/css", "text/javascript"]);
const plainExts = new Set(["", ".txt", ".text", ".log", ".nfo", ".csv", ".tsv"]);
const archives = new Set([".zip", ".7z", ".gz", ".tgz", ".tar", ".rar", ".xz"]);

/** An icon for a file or folder. */
export function KindIcon({ node, size = 18 }: { node: FileNode; size?: number }) {
  const p = { size, stroke: 1.6 };
  switch (node.kind) {
    case "folder":
      return <IconFolderFilled size={size} color="var(--mantine-color-yellow-5)" />;
    case "markdown":
      return <IconMarkdown {...p} color="var(--mantine-color-blue-6)" />;
    case "text":
      return codeTypes.has(node.type ?? "") || !plainExts.has(splitExt(node.name)[1].toLowerCase()) ? (
        <IconFileCode {...p} color="var(--mantine-color-violet-6)" />
      ) : (
        <IconFileText {...p} color="var(--mantine-color-gray-6)" />
      );
    case "image":
      return <IconPhoto {...p} color="var(--mantine-color-teal-6)" />;
    case "pdf":
      return <IconFileTypePdf {...p} color="var(--mantine-color-red-6)" />;
    case "audio":
      return <IconMusic {...p} color="var(--mantine-color-pink-6)" />;
    case "video":
      return <IconMovie {...p} color="var(--mantine-color-grape-6)" />;
  }
  if (archives.has(splitExt(node.name)[1].toLowerCase())) return <IconFileZip {...p} color="var(--mantine-color-orange-6)" />;
  return <IconFile {...p} color="var(--mantine-color-gray-6)" />;
}

const kindLabels: Record<string, string> = {
  folder: "Folder",
  markdown: "Markdown",
  image: "Image",
  pdf: "PDF",
  audio: "Audio",
  video: "Video",
};

/** A short description of what a file is. */
export function kindLabel(node: FileNode): string {
  if (kindLabels[node.kind]) return kindLabels[node.kind];
  const ext = splitExt(node.name)[1].slice(1).toUpperCase();
  if (node.kind === "text") return ext && ext.length <= 5 ? `${ext} text` : "Text";
  return ext && ext.length <= 5 ? `${ext} file` : "File";
}

/** Drop-target handlers for a folder (breadcrumbs, folder rows). */
export interface FolderDrop {
  over: number | null;
  handlers: (folderId: number) => {
    onDragOver: (e: DragEvent) => void;
    onDragLeave: (e: DragEvent) => void;
    onDrop: (e: DragEvent) => void;
  };
}

/** The path from the top level to a node, each part a link (and a drop target). */
export function FilePath({ path, current, drop }: { path: FileNode[]; current?: boolean; drop?: FolderDrop }) {
  const crumbs = [{ id: 0, name: "Files", route: "/files" }];
  path.forEach((n, i) =>
    crumbs.push({
      id: n.id,
      name: n.name,
      route: filesRoute(joinPath(path.slice(0, i + 1).map((p) => p.name))),
    }),
  );
  return (
    <Breadcrumbs separatorMargin={6} className={classes.crumbs}>
      {crumbs.map((c, i) => {
        const last = i === crumbs.length - 1;
        const dropProps = drop && !(last && current) ? drop.handlers(c.id) : undefined;
        return last && current ? (
          <Text key={c.id} fw={600} size="lg" truncate maw={420} {...dropProps} data-drop={drop?.over === c.id || undefined}>
            {c.name}
          </Text>
        ) : (
          <Anchor
            key={c.id}
            component={Link}
            to={c.route}
            size="lg"
            c="dimmed"
            truncate
            maw={260}
            {...dropProps}
            data-drop={drop?.over === c.id || undefined}
          >
            {c.name}
          </Anchor>
        );
      })}
    </Breadcrumbs>
  );
}
