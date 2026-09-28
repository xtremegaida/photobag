package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"photobag/internal/bag"
)

// Tag is a tag with its active-image count.
type Tag struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// EnsureTags returns tag ids for names, creating missing tags. Names are
// matched case-insensitively; the first spelling used is kept.
func EnsureTags(ctx context.Context, tx *sql.Tx, names []string) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	seen := map[int64]bool{}
	now := bag.NowMillis()
	for _, n := range names {
		if err := bag.ValidateLabel(n); err != nil {
			return nil, err
		}
		clean, key := bag.CleanLabel(n), bag.LabelKey(n)
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO tags(name, key, created_at) VALUES (?, ?, ?) ON CONFLICT(key) DO NOTHING",
			clean, key, now); err != nil {
			return nil, err
		}
		var id int64
		if err := tx.QueryRowContext(ctx, "SELECT id FROM tags WHERE key = ?", key).Scan(&id); err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// TagImages attaches tag ids to image ids inside a transaction.
func TagImages(ctx context.Context, tx *sql.Tx, imageIDs, tagIDs []int64) error {
	if len(imageIDs) == 0 || len(tagIDs) == 0 {
		return nil
	}
	ij, _ := json.Marshal(imageIDs)
	for _, t := range tagIDs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO image_tags(image_id, tag_id)
			SELECT value, ? FROM json_each(?) WHERE value IN (SELECT id FROM images)`, t, string(ij)); err != nil {
			return err
		}
	}
	return nil
}

// AddTags tags the images with names (creating tags as needed).
func AddTags(ctx context.Context, b *bag.Bag, imageIDs []int64, names []string) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		ids, err := EnsureTags(ctx, tx, names)
		if err != nil {
			return err
		}
		return TagImages(ctx, tx, imageIDs, ids)
	})
}

// RemoveTags removes the named tags from the images.
func RemoveTags(ctx context.Context, b *bag.Bag, imageIDs []int64, names []string) error {
	if len(imageIDs) == 0 || len(names) == 0 {
		return nil
	}
	ij, _ := json.Marshal(imageIDs)
	ks := make([]string, 0, len(names))
	for _, n := range names {
		ks = append(ks, bag.LabelKey(n))
	}
	kj, _ := json.Marshal(ks)
	return b.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM image_tags
			WHERE image_id IN (SELECT value FROM json_each(?))
			AND tag_id IN (SELECT id FROM tags WHERE key IN (SELECT value FROM json_each(?)))`, string(ij), string(kj))
		return err
	})
}

// ListTags returns all tags with the number of active images carrying them.
func ListTags(ctx context.Context, b *bag.Bag) ([]Tag, error) {
	rows, err := b.R.QueryContext(ctx, `SELECT t.id, t.name,
		(SELECT count(*) FROM image_tags it JOIN images i ON i.id = it.image_id
		 WHERE it.tag_id = t.id AND i.deleted_at IS NULL AND i.purged_at IS NULL)
		FROM tags t ORDER BY t.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateTag creates a tag (or returns the existing one with that name).
func CreateTag(ctx context.Context, b *bag.Bag, name string) (int64, error) {
	var id int64
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		ids, err := EnsureTags(ctx, tx, []string{name})
		if err == nil {
			id = ids[0]
		}
		return err
	})
	return id, err
}

// RenameTag renames a tag. If another tag already has the new name, the
// two are merged and the surviving tag's id is returned.
func RenameTag(ctx context.Context, b *bag.Bag, id int64, name string) (int64, error) {
	if err := bag.ValidateLabel(name); err != nil {
		return 0, err
	}
	clean, key := bag.CleanLabel(name), bag.LabelKey(name)
	result := id
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tags WHERE id = ?", id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
		var other int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM tags WHERE key = ?", key).Scan(&other)
		switch {
		case errors.Is(err, sql.ErrNoRows) || other == id:
			_, err = tx.ExecContext(ctx, "UPDATE tags SET name = ?, key = ? WHERE id = ?", clean, key, id)
			return err
		case err != nil:
			return err
		}
		// Merge id into other.
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO image_tags(image_id, tag_id)
			SELECT image_id, ? FROM image_tags WHERE tag_id = ?`, other, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM tags WHERE id = ?", id); err != nil {
			return err
		}
		result = other
		return nil
	})
	return result, err
}

// DeleteTag removes a tag from every image and deletes it.
func DeleteTag(ctx context.Context, b *bag.Bag, id int64) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM tags WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("tag %d: %w", id, ErrNotFound)
		}
		return nil
	})
}
