package importer

import (
	"context"
	"database/sql"
	"runtime"
	"sync"

	"photobag/internal/bag"
	"photobag/internal/imaging"
	"photobag/internal/library"
)

// RefreshReport summarises a derivative refresh.
type RefreshReport struct {
	Checked int `json:"checked"`
	Updated int `json:"updated"`
	Failed  int `json:"failed"`
}

// Outdated counts blobs whose thumbprint is missing or was made by an
// older thumbprint version, or whose thumbnail is missing from a bag that
// stores them.
func Outdated(ctx context.Context, b *bag.Bag) (int, error) {
	q, args, err := outdatedSQL(ctx, b, "count(*)")
	if err != nil {
		return 0, err
	}
	var n int
	err = b.R.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

func outdatedSQL(ctx context.Context, b *bag.Bag, cols string) (string, []any, error) {
	mode, err := library.GetThumbMode(ctx, b)
	if err != nil {
		return "", nil, err
	}
	return `SELECT ` + cols + ` FROM blobs bl
		LEFT JOIN fingerprints f ON f.blob_id = bl.id
		LEFT JOIN thumbnails th ON th.blob_id = bl.id
		WHERE f.blob_id IS NULL OR f.version < ? OR (th.blob_id IS NULL AND ?)`,
		[]any{imaging.FingerprintVersion, mode == library.ThumbsStored}, nil
}

// Refresh regenerates missing or outdated thumbnails and thumbprints, for
// example after the thumbprint algorithm changes.
func Refresh(ctx context.Context, b *bag.Bag, progress func(done, total int)) (*RefreshReport, error) {
	if progress == nil {
		progress = func(int, int) {}
	}
	q, args, err := outdatedSQL(ctx, b, "bl.id")
	if err != nil {
		return nil, err
	}
	rows, err := b.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	rep := &RefreshReport{Checked: len(ids)}
	progress(0, len(ids))

	type result struct {
		blob int64
		sha  []byte
		res  *imaging.Result
	}
	jobs := make(chan int64)
	results := make(chan result)
	var wg sync.WaitGroup
	for w := 0; w < max(1, runtime.NumCPU()/2); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				// Blobs of generated images and of re-encoded versions have
				// no image row, so the format is read from the bytes.
				var sha, data []byte
				err := b.R.QueryRowContext(ctx, "SELECT sha256, data FROM blobs WHERE id = ?", id).Scan(&sha, &data)
				var r *imaging.Result
				if err == nil {
					var f imaging.Format
					if f, err = imaging.Identify(data); err == nil {
						r, err = imaging.Process(f, data)
					}
				}
				if err != nil {
					r = nil
				}
				results <- result{id, sha, r}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, id := range ids {
			select {
			case jobs <- id:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	done := 0
	var firstErr error
	for r := range results {
		done++
		switch {
		case firstErr != nil:
			// keep draining so workers can exit
		case r.res == nil:
			rep.Failed++
		default:
			fp := r.res.Fingerprint
			err := b.Tx(ctx, func(tx *sql.Tx) error {
				if err := library.SaveThumb(ctx, tx, b, r.blob, r.sha, r.res); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `INSERT INTO fingerprints(blob_id, version, phash, color, aspect, ac_energy)
					VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(blob_id) DO UPDATE SET version = excluded.version,
					phash = excluded.phash, color = excluded.color, aspect = excluded.aspect, ac_energy = excluded.ac_energy`,
					r.blob, fp.Version, int64(fp.PHash), fp.Color[:], fp.Aspect, fp.ACEnergy)
				return err
			})
			if err != nil {
				firstErr = err
			} else {
				rep.Updated++
			}
		}
		if done%20 == 0 || done == len(ids) {
			progress(done, len(ids))
		}
	}
	if firstErr != nil {
		return rep, firstErr
	}
	return rep, ctx.Err()
}
