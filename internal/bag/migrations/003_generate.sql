-- PhotoBag schema v3: image generation experiments with ComfyUI.

-- Workflow contents (ComfyUI API format), content-addressed. Every
-- generated image points at the exact version it was made from, so edits
-- to a template never change the record of how an image was made.
CREATE TABLE workflow_versions (
  id         INTEGER PRIMARY KEY,
  sha256     BLOB    NOT NULL UNIQUE,
  json       TEXT    NOT NULL,
  created_at INTEGER NOT NULL
);

-- Named workflow templates; editing one points it at a new version.
CREATE TABLE workflows (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  key        TEXT    NOT NULL UNIQUE,
  notes      TEXT    NOT NULL DEFAULT '',
  version_id INTEGER NOT NULL REFERENCES workflow_versions(id),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- A container for generated images, which stay out of the library until
-- moved there. request holds the generation settings last used.
CREATE TABLE experiments (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  notes      TEXT    NOT NULL DEFAULT '',
  request    TEXT    NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- One press of Generate: a number of prompts, possibly sweeping overrides.
-- dims are the swept values (the axes of the result grid); failures list
-- the prompts that made no image, and why.
CREATE TABLE generation_runs (
  id            INTEGER PRIMARY KEY,
  experiment_id INTEGER NOT NULL REFERENCES experiments(id) ON DELETE CASCADE,
  version_id    INTEGER NOT NULL REFERENCES workflow_versions(id),
  workflow_id   INTEGER REFERENCES workflows(id) ON DELETE SET NULL,
  workflow_name TEXT    NOT NULL,
  request       TEXT    NOT NULL,
  prompts       INTEGER NOT NULL,
  done          INTEGER NOT NULL DEFAULT 0,
  failed        INTEGER NOT NULL DEFAULT 0,
  images        INTEGER NOT NULL DEFAULT 0,
  status        TEXT    NOT NULL DEFAULT 'running',
  error         TEXT    NOT NULL DEFAULT '',
  dims          TEXT    NOT NULL DEFAULT '[]',
  failures      TEXT    NOT NULL DEFAULT '[]',
  job_id        TEXT    NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  finished_at   INTEGER
);
CREATE INDEX generation_runs_experiment ON generation_runs(experiment_id);

-- One generated image. While it is held in its experiment, blob_id holds
-- its bytes; moving it to the library creates an image (image_id) that
-- takes the blob over. applied lists the exact values set on the workflow
-- version (overrides, swept values, seeds), so the image can be made again.
CREATE TABLE generations (
  id            INTEGER PRIMARY KEY,
  experiment_id INTEGER REFERENCES experiments(id) ON DELETE SET NULL,
  run_id        INTEGER REFERENCES generation_runs(id) ON DELETE SET NULL,
  version_id    INTEGER NOT NULL REFERENCES workflow_versions(id),
  workflow_id   INTEGER REFERENCES workflows(id) ON DELETE SET NULL,
  workflow_name TEXT    NOT NULL,
  applied       TEXT    NOT NULL,
  combo         INTEGER NOT NULL DEFAULT 0,
  repeat        INTEGER NOT NULL DEFAULT 0,
  batch_index   INTEGER NOT NULL DEFAULT 0,
  seed          INTEGER,
  prompt_id     TEXT    NOT NULL DEFAULT '',
  sha256        BLOB    NOT NULL,
  blob_id       INTEGER REFERENCES blobs(id),
  image_id      INTEGER REFERENCES images(id),
  format        TEXT    NOT NULL,
  size          INTEGER NOT NULL,
  width         INTEGER NOT NULL,
  height        INTEGER NOT NULL,
  millis        INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);
CREATE INDEX generations_experiment ON generations(experiment_id);
CREATE INDEX generations_run        ON generations(run_id);
CREATE INDEX generations_image      ON generations(image_id) WHERE image_id IS NOT NULL;
CREATE INDEX generations_blob       ON generations(blob_id) WHERE blob_id IS NOT NULL;
