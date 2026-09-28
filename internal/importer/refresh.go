package importer

import (
	"context"
	"database/sql"
	"runtime"
	"sync"

	"photobag/internal/bag"
	"photobag/internal/imaging"
)

// RefreshReport summarises a derivative refresh.
type RefreshReport struct {
	Checked int `json:"checked"`
	Updated int `json:"updated"`
	Failed  int `json:"failed"`
}

// Outdated counts blobs whose thumbnail or thumbprint is missing or was
// made by an older thumbprint version.
func Outdated(ctx context.Context, b *bag.Bag) (int, error) {
	var n int
	err := b.R.QueryRowContext(ctx, outdatedSQL("count(*)"), imaging.FingerprintVersion).Scan(&n)
	return n, err
}

func outdatedSQL(cols string) string {
	return `SELECT ` + cols + ` FROM blobs bl
		LEFT JOIN fingerprints f ON f.blob_id = bl.id
		LEFT JOIN thumbnails th ON th.blob_id = bl.id
		WHERE f.blob_id IS NULL OR th.blob_id IS NULL OR f.version < ?`
}

// Refresh regenerates missing or outdated thumbnails and thumbprints, for
// example after the thumbprint algorithm changes.
func Refresh(ctx context.Context, b *bag.Bag, progress func(done, total int)) (*RefreshReport, error) {
	if progress == nil {
		progress = func(int, int) {}
	}
	rows, err := b.R.QueryContext(ctx, outdatedSQL("bl.id"), imaging.FingerprintVersion)
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
				var data []byte
				var format string
				err := b.R.QueryRowContext(ctx, `SELECT bl.data, i.format FROM blobs bl
					JOIN images i ON i.blob_id = bl.id WHERE bl.id = ? LIMIT 1`, id).Scan(&data, &format)
				var r *imaging.Result
				if err == nil {
					r, err = imaging.Process(imaging.Format(format), data)
				}
				if err != nil {
					r = nil
				}
				results <- result{id, r}
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
				if _, err := tx.ExecContext(ctx, `INSERT INTO thumbnails(blob_id, width, height, data) VALUES (?, ?, ?, ?)
					ON CONFLICT(blob_id) DO UPDATE SET width = excluded.width, height = excluded.height, data = excluded.data`,
					r.blob, r.res.ThumbW, r.res.ThumbH, r.res.Thumb); err != nil {
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
