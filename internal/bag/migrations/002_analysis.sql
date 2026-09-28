-- PhotoBag schema v2: machine analysis of images by a vision-language model
-- (OCR text, captions, Danbooru tags, categories).

-- One current result per image and pipeline. text is the result as plain
-- text ('' means the pipeline found nothing, e.g. no legible text); data
-- holds pipeline-specific JSON (tag lists, main/sub category).
CREATE TABLE analyses (
  id         INTEGER PRIMARY KEY,
  image_id   INTEGER NOT NULL REFERENCES images(id),
  pipeline   TEXT    NOT NULL,
  text       TEXT    NOT NULL,
  data       TEXT,
  model      TEXT    NOT NULL DEFAULT '',
  config     TEXT    NOT NULL DEFAULT '', -- hash of the prompt and options that produced it
  edited     INTEGER NOT NULL DEFAULT 0,  -- 1 once a person corrected it; jobs never overwrite it
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (image_id, pipeline)
);
CREATE INDEX analyses_pipeline ON analyses(pipeline);

-- The pipeline that attached a tag (NULL: a person or an import did). A
-- re-run replaces its own tags without touching hand-made ones.
ALTER TABLE image_tags ADD COLUMN source TEXT;
