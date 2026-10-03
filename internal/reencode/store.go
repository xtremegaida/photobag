// Package reencode re-encodes library images in batches: each image gets a
// new file in another format, quality or size, which either replaces the
// original at once or waits for the user to compare the two and decide.
package reencode

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"photobag/internal/bag"
	"photobag/internal/imaging"
	"photobag/internal/library"
)

// ErrNotFound is library.ErrNotFound, so the server maps it to 404.
var ErrNotFound = library.ErrNotFound

// ErrInvalid marks errors in what was asked for.
var ErrInvalid = errors.New("invalid")

// ErrBusy reports a batch that is running.
var ErrBusy = errors.New("busy")

type invalid struct{ error }

func (e invalid) Is(t error) bool { return t == ErrInvalid }
func (e invalid) Unwrap() error   { return e.error }

func invalidf(format string, args ...any) error { return invalid{fmt.Errorf(format, args...)} }

// MaxImages is the most images one batch takes.
const MaxImages = 20000

// DefaultSettings re-encode losslessly to WebP.
func DefaultSettings() Settings {
	return Settings{Format: "webp", Lossless: true, Quality: 80, Effort: 4, Progressive: true, KeepMetadata: true, OnlySmaller: true}
}

// Normalize checks settings and fills in what a format implies.
func (s Settings) Normalize() (Settings, error) {
	switch s.Format {
	case "png":
		s.Lossless = true
		s.Effort = min(max(s.Effort, 0), 2)
		s.Progressive, s.Chroma444 = false, false
	case "jpeg":
		s.Lossless = false
		s.Effort = 0
	case "webp":
		if s.Effort < 0 || s.Effort > 6 {
			return s, invalidf("WebP effort runs from 0 to 6")
		}
		s.Progressive, s.Chroma444 = false, false
	default:
		return s, invalidf("re-encoding writes JPEG, PNG or WebP, not %q", s.Format)
	}
	if s.Lossless {
		s.Quality = 0
	} else if s.Quality < 1 || s.Quality > 100 {
		return s, invalidf("quality runs from 1 to 100")
	}
	if s.MaxWidth < 0 || s.MaxHeight < 0 || s.MaxWidth > 100000 || s.MaxHeight > 100000 {
		return s, invalidf("the largest size must be between 1 and 100000 pixels")
	}
	return s, nil
}

// Options turns settings into imaging options.
func (s Settings) Options() imaging.EncodeOptions {
	return imaging.EncodeOptions{
		Format: imaging.Format(s.Format), Lossless: s.Lossless, Quality: s.Quality, Effort: s.Effort,
		Progressive: s.Progressive, Chroma444: s.Chroma444, MaxWidth: s.MaxWidth, MaxHeight: s.MaxHeight,
		KeepMetadata: s.KeepMetadata,
	}
}

// Scales reports whether the settings scale images down.
func (s Settings) Scales() bool { return s.MaxWidth > 0 || s.MaxHeight > 0 }

// DefaultMode is replace for lossless re-encodes at full size, where
// nothing visible can change, and review otherwise.
func (s Settings) DefaultMode() string {
	if s.Lossless && !s.Scales() {
		return ModeReplace
	}
	return ModeReview
}

// Describe summarises the settings ("WebP q80, at most 2000×2000").
func (s Settings) Describe() string {
	var b strings.Builder
	switch {
	case s.Format == "png":
		b.WriteString("PNG")
	case s.Format == "webp" && s.Lossless:
		b.WriteString("lossless WebP")
	default:
		fmt.Fprintf(&b, "%s q%d", map[string]string{"jpeg": "JPEG", "webp": "WebP"}[s.Format], s.Quality)
	}
	switch {
	case s.MaxWidth > 0 && s.MaxHeight > 0:
		fmt.Fprintf(&b, ", at most %d×%d", s.MaxWidth, s.MaxHeight)
	case s.MaxWidth > 0:
		fmt.Fprintf(&b, ", at most %d wide", s.MaxWidth)
	case s.MaxHeight > 0:
		fmt.Fprintf(&b, ", at most %d high", s.MaxHeight)
	}
	return b.String()
}

