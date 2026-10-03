package reencode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"time"

	"photobag/internal/bag"
	"photobag/internal/imaging"
	"photobag/internal/library"
)

// memoryBudget bounds the decoded pixels in flight across workers.
const memoryBudget = 2 << 30

type work struct {
	image  int64
	name   string
	blob   int64
	format imaging.Format
	size   int64
	w, h   int
	sha    []byte
}

// budget is a counting semaphore over bytes.
type budget struct {
	mu    sync.Mutex
	cond  *sync.Cond
	avail int64
}

func newBudget(n int64) *budget {
	b := &budget{avail: n}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *budget) acquire(n int64) {
	b.mu.Lock()
	for b.avail < n {
		b.cond.Wait()
	}
	b.avail -= n
	b.mu.Unlock()
}

func (b *budget) release(n int64) {
	b.mu.Lock()
	b.avail += n
	b.cond.Broadcast()
	b.mu.Unlock()
}

// Run re-encodes a batch's pending images. In replace mode each result
// replaces its original as soon as it is made. Cancelling ctx stops after
// the images being worked on; the rest stay pending, and the batch is
// left stopped, to be run again later.
func Run(ctx context.Context, b *bag.Bag, id int64, progress func(Progress)) (Progress, error) {
	if err := setState(ctx, b, id, StateRunning, ""); err != nil {
		return Progress{}, err
	}
	prog, err := run(ctx, b, id, progress)
	state, msg := StateFinished, ""
	switch {
	case err != nil && !errors.Is(err, context.Canceled):
		state, msg = StateStopped, err.Error()
	case err != nil || prog.Done < prog.Total:
		state = StateStopped
	}
	if serr := setState(context.Background(), b, id, state, msg); err == nil {
		err = serr
	}
	return prog, err
}

func run(ctx context.Context, b *bag.Bag, id int64, progress func(Progress)) (Progress, error) {
	var prog Progress
	if progress == nil {
		progress = func(Progress) {}
	}
	bt, err := Get(ctx, b, id)
	if err != nil {
		return prog, err
	}
	settingsJSON, _ := json.Marshal(bt.Settings)
	c := bt.Counts
	prog = Progress{Total: c.Total, Done: c.Total - c.Pending, Ready: c.Ready, Replaced: c.Replaced,
		Skipped: c.Skipped + c.Kept, Failed: c.Failed, OldBytes: bt.ReplacedOld, NewBytes: bt.ReplacedNew}
	progress(prog)

	rows, err := b.R.QueryContext(ctx, `SELECT it.image_id, im.name, COALESCE(im.blob_id, 0), im.format, im.size,
		im.width, im.height, im.sha256
		FROM reencode_items it JOIN images im ON im.id = it.image_id
		WHERE it.batch_id = ? AND it.status = 'pending' ORDER BY it.ord`, id)
	if err != nil {
		return prog, err
	}
	var todo []work
	for rows.Next() {
		var w work
		var format string
		if err := rows.Scan(&w.image, &w.name, &w.blob, &format, &w.size, &w.w, &w.h, &w.sha); err != nil {
			rows.Close()
			return prog, err
		}
		w.format = imaging.Format(format)
		todo = append(todo, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return prog, err
	}

	var mu sync.Mutex
	var writeErr error
	last := time.Now()
	jobs := make(chan work)
	mem := newBudget(memoryBudget)
	var wg sync.WaitGroup
	for range max(1, runtime.NumCPU()/2) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for w := range jobs {
				need := min(w.size+int64(w.w)*int64(w.h)*4*5, memoryBudget)
				mem.acquire(need)
				status, saved, err := process(ctx, b, id, w, bt.Settings, bt.Mode, string(settingsJSON))
				mem.release(need)
				mu.Lock()
				if err != nil && writeErr == nil {
					writeErr = err
				}
				if status == Pending { // stopped before it was done
					mu.Unlock()
					continue
				}
				prog.Done++
				prog.Current = w.name
				switch status {
				case Ready:
					prog.Ready++
				case Replaced:
					prog.Replaced++
					prog.OldBytes += w.size
					prog.NewBytes += saved
				case Skipped:
					prog.Skipped++
				case Failed:
					prog.Failed++
				}
				if time.Since(last) > 300*time.Millisecond {
					progress(prog)
					last = time.Now()
				}
				mu.Unlock()
			}
		}()
	}
	for _, w := range todo {
		mu.Lock()
		stop := writeErr != nil
		mu.Unlock()
		if stop || ctx.Err() != nil {
			break
		}
		jobs <- w
	}
	close(jobs)
	wg.Wait()
	prog.Current = ""
	progress(prog)
	if prog.Replaced > c.Replaced {
		if _, err := b.W.ExecContext(context.Background(), "PRAGMA incremental_vacuum"); err != nil && writeErr == nil {
			writeErr = err
		}
	}
	if writeErr != nil {
		return prog, writeErr
	}
	return prog, ctx.Err()
}

