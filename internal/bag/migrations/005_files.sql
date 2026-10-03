-- PhotoBag schema v5: ordinary files (notes, documentation) kept in folders.

-- File contents, shared by identical files. sha256 is NULL while an upload
-- is still being written; such rows are removed when the bag is opened.
CREATE TABLE file_contents (
  id         INTEGER PRIMARY KEY,
  sha256     BLOB    UNIQUE,
  size       INTEGER NOT NULL DEFAULT 0,
  -- The content type sniffed from the first bytes (http.DetectContentType).
  sniffed    TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);

-- Contents in chunks (1 MiB, the last one shorter), so files of any size
-- stream in and out without being held in memory.
CREATE TABLE file_chunks (
  content_id INTEGER NOT NULL REFERENCES file_contents(id) ON DELETE CASCADE,
  seq        INTEGER NOT NULL,
  data       BLOB    NOT NULL,
  PRIMARY KEY (content_id, seq)
);

-- Files and folders. parent_id NULL is the top level. key is the folded
-- name: names are unique within a folder, ignoring case, as on Windows.
CREATE TABLE files (
  id          INTEGER PRIMARY KEY,
  parent_id   INTEGER REFERENCES files(id) ON DELETE CASCADE,
  name        TEXT    NOT NULL,
  key         TEXT    NOT NULL,
  is_dir      INTEGER NOT NULL DEFAULT 0,
  content_id  INTEGER REFERENCES file_contents(id),
  size        INTEGER NOT NULL DEFAULT 0,
  modified_at INTEGER NOT NULL,
  created_at  INTEGER NOT NULL,
  CHECK ((is_dir = 1) = (content_id IS NULL))
);
CREATE UNIQUE INDEX files_name    ON files(COALESCE(parent_id, 0), key);
CREATE INDEX        files_parent  ON files(parent_id);
CREATE INDEX        files_content ON files(content_id) WHERE content_id IS NOT NULL;