const batchCols = `b.id, b.settings, b.mode, b.description, b.state, b.error, b.job_id, b.created_at, COALESCE(b.finished_at, 0),
	(SELECT count(*) FROM reencode_items i WHERE i.batch_id = b.id),
	(SELECT count(*) FROM reencode_items i WHERE i.batch_id = b.id AND i.status = 'pending'),
	(SELECT count(*) FROM reencode_items i WHERE i.batch_id = b.id AND i.status = 'ready'),
	(SELECT count(*) FROM reencode_items i WHERE i.batch_id = b.id AND i.status = 'replaced'),
	(SELECT count(*) FROM reencode_items i WHERE i.batch_id = b.id AND i.status = 'kept'),
	(SELECT count(*) FROM reencode_items i WHERE i.batch_id = b.id AND i.status = 'skipped'),
	(SELECT count(*) FROM reencode_items i WHERE i.batch_id = b.id AND i.status = 'failed'),
	(SELECT COALESCE(sum(old_size), 0) FROM reencode_items i WHERE i.batch_id = b.id AND i.status = 'ready'),
	(SELECT COALESCE(sum(new_size), 0) FROM reencode_items i WHERE i.batch_id = b.id AND i.status = 'ready'),
	(SELECT COALESCE(sum(old_size), 0) FROM reencode_items i WHERE i.batch_id = b.id AND i.status = 'replaced'),
	(SELECT COALESCE(sum(new_size), 0) FROM reencode_items i WHERE i.batch_id = b.id AND i.status = 'replaced')`

func scanBatch(sc interface{ Scan(...any) error }) (Batch, error) {
	var bt Batch
	var settings string
	c := &bt.Counts
	err := sc.Scan(&bt.ID, &settings, &bt.Mode, &bt.Description, &bt.State, &bt.Error, &bt.JobID, &bt.CreatedAt, &bt.FinishedAt,
		&c.Total, &c.Pending, &c.Ready, &c.Replaced, &c.Kept, &c.Skipped, &c.Failed,
		&bt.ReadyOld, &bt.ReadyNew, &bt.ReplacedOld, &bt.ReplacedNew)
	if err == nil {
		err = json.Unmarshal([]byte(settings), &bt.Settings)
	}
	return bt, err
}

// List returns the batches, newest first.
func List(ctx context.Context, b *bag.Bag) ([]Batch, error) {
	rows, err := b.R.QueryContext(ctx, "SELECT "+batchCols+" FROM reencode_batches b ORDER BY b.id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Batch{}
	for rows.Next() {
		bt, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, bt)
	}
	return out, rows.Err()
}