// process re-encodes one image and records the outcome; it returns the
// item's new status and, for a replacement, the new size. Only database
// failures are returned as errors. Once ctx is cancelled it leaves the
// image pending; a result already made is still recorded.
func process(ctx context.Context, b *bag.Bag, batch int64, w work, s Settings, mode, settings string) (string, int64, error) {
	if ctx.Err() != nil {
		return Pending, 0, nil
	}
	start := time.Now()
	bg := context.Background()
	finish := func(status, reason string) (string, int64, error) {
		err := b.Tx(bg, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(bg, `UPDATE reencode_items SET status = ?, reason = ?, old_blob_id = ?, old_format = ?,
				old_size = ?, old_width = ?, old_height = ?, millis = ? WHERE batch_id = ? AND image_id = ?`,
				status, reason, nullable(w.blob), string(w.format), w.size, w.w, w.h, time.Since(start).Milliseconds(), batch, w.image)
			return err
		})
		return status, 0, err
	}
	if w.blob == 0 {
		return finish(Skipped, "the image was deleted")
	}
	var data []byte
	if err := b.R.QueryRowContext(ctx, "SELECT data FROM blobs WHERE id = ?", w.blob).Scan(&data); err != nil {
		if ctx.Err() != nil {
			return Pending, 0, nil
		}
		if errors.Is(err, sql.ErrNoRows) {
			return finish(Skipped, "the image was deleted")
		}
		return finish(Failed, err.Error())
	}
	r, err := imaging.Reencode(w.format, data, s.Options())
	data = nil
	switch {
	case errors.Is(err, imaging.ErrAnimated), errors.Is(err, imaging.ErrCMYK):
		return finish(Skipped, err.Error())
	case err != nil:
		return finish(Failed, err.Error())
	}
	sum := sha256.Sum256(r.Data)
	if bytes.Equal(sum[:], w.sha) {
		return finish(Skipped, "the result is identical to the original")
	}
	if s.OnlySmaller && int64(len(r.Data)) >= w.size {
		return finish(Skipped, fmt.Sprintf("not smaller (%s → %s)", humanSize(w.size), humanSize(int64(len(r.Data)))))
	}

	notes, _ := json.Marshal(append([]string{}, r.Notes...))
	var psnr any
	if !r.Exact && !math.IsInf(r.PSNR, 0) && !math.IsNaN(r.PSNR) {
		psnr = math.Round(r.PSNR*100) / 100
	}
	status := Ready
	ctx = bg // from here on the result is recorded, even when stopping
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		// The image may have changed (or gone) while this was made.
		var cur sql.NullInt64
		if err := tx.QueryRowContext(ctx, "SELECT blob_id FROM images WHERE id = ?", w.image).Scan(&cur); err != nil {
			return err
		}
		if !cur.Valid || cur.Int64 != w.blob {
			status = Skipped
			_, err := tx.ExecContext(ctx, `UPDATE reencode_items SET status = 'skipped', reason = 'the image changed meanwhile'
				WHERE batch_id = ? AND image_id = ?`, batch, w.image)
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO blobs(sha256, size, data) VALUES (?, ?, ?) ON CONFLICT(sha256) DO NOTHING",
			sum[:], len(r.Data), r.Data); err != nil {
			return err
		}
		var blob int64
		if err := tx.QueryRowContext(ctx, "SELECT id FROM blobs WHERE sha256 = ?", sum[:]).Scan(&blob); err != nil {
			return err
		}
		res := r.Result
		if err := library.SaveThumb(ctx, tx, b, blob, sum[:], res); err != nil {
			return err
		}
		fp := res.Fingerprint
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO fingerprints(blob_id, version, phash, color, aspect, ac_energy)
			VALUES (?, ?, ?, ?, ?, ?)`, blob, fp.Version, int64(fp.PHash), fp.Color[:], fp.Aspect, fp.ACEnergy); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE reencode_items SET status = 'ready', reason = '', notes = ?,
			old_blob_id = ?, old_format = ?, old_size = ?, old_width = ?, old_height = ?,
			new_blob_id = ?, new_sha256 = ?, new_format = ?, new_size = ?, new_width = ?, new_height = ?, psnr = ?, millis = ?
			WHERE batch_id = ? AND image_id = ?`,
			string(notes), w.blob, string(w.format), w.size, w.w, w.h,
			blob, sum[:], string(r.Format), len(r.Data), r.Width, r.Height, psnr, time.Since(start).Milliseconds(),
			batch, w.image); err != nil {
			return err
		}
		if mode != ModeReplace {
			return nil
		}
		ok, err := replace(ctx, tx, batch, w.image, settings)
		if ok {
			status = Replaced
		} else if err == nil {
			status = Skipped
		}
		return err
	})
	if err != nil {
		return Failed, 0, err
	}
	b.Checkpoint(context.Background())
	return status, int64(len(r.Data)), nil
}

func nullable(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Try re-encodes up to a few images without keeping anything, to show
// what settings would do.
func Try(ctx context.Context, b *bag.Bag, ids []int64, s Settings) (*Estimate, error) {
	s, err := s.Normalize()
	if err != nil {
		return nil, err
	}
	est := &Estimate{Problems: []string{}, Exact: true}
	start := time.Now()
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		var data []byte
		var format, name string
		err := b.R.QueryRowContext(ctx, `SELECT bl.data, im.format, im.name FROM images im JOIN blobs bl ON bl.id = im.blob_id
			WHERE im.id = ?`, id).Scan(&data, &format, &name)
		if err != nil {
			continue
		}
		r, err := imaging.Reencode(imaging.Format(format), data, s.Options())
		if err != nil {
			est.Problems = append(est.Problems, name+": "+err.Error())
			continue
		}
		est.Tried++
		est.OldBytes += int64(len(data))
		est.NewBytes += int64(len(r.Data))
		if !r.Exact {
			est.Exact = false
			p := math.Round(r.PSNR*10) / 10
			if est.MinPSNR == nil || p < *est.MinPSNR {
				est.MinPSNR = &p
			}
		}
	}
	est.Millis = time.Since(start).Milliseconds()
	if est.Tried == 0 {
		est.Exact = false
	}
	return est, nil
}
