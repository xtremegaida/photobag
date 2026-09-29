package experiments

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"photobag/internal/bag"
	"photobag/internal/comfy"
	"photobag/internal/imaging"
	"photobag/internal/library"
)

const generationCols = `g.id, COALESCE(g.experiment_id, 0), COALESCE(g.run_id, 0), g.version_id, COALESCE(g.workflow_id, 0),
	g.workflow_name, g.applied, g.combo, g.repeat, g.batch_index, g.seed, g.sha256, g.format, g.size, g.width, g.height,
	COALESCE(th.width, 0), COALESCE(th.height, 0), g.millis, g.created_at, COALESCE(g.image_id, 0), COALESCE(e.name, '')`

const generationFrom = ` FROM generations g
	LEFT JOIN blobs bl ON bl.sha256 = g.sha256
	LEFT JOIN thumbnails th ON th.blob_id = bl.id
	LEFT JOIN experiments e ON e.id = g.experiment_id`

func scanGeneration(sc interface{ Scan(...any) error }) (Generation, error) {
	var g Generation
	var applied string
	var seed sql.NullInt64
	var sha []byte
	err := sc.Scan(&g.ID, &g.ExperimentID, &g.RunID, &g.VersionID, &g.WorkflowID, &g.WorkflowName, &applied,
		&g.Combo, &g.Repeat, &g.BatchIndex, &seed, &sha, &g.Format, &g.Size, &g.Width, &g.Height,
		&g.ThumbW, &g.ThumbH, &g.Millis, &g.CreatedAt, &g.ImageID, &g.ExperimentName)
	if err != nil {
		return g, err
	}
	g.SHA256 = hex.EncodeToString(sha)
	if seed.Valid {
		g.Seed = &seed.Int64
	}
	if json.Unmarshal([]byte(applied), &g.Applied) != nil || g.Applied == nil {
		g.Applied = []comfy.Applied{}
	}
	return g, nil
}

