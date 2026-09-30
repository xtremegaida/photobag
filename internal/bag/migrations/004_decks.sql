-- PhotoBag schema v4: slide decks.

-- A slide deck: library images in a chosen order, with the settings of its
-- slideshow (a JSON object, see decks.Settings).
CREATE TABLE decks (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  key        TEXT    NOT NULL UNIQUE,
  notes      TEXT    NOT NULL DEFAULT '',
  settings   TEXT    NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- The images of a deck in slide order; an image is in a deck at most once.
-- Trashed images keep their place (hidden), so restoring them brings them
-- back; purging removes them.
CREATE TABLE deck_images (
  deck_id  INTEGER NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
  image_id INTEGER NOT NULL REFERENCES images(id),
  position INTEGER NOT NULL,
  added_at INTEGER NOT NULL,
  PRIMARY KEY (deck_id, image_id)
) WITHOUT ROWID;
CREATE INDEX deck_images_order ON deck_images(deck_id, position);
CREATE INDEX deck_images_image ON deck_images(image_id);