// Get returns one batch.
func Get(ctx context.Context, b *bag.Bag, id int64) (*Batch, error) {
	bt, err := scanBatch(b.R.QueryRowContext(ctx, "SELECT "+batchCols+" FROM reencode_batches b WHERE b.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &bt, nil
}

// GetDetail returns a batch with its items in order.
func GetDetail(ctx context.Context, b *bag.Bag, id int64) (*Detail, error) {
	bt, err := Get(ctx, b, id)
	if err != nil {
		return nil, err
	}
	rows, err := b.R.QueryContext(ctx, `SELECT it.image_id, im.name, it.ord, it.status, it.reason, it.notes,
		it.old_format, it.old_size, it.old_width, it.old_height,
		it.new_format, it.new_size, it.new_width, it.new_height, COALESCE(it.new_sha256, x''), it.psnr, it.millis
		FROM reencode_items it JOIN images im ON im.id = it.image_id
		WHERE it.batch_id = ? ORDER BY it.ord`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d := &Detail{Batch: *bt, Items: []Item{}}
	for rows.Next() {
		var it Item
		var notes string
		var sha []byte
		var psnr sql.NullFloat64
		if err := rows.Scan(&it.ImageID, &it.Name, &it.Ord, &it.Status, &it.Reason, &notes,
			&it.OldFormat, &it.OldSize, &it.OldWidth, &it.OldHeight,
			&it.NewFormat, &it.NewSize, &it.NewWidth, &it.NewHeight, &sha, &psnr, &it.Millis); err != nil {
			return nil, err
		}
		it.Notes = []string{}
		_ = json.Unmarshal([]byte(notes), &it.Notes)
		if len(sha) > 0 {
			it.NewSHA256 = hex.EncodeToString(sha)
		}
		if psnr.Valid {
			v := psnr.Float64
			it.PSNR = &v
		}
		d.Items = append(d.Items, it)
	}
	return d, rows.Err()
}

// Create makes a batch of images (in the order given). Purged images and
// repeats are left out.
func Create(ctx context.Context, b *bag.Bag, ids []int64, s Settings, mode, description string) (*Batch, error) {
	s, err := s.Normalize()
	if err != nil {
		return nil, err
	}
	if mode == "" {
		mode = s.DefaultMode()
	}
	if mode != ModeReplace && mode != ModeReview {
		return nil, invalidf("unknown mode %q", mode)
	}
	if len(ids) == 0 {
		return nil, invalidf("no images to re-encode")
	}
	if len(ids) > MaxImages {
		return nil, invalidf("at most %d images can be re-encoded in one go; do the rest in another", MaxImages)
	}
	if description == "" {
		description = fmt.Sprintf("%d images", len(ids))
	}
	settings, _ := json.Marshal(s)
	js, _ := json.Marshal(ids)
	var id int64
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO reencode_batches(settings, mode, description, created_at) VALUES (?, ?, ?, ?)`,
			string(settings), mode, description, bag.NowMillis())
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		res, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO reencode_items(batch_id, image_id, ord)
			SELECT ?, i.id, j.key FROM json_each(?) j JOIN images i ON i.id = j.value
			WHERE i.blob_id IS NOT NULL`, id, string(js))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return invalidf("none of these images can be re-encoded")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return Get(ctx, b, id)
}

// SetState records a batch's state (and the job running it).
func SetState(ctx context.Context, b *bag.Bag, id int64, state, jobID, errText string) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		var finished any
		if state == StateFinished || state == StateStopped {
			finished = bag.NowMillis()
		}
		_, err := tx.ExecContext(ctx, `UPDATE reencode_batches SET state = ?, job_id = ?, error = ?, finished_at = ? WHERE id = ?`,
			state, jobID, errText, finished, id)
		return err
	})
}

// SetJob records the job working on a batch.
func SetJob(ctx context.Context, b *bag.Bag, id int64, jobID string) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE reencode_batches SET job_id = ? WHERE id = ?", jobID, id)
		return err
	})
}

// setState records a batch's state, keeping its job.
func setState(ctx context.Context, b *bag.Bag, id int64, state, errText string) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		var finished any
		if state == StateFinished || state == StateStopped {
			finished = bag.NowMillis()
		}
		_, err := tx.ExecContext(ctx, `UPDATE reencode_batches SET state = ?, error = ?, finished_at = ? WHERE id = ?`,
			state, errText, finished, id)
		return err
	})
}