// ListGenerations returns an experiment's images in the order they were
// made: those it holds, and with moved also those moved to the library.
func ListGenerations(ctx context.Context, b *bag.Bag, experimentID int64, moved bool) ([]Generation, error) {
	cond := " AND g.image_id IS NULL"
	if moved {
		cond = ""
	}
	rows, err := b.R.QueryContext(ctx, `SELECT `+generationCols+generationFrom+` WHERE g.experiment_id = ?`+cond+` ORDER BY g.id`, experimentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Generation{}
	for rows.Next() {
		g, err := scanGeneration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGeneration returns a generated image.
func GetGeneration(ctx context.Context, b *bag.Bag, id int64) (*Generation, error) {
	g, err := scanGeneration(b.R.QueryRowContext(ctx, `SELECT `+generationCols+generationFrom+` WHERE g.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("generated image %d: %w", id, ErrNotFound)
	}
	return &g, err
}

// ForImage returns how a library image was generated, or nil.
func ForImage(ctx context.Context, b *bag.Bag, imageID int64) (*Generation, error) {
	g, err := scanGeneration(b.R.QueryRowContext(ctx, `SELECT `+generationCols+generationFrom+` WHERE g.image_id = ?`, imageID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &g, err
}

// Blob locates a generated image's bytes (held, or in the library).
func Blob(ctx context.Context, b *bag.Bag, id int64) (blobID int64, format string, err error) {
	err = b.R.QueryRowContext(ctx, `SELECT COALESCE(g.blob_id, i.blob_id), g.format FROM generations g
		LEFT JOIN images i ON i.id = g.image_id WHERE g.id = ? AND COALESCE(g.blob_id, i.blob_id) IS NOT NULL`, id).Scan(&blobID, &format)
	if errors.Is(err, sql.ErrNoRows) {
		err = fmt.Errorf("generated image %d: %w", id, ErrNotFound)
	}
	return
}

// Effective returns the workflow exactly as an image was made: its version
// with the applied values set.
func Effective(ctx context.Context, b *bag.Bag, id int64) (*comfy.Workflow, *Generation, error) {
	g, err := GetGeneration(ctx, b, id)
	if err != nil {
		return nil, nil, err
	}
	w, err := loadVersion(ctx, b, g.VersionID)
	if err != nil {
		return nil, nil, err
	}
	eff, err := w.Apply(g.Applied)
	return eff, g, err
}

// SaveEffective stores the workflow an image was made with as a new
// template.
func SaveEffective(ctx context.Context, b *bag.Bag, id int64, name string) (*WorkflowDetail, error) {
	w, _, err := Effective(ctx, b, id)
	if err != nil {
		return nil, err
	}
	js := string(w.JSON())
	return SaveWorkflow(ctx, b, 0, WorkflowChange{Name: &name, JSON: &js})
}

// discardTx deletes held generations matching cond and the bytes only
// they used. It returns how many went.
func discardTx(ctx context.Context, tx *sql.Tx, cond string, args ...any) (int, error) {
	if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS discard_blobs(id INTEGER PRIMARY KEY)"); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM temp.discard_blobs"); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO temp.discard_blobs
		SELECT blob_id FROM generations WHERE image_id IS NULL AND blob_id IS NOT NULL AND `+cond, args...); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM generations WHERE image_id IS NULL AND "+cond, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `DELETE FROM blobs WHERE id IN (SELECT id FROM temp.discard_blobs) AND `+library.UnusedBlob); err != nil {
		return 0, err
	}
	return int(n), gcVersions(ctx, tx)
}

// gcVersions deletes workflow versions nothing refers to.
func gcVersions(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM workflow_versions
		WHERE id NOT IN (SELECT version_id FROM workflows)
		AND id NOT IN (SELECT version_id FROM generation_runs)
		AND id NOT IN (SELECT version_id FROM generations)`)
	return err
}

// Discard deletes held images for good. Images already in the library
// are left alone.
func Discard(ctx context.Context, b *bag.Bag, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	js, _ := json.Marshal(ids)
	var n int
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = discardTx(ctx, tx, "id IN (SELECT value FROM json_each(?))", string(js))
		return err
	})
	if err != nil {
		return 0, err
	}
	_, err = b.W.ExecContext(ctx, "PRAGMA incremental_vacuum")
	return n, err
}

var unsafeName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)

// libraryName names a moved image: "<experiment> 0042.png", and returns
// the cleaned experiment name.
func libraryName(experiment string, id int64, format string) (name, base string) {
	base = strings.TrimSpace(unsafeName.ReplaceAllString(experiment, " "))
	base = strings.Trim(strings.Join(strings.Fields(base), " "), ". ")
	if base == "" {
		base = "ComfyUI"
	}
	if r := []rune(base); len(r) > 80 {
		base = string(r[:80])
	}
	return fmt.Sprintf("%s %04d%s", base, id, imaging.Format(format).Extension()), base
}

// Move puts held images into the library, tagged with tags. The images
// keep how they were made (see ForImage).
func Move(ctx context.Context, b *bag.Bag, ids []int64, tags []string) (MoveResult, error) {
	res := MoveResult{ImageIDs: []int64{}}
	if len(ids) == 0 {
		return res, nil
	}
	for _, t := range tags {
		if err := bag.ValidateLabel(t); err != nil {
			return res, invalid{err}
		}
	}
	js, _ := json.Marshal(ids)
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		res = MoveResult{ImageIDs: []int64{}}
		tagIDs, err := library.EnsureTags(ctx, tx, tags)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT g.id, g.sha256, g.blob_id, g.format, g.size, g.width, g.height, g.created_at,
			COALESCE(e.name, '') FROM generations g LEFT JOIN experiments e ON e.id = g.experiment_id
			WHERE g.id IN (SELECT value FROM json_each(?)) AND g.image_id IS NULL AND g.blob_id IS NOT NULL ORDER BY g.id`, string(js))
		if err != nil {
			return err
		}
		type held struct {
			id, blob, size, created int64
			sha                     []byte
			format, experiment      string
			w, h                    int
		}
		var hs []held
		for rows.Next() {
			var h held
			if err := rows.Scan(&h.id, &h.sha, &h.blob, &h.format, &h.size, &h.w, &h.h, &h.created, &h.experiment); err != nil {
				rows.Close()
				return err
			}
			hs = append(hs, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		now := bag.NowMillis()
		for _, h := range hs {
			uid, err := uuid.NewV7()
			if err != nil {
				return err
			}
			name, dir := libraryName(h.experiment, h.id, h.format)
			taken := time.UnixMilli(h.created).Format("2006-01-02T15:04:05")
			r, err := tx.ExecContext(ctx, `INSERT INTO images(uid, sha256, blob_id, name, original_name, original_path, import_id,
				format, size, width, height, orientation, file_mtime, taken_at, taken_offset, imported_at)
				VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
				uid.String(), h.sha, h.blob, name, name, "ComfyUI/"+dir+"/"+name,
				h.format, h.size, h.w, h.h, h.created, taken, time.UnixMilli(h.created).Format("-07:00"), now)
			if err != nil {
				return err
			}
			imageID, _ := r.LastInsertId()
			if _, err := tx.ExecContext(ctx, "UPDATE generations SET image_id = ?, blob_id = NULL WHERE id = ?", imageID, h.id); err != nil {
				return err
			}
			res.ImageIDs = append(res.ImageIDs, imageID)
		}
		res.Moved = len(res.ImageIDs)
		return library.TagImages(ctx, tx, res.ImageIDs, tagIDs)
	})
	return res, err
}
