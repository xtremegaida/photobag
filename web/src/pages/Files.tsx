import { Alert, Anchor, Center, Loader, Stack } from "@mantine/core";
import { Link, useParams } from "react-router";
import { ApiError, errorMessage } from "../api/client";
import { useFileListing } from "../api/files";
import { FileBrowser } from "../components/files/FileBrowser";
import { FileViewer } from "../components/files/FileViewer";

/** The bag's ordinary files: /files/<folder>/<file>. */
export function FilesPage() {
  const path = (useParams()["*"] ?? "").split("/").filter(Boolean).join("/");
  const { data, error, isLoading } = useFileListing(path);
  if (isLoading) {
    return (
      <Center style={{ flex: 1 }}>
        <Loader />
      </Center>
    );
  }
  if (error) {
    const missing = error instanceof ApiError && error.status === 404;
    return (
      <Stack p="md" maw={720}>
        <Alert color={missing ? "yellow" : "red"} title={missing ? "Nothing here" : "Could not open this"}>
          {missing ? `There is no file or folder at “${path}”. It may have been moved, renamed or deleted.` : errorMessage(error)}{" "}
          <Anchor component={Link} to="/files">
            Go to Files
          </Anchor>
        </Alert>
      </Stack>
    );
  }
  if (!data) return null;
  return data.node && !data.node.dir ? <FileViewer listing={data} path={path} /> : <FileBrowser listing={data} path={path} />;
}