// Interrupted marks batches left queued or running (by a server that
// stopped) as stopped, so they can be resumed.
func Interrupted(ctx context.Context, b *bag.Bag) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE reencode_batches SET state = ?, job_id = '' WHERE state IN (?, ?)`,
			StateStopped, StateQueued, StateRunning)
		return err
	})
}

// renamed swaps a name's extension for the new format's, when it had the
// old format's one.
func renamed(name string, from, to imaging.Format) string {
	ext := path.Ext(name)
	if ext == "" || from == to {
		return name
	}
	if !slices.Contains(from.Extensions(), strings.ToLower(ext)) {
		return name
	}
	return strings.TrimSuffix(name, ext) + to.Extension()
}

// dropResult forgets an item's result, deleting its blob unless something
// else uses it.
func dropResult(ctx context.Context, tx *sql.Tx, batch, image int64, status, reason string) error {
	var blob sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT new_blob_id FROM reencode_items WHERE batch_id = ? AND image_id = ?", batch, image).Scan(&blob); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE reencode_items SET status = ?, reason = ?, new_blob_id = NULL, decided_at = ?
		WHERE batch_id = ? AND image_id = ?`, status, reason, bag.NowMillis(), batch, image); err != nil {
		return err
	}
	if blob.Valid {
		_, err := tx.ExecContext(ctx, "DELETE FROM blobs WHERE id = ? AND "+library.UnusedBlob, blob.Int64)
		return err
	}
	return nil
}

