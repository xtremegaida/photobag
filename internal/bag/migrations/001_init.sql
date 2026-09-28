-- PhotoBag schema v1. Timestamps are unix milliseconds. sha256 values are
-- 32-byte BLOBs.

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID;

-- Content-addressed original bytes. Bit-identical files share one blob.
CREATE TABLE blobs (
  id     INTEGER PRIMARY KEY,
  sha256 BLOB    NOT NULL UNIQUE,
  size   INTEGER NOT NULL,
  data   BLOB    NOT NULL
);

CREATE TABLE thumbnails (
  blob_id INTEGER PRIMARY KEY REFERENCES blobs(id) ON DELETE CASCADE,
  width   INTEGER NOT NULL,
  height  INTEGER NOT NULL,
  data    BLOB    NOT NULL
);

CREATE TABLE fingerprints (
  blob_id   INTEGER PRIMARY KEY REFERENCES blobs(id) ON DELETE CASCADE,
  version   INTEGER NOT NULL,
  phash     INTEGER NOT NULL,
  color     BLOB    NOT NULL,
  aspect    REAL    NOT NULL,
  ac_energy REAL    NOT NULL
);

CREATE TABLE imports (
  id           INTEGER PRIMARY KEY,
  source       TEXT    NOT NULL,
  recursive    INTEGER NOT NULL DEFAULT 0,
  options_json TEXT    NOT NULL DEFAULT '{}',
  started_at   INTEGER NOT NULL,
  finished_at  INTEGER,
  added        INTEGER NOT NULL DEFAULT 0,
  skipped      INTEGER NOT NULL DEFAULT 0,
  failed       INTEGER NOT NULL DEFAULT 0
);

-- Images are never hard-deleted: trashed images keep deleted_at, purged
-- images become tombstones (blob_id NULL) so comparison history and
-- "previously removed" detection survive.
CREATE TABLE images (
  id            INTEGER PRIMARY KEY,
  uid           TEXT    NOT NULL UNIQUE,
  sha256        BLOB    NOT NULL,
  blob_id       INTEGER REFERENCES blobs(id),
  name          TEXT    NOT NULL,
  original_name TEXT    NOT NULL,
  original_path TEXT    NOT NULL DEFAULT '',
  import_id     INTEGER REFERENCES imports(id),
  format        TEXT    NOT NULL,
  size          INTEGER NOT NULL,
  width         INTEGER NOT NULL,
  height        INTEGER NOT NULL,
  orientation   INTEGER NOT NULL DEFAULT 1,
  file_mtime    INTEGER,
  taken_at      TEXT,
  taken_offset  TEXT,
  imported_at   INTEGER NOT NULL,
  deleted_at    INTEGER,
  merged_into   INTEGER REFERENCES images(id),
  purged_at     INTEGER
);
CREATE INDEX images_sha      ON images(sha256);
CREATE INDEX images_blob     ON images(blob_id);
CREATE INDEX images_imported ON images(imported_at);
CREATE INDEX images_taken    ON images(taken_at);
CREATE INDEX images_name     ON images(name COLLATE NOCASE);
CREATE INDEX images_deleted  ON images(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX images_merged   ON images(merged_into) WHERE merged_into IS NOT NULL;

-- key = NFC-normalised, Unicode case-folded name (NOCASE only folds ASCII).
CREATE TABLE tags (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  key        TEXT    NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
);

CREATE TABLE image_tags (
  image_id INTEGER NOT NULL REFERENCES images(id) ON DELETE CASCADE,
  tag_id   INTEGER NOT NULL REFERENCES tags(id)   ON DELETE CASCADE,
  PRIMARY KEY (image_id, tag_id)
) WITHOUT ROWID;
CREATE INDEX image_tags_tag ON image_tags(tag_id, image_id);

-- User decisions from the dedup review: these pairs are distinct images.
CREATE TABLE not_duplicates (
  a          INTEGER NOT NULL REFERENCES images(id),
  b          INTEGER NOT NULL REFERENCES images(id),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (a, b),
  CHECK (a < b)
) WITHOUT ROWID;

CREATE TABLE metrics (
  id               INTEGER PRIMARY KEY,
  name             TEXT    NOT NULL,
  key              TEXT    NOT NULL UNIQUE,
  description      TEXT    NOT NULL DEFAULT '',
  created_at       INTEGER NOT NULL,
  scores_dirty     INTEGER NOT NULL DEFAULT 1, -- change counter; 0 = scores fresh
  scored_at        INTEGER,
  comparison_count INTEGER NOT NULL DEFAULT 0,
  pair_count       INTEGER NOT NULL DEFAULT 0,
  cancelled_pairs  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE score_runs (
  id             INTEGER PRIMARY KEY,
  metric_id      INTEGER NOT NULL REFERENCES metrics(id) ON DELETE CASCADE,
  seed           INTEGER NOT NULL,
  position       INTEGER NOT NULL DEFAULT 0,
  total_pairs    INTEGER NOT NULL,
  image_count    INTEGER NOT NULL,
  status         TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'stopped', 'complete')),
  selection_json TEXT    NOT NULL DEFAULT '{}',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  finished_at    INTEGER
);
CREATE INDEX score_runs_metric ON score_runs(metric_id);

-- The frozen, ordered input set of a run.
CREATE TABLE score_run_images (
  run_id   INTEGER NOT NULL REFERENCES score_runs(id) ON DELETE CASCADE,
  ord      INTEGER NOT NULL,
  image_id INTEGER NOT NULL REFERENCES images(id),
  PRIMARY KEY (run_id, ord)
) WITHOUT ROWID;

-- winner NULL means the pair was skipped.
CREATE TABLE comparisons (
  id         INTEGER PRIMARY KEY,
  run_id     INTEGER NOT NULL REFERENCES score_runs(id) ON DELETE CASCADE,
  pos        INTEGER NOT NULL,
  metric_id  INTEGER NOT NULL REFERENCES metrics(id) ON DELETE CASCADE,
  left_id    INTEGER NOT NULL REFERENCES images(id),
  right_id   INTEGER NOT NULL REFERENCES images(id),
  winner     INTEGER REFERENCES images(id),
  created_at INTEGER NOT NULL,
  UNIQUE (run_id, pos)
);
CREATE INDEX comparisons_metric ON comparisons(metric_id);
CREATE INDEX comparisons_left   ON comparisons(left_id);
CREATE INDEX comparisons_right  ON comparisons(right_id);

CREATE TABLE scores (
  metric_id     INTEGER NOT NULL REFERENCES metrics(id) ON DELETE CASCADE,
  image_id      INTEGER NOT NULL REFERENCES images(id),
  theta         REAL    NOT NULL,
  score         REAL    NOT NULL,
  stderr        REAL    NOT NULL,
  stderr_approx INTEGER NOT NULL DEFAULT 0,
  n             INTEGER NOT NULL,
  wins          INTEGER NOT NULL,
  losses        INTEGER NOT NULL,
  raw_wins      INTEGER NOT NULL,
  raw_losses    INTEGER NOT NULL,
  computed_at   INTEGER NOT NULL,
  PRIMARY KEY (metric_id, image_id)
) WITHOUT ROWID;
CREATE INDEX scores_rank ON scores(metric_id, score DESC);
