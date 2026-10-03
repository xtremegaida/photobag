package library

import (
	"context"
	"database/sql"
	"encoding/json"

	"photobag/internal/bag"
)

// Trash moves active images to the trash. It returns how many moved.
func Trash(ctx context.Context, b *bag.Bag, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	js, _ := json.Marshal(ids)
	var n int64
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE images SET deleted_at = ?
			WHERE id IN (SELECT value FROM json_each(?)) AND deleted_at IS NULL AND purged_at IS NULL`,
			bag.NowMillis(), string(js))
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return MarkAllMetricsDirty(ctx, tx)
	})
	return int(n), err
}

// Restore brings trashed images back (undoing any dedup merge).
func Restore(ctx context.Context, b *bag.Bag, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	js, _ := json.Marshal(ids)
	var n int64
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE images SET deleted_at = NULL, merged_into = NULL
			WHERE id IN (SELECT value FROM json_each(?)) AND deleted_at IS NOT NULL AND purged_at IS NULL`,
			string(js))
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return MarkAllMetricsDirty(ctx, tx)
	})
	return int(n), err
}

// UnusedBlob is a condition on blobs: neither an image, nor a generated
// image held in an experiment, nor a re-encode awaiting review uses it.
const UnusedBlob = `NOT EXISTS (SELECT 1 FROM images WHERE images.blob_id = blobs.id)
	AND NOT EXISTS (SELECT 1 FROM generations WHERE generations.blob_id = blobs.id)
	AND NOT EXISTS (SELECT 1 FROM reencode_items WHERE reencode_items.new_blob_id = blobs.id)`

// PurgeResult summarises an empty-trash operation.
type PurgeResult struct {
	Purged     int   `json:"purged"`
	BlobsFreed int   `json:"blobsFreed"`
	BytesFreed int64 `json:"bytesFreed"`
}

// EmptyTrash purges trashed images (all of them when ids is empty). Purged
// images stay as tombstones (keeping comparison history and "previously
// removed" detection); their original bytes are dropped once no other image
// shares them, and free pages are released with incremental vacuum.
func EmptyTrash(ctx context.Context, b *bag.Bag, ids []int64) (PurgeResult, error) {
	var r PurgeResult
	cond := "deleted_at IS NOT NULL AND purged_at IS NULL"
	var args []any
	if len(ids) > 0 {
		js, _ := json.Marshal(ids)
		cond += " AND id IN (SELECT value FROM json_each(?))"
		args = append(args, string(js))
	}
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS purge_ids(id INTEGER PRIMARY KEY)"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM temp.purge_ids"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO temp.purge_ids SELECT id FROM images WHERE "+cond, args...); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE images SET purged_at = ?, blob_id = NULL
			WHERE id IN (SELECT id FROM temp.purge_ids)`, bag.NowMillis())
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		r.Purged = int(n)
		if _, err := tx.ExecContext(ctx, "DELETE FROM image_tags WHERE image_id IN (SELECT id FROM temp.purge_ids)"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM analyses WHERE image_id IN (SELECT id FROM temp.purge_ids)"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM deck_images WHERE image_id IN (SELECT id FROM temp.purge_ids)"); err != nil {
			return err
		}
		// Re-encodes of purged images have nothing left to replace.
		if _, err := tx.ExecContext(ctx, `UPDATE reencode_items SET status = 'skipped', reason = 'the image was deleted',
			new_blob_id = NULL WHERE status IN ('pending', 'ready') AND image_id IN (SELECT id FROM temp.purge_ids)`); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*), COALESCE(sum(size), 0) FROM blobs
			WHERE `+UnusedBlob).Scan(&r.BlobsFreed, &r.BytesFreed); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM blobs WHERE `+UnusedBlob); err != nil {
			return err
		}
		return MarkAllMetricsDirty(ctx, tx)
	})
	if err != nil {
		return r, err
	}
	_, err = b.W.ExecContext(ctx, "PRAGMA incremental_vacuum")
	return r, err
}