// replace swaps an item's result in for the original. It reports false
// (and drops the result) if the image changed since the result was made.
func replace(ctx context.Context, tx *sql.Tx, batch, image int64, settings string) (bool, error) {
	var it struct {
		oldBlob, newBlob       sql.NullInt64
		oldFormat, newFormat   string
		oldSize, newSize       int64
		oldW, oldH, newW, newH int
		newSHA                 []byte
	}
	err := tx.QueryRowContext(ctx, `SELECT old_blob_id, new_blob_id, old_format, new_format, old_size, new_size,
		old_width, old_height, new_width, new_height, new_sha256
		FROM reencode_items WHERE batch_id = ? AND image_id = ? AND status = 'ready'`, batch, image).Scan(
		&it.oldBlob, &it.newBlob, &it.oldFormat, &it.newFormat, &it.oldSize, &it.newSize,
		&it.oldW, &it.oldH, &it.newW, &it.newH, &it.newSHA)
	if err != nil {
		return false, err
	}
	var cur sql.NullInt64
	var name string
	var oldSHA []byte
	if err := tx.QueryRowContext(ctx, "SELECT blob_id, name, sha256 FROM images WHERE id = ?", image).Scan(&cur, &name, &oldSHA); err != nil {
		return false, err
	}
	if !cur.Valid || cur.Int64 != it.oldBlob.Int64 || !it.newBlob.Valid {
		return false, dropResult(ctx, tx, batch, image, Skipped, "the image changed after this result was made")
	}
	newName := renamed(name, imaging.Format(it.oldFormat), imaging.Format(it.newFormat))
	if _, err := tx.ExecContext(ctx, `UPDATE images SET blob_id = ?, sha256 = ?, format = ?, size = ?, width = ?, height = ?,
		orientation = 1, name = ? WHERE id = ?`,
		it.newBlob.Int64, it.newSHA, it.newFormat, it.newSize, it.newW, it.newH, newName, image); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO image_reencodes(image_id, batch_id, old_sha256, old_format, old_size, old_width,
		old_height, new_sha256, new_format, new_size, new_width, new_height, settings, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		image, batch, oldSHA, it.oldFormat, it.oldSize, it.oldW, it.oldH, it.newSHA, it.newFormat, it.newSize, it.newW, it.newH,
		settings, bag.NowMillis()); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE reencode_items SET status = 'replaced', new_blob_id = NULL, decided_at = ?
		WHERE batch_id = ? AND image_id = ?`, bag.NowMillis(), batch, image); err != nil {
		return false, err
	}
	// The original goes, unless another image (a duplicate) shares it.
	_, err = tx.ExecContext(ctx, "DELETE FROM blobs WHERE id = ? AND "+library.UnusedBlob, it.oldBlob.Int64)
	return true, err
}

// Decide replaces originals with their results (replace) or keeps the
// originals and drops the results, for the batch's items awaiting review:
// those of ids, or all of them when ids is empty.
func Decide(ctx context.Context, b *bag.Bag, id int64, ids []int64, replaceThem bool) (Decided, error) {
	var d Decided
	var settings string
	if err := b.R.QueryRowContext(ctx, "SELECT settings FROM reencode_batches WHERE id = ?", id).Scan(&settings); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return d, ErrNotFound
		}
		return d, err
	}
	q := "SELECT image_id FROM reencode_items WHERE batch_id = ? AND status = 'ready'"
	args := []any{id}
	if len(ids) > 0 {
		js, _ := json.Marshal(ids)
		q += " AND image_id IN (SELECT value FROM json_each(?))"
		args = append(args, string(js))
	}
	rows, err := b.R.QueryContext(ctx, q+" ORDER BY ord", args...)
	if err != nil {
		return d, err
	}
	var todo []int64
	for rows.Next() {
		var image int64
		if err := rows.Scan(&image); err != nil {
			rows.Close()
			return d, err
		}
		todo = append(todo, image)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}
	for chunk := range slices.Chunk(todo, 200) {
		err := b.Tx(ctx, func(tx *sql.Tx) error {
			for _, image := range chunk {
				if !replaceThem {
					if err := dropResult(ctx, tx, id, image, Kept, ""); err != nil {
						return err
					}
					d.Kept++
					continue
				}
				ok, err := replace(ctx, tx, id, image, settings)
				if err != nil {
					return err
				}
				if ok {
					d.Replaced++
				} else {
					d.Stale++
				}
			}
			return nil
		})
		if err != nil {
			return d, err
		}
	}
	if len(todo) > 0 {
		_, err = b.W.ExecContext(ctx, "PRAGMA incremental_vacuum")
	}
	return d, err
}

// Delete removes a batch. Results still awaiting review are dropped (the
// originals stay); replacements already made stay made.
func Delete(ctx context.Context, b *bag.Bag, id int64) error {
	bt, err := Get(ctx, b, id)
	if err != nil {
		return err
	}
	if bt.State == StateRunning || bt.State == StateQueued {
		return fmt.Errorf("the batch is still running: %w", ErrBusy)
	}
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS reencode_drop(id INTEGER PRIMARY KEY)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM temp.reencode_drop`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO temp.reencode_drop
			SELECT new_blob_id FROM reencode_items WHERE batch_id = ? AND new_blob_id IS NOT NULL`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM reencode_batches WHERE id = ?", id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM blobs WHERE id IN (SELECT id FROM temp.reencode_drop) AND "+library.UnusedBlob)
		return err
	})
	if err != nil {
		return err
	}
	_, err = b.W.ExecContext(ctx, "PRAGMA incremental_vacuum")
	return err
}

// History lists the replacements made to an image, oldest first.
func History(ctx context.Context, b *bag.Bag, image int64) ([]Change, error) {
	rows, err := b.R.QueryContext(ctx, `SELECT old_format, old_size, old_width, old_height, new_format, new_size, new_width,
		new_height, COALESCE(batch_id, 0), created_at FROM image_reencodes WHERE image_id = ? ORDER BY id`, image)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.OldFormat, &c.OldSize, &c.OldWidth, &c.OldHeight, &c.NewFormat, &c.NewSize, &c.NewWidth,
			&c.NewHeight, &c.BatchID, &c.At); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Result returns the bytes and format of an item's result awaiting review.
func Result(ctx context.Context, b *bag.Bag, batch, image int64) ([]byte, imaging.Format, error) {
	var data []byte
	var format string
	err := b.R.QueryRowContext(ctx, `SELECT bl.data, it.new_format FROM reencode_items it JOIN blobs bl ON bl.id = it.new_blob_id
		WHERE it.batch_id = ? AND it.image_id = ?`, batch, image).Scan(&data, &format)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	return data, imaging.Format(format), err
}
