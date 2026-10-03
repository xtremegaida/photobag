-- PhotoBag schema v6: re-encoding images.

-- A re-encode of a set of images. settings is a JSON object (see
-- reencode.Settings); mode 'replace' swaps results in as they are made,
-- 'review' keeps them for the user to compare and decide.
CREATE TABLE reencode_batches (
  id          INTEGER PRIMARY KEY,
  settings    TEXT    NOT NULL,
  mode        TEXT    NOT NULL CHECK (mode IN ('replace', 'review')),
  description TEXT    NOT NULL DEFAULT '',
  state       TEXT    NOT NULL DEFAULT 'queued', -- queued | running | stopped | finished
  error       TEXT    NOT NULL DEFAULT '',
  job_id      TEXT    NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  finished_at INTEGER
);

-- One image of a batch. A result waiting for review is a blob of its own
-- (new_blob_id, with thumbnail and thumbprint), so replacing the original
-- only repoints the image. psnr is NULL for a pixel-exact result.
CREATE TABLE reencode_items (
  batch_id    INTEGER NOT NULL REFERENCES reencode_batches(id) ON DELETE CASCADE,
  image_id    INTEGER NOT NULL REFERENCES images(id),
  ord         INTEGER NOT NULL,
  status      TEXT    NOT NULL DEFAULT 'pending', -- pending | ready | replaced | kept | skipped | failed
  reason      TEXT    NOT NULL DEFAULT '',
  notes       TEXT    NOT NULL DEFAULT '[]',
  old_blob_id INTEGER,
  old_format  TEXT    NOT NULL DEFAULT '',
  old_size    INTEGER NOT NULL DEFAULT 0,
  old_width   INTEGER NOT NULL DEFAULT 0,
  old_height  INTEGER NOT NULL DEFAULT 0,
  new_blob_id INTEGER REFERENCES blobs(id),
  new_sha256  BLOB,
  new_format  TEXT    NOT NULL DEFAULT '',
  new_size    INTEGER NOT NULL DEFAULT 0,
  new_width   INTEGER NOT NULL DEFAULT 0,
  new_height  INTEGER NOT NULL DEFAULT 0,
  psnr        REAL,
  millis      INTEGER NOT NULL DEFAULT 0,
  decided_at  INTEGER,
  PRIMARY KEY (batch_id, image_id)
);
CREATE INDEX reencode_items_order ON reencode_items(batch_id, ord);
CREATE INDEX reencode_items_image ON reencode_items(image_id);
CREATE INDEX reencode_items_blob  ON reencode_items(new_blob_id) WHERE new_blob_id IS NOT NULL;

-- Every original a re-encode replaced: the image's history, and how an
-- import recognises the old file.
CREATE TABLE image_reencodes (
  id         INTEGER PRIMARY KEY,
  image_id   INTEGER NOT NULL REFERENCES images(id),
  batch_id   INTEGER REFERENCES reencode_batches(id) ON DELETE SET NULL,
  old_sha256 BLOB    NOT NULL,
  old_format TEXT    NOT NULL,
  old_size   INTEGER NOT NULL,
  old_width  INTEGER NOT NULL,
  old_height INTEGER NOT NULL,
  new_sha256 BLOB    NOT NULL,
  new_format TEXT    NOT NULL,
  new_size   INTEGER NOT NULL,
  new_width  INTEGER NOT NULL,
  new_height INTEGER NOT NULL,
  settings   TEXT    NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX image_reencodes_image ON image_reencodes(image_id);
CREATE INDEX image_reencodes_old   ON image_reencodes(old_sha256);

-- Lossy WebP used to be decoded with the wrong colour range (JPEG's full
-- range instead of VP8's studio range). Dropping their thumbnails and
-- thumbprints makes the server remake them.
DELETE FROM thumbnails WHERE blob_id IN (SELECT blob_id FROM images WHERE format = 'webp' AND blob_id IS NOT NULL);
DELETE FROM fingerprints WHERE blob_id IN (SELECT blob_id FROM images WHERE format = 'webp' AND blob_id IS NOT NULL);
