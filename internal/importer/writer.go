package importer

import (
	"context"
	"database/sql"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"photobag/internal/bag"
	"photobag/internal/library"
)

const (
	batchBytes = 128 << 20
	batchItems = 100
	batchIdle  = 250 * time.Millisecond
)

// writer drains items into batched transactions until the channel closes.
func (r *run) writer(items <-chan *item) {
	var batch []*item
	var bytes int64
	timer := time.NewTimer(batchIdle)
	defer timer.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		r.flush(batch)
		for _, it := range batch {
			r.sem.Release(it.mem)
		}
		batch, bytes = nil, 0
	}
	for {
		select {
		case it, ok := <-items:
			if !ok {
				flush()
				return
			}
			batch = append(batch, it)
			bytes += int64(len(it.data))
			if bytes >= batchBytes || len(batch) >= batchItems {
				flush()
			}
		case <-timer.C:
			flush()
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(batchIdle)
	}
}

func (r *run) flush(batch []*item) {
	ctx := context.Background() // finish in-flight work even when cancelled
	if r.writeErr != nil {
		for _, it := range batch {
			r.fileDone(it.f, failed("not written: "+r.writeErr.Error()))
		}
		return
	}
	var added []*item
	err := r.b.Tx(ctx, func(tx *sql.Tx) error {
		added = added[:0]
		for _, it := range batch {
			if err := r.insert(ctx, tx, it); err != nil {
				return err
			}
			added = append(added, it)
		}
		return nil
	})
	if err != nil {
		r.writeErr = err
		for _, it := range batch {
			r.fileDone(it.f, failed("database write failed: "+err.Error()))
		}
		return
	}
	r.mu.Lock()
	for _, it := range added {
		r.blobs[it.sha] = true
	}
	r.report.Added += len(added)
	r.prog.Added += len(added)
	r.mu.Unlock()
	for _, it := range added {
		r.fileDone(it.f, nil)
	}
	r.b.Checkpoint(ctx)
}

func (r *run) insert(ctx context.Context, tx *sql.Tx, it *item) error {
	if it.data != nil {
		if _, err := tx.ExecContext(ctx, "INSERT INTO blobs(sha256, size, data) VALUES (?, ?, ?) ON CONFLICT(sha256) DO NOTHING",
			it.sha[:], len(it.data), it.data); err != nil {
			return err
		}
	}
	var blobID int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM blobs WHERE sha256 = ?", it.sha[:]).Scan(&blobID); err != nil {
		return err
	}
	if res := it.res; res != nil {
		if err := library.SaveThumb(ctx, tx, r.b, blobID, it.sha[:], res); err != nil {
			return err
		}
		fp := res.Fingerprint
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO fingerprints(blob_id, version, phash, color, aspect, ac_energy)
			VALUES (?, ?, ?, ?, ?, ?)`, blobID, fp.Version, int64(fp.PHash), fp.Color[:], fp.Aspect, fp.ACEnergy); err != nil {
			return err
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	name := path.Base(it.f.rel)
	res, err := tx.ExecContext(ctx, `INSERT INTO images(uid, sha256, blob_id, name, original_name, original_path, import_id,
		format, size, width, height, orientation, file_mtime, taken_at, taken_offset, imported_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id.String(), it.sha[:], blobID, name, name, it.f.rel, r.importID,
		string(it.format), it.f.size, it.width, it.height, it.meta.Orientation, it.f.mtime,
		nullIfEmpty(it.meta.TakenAt), nullIfEmpty(it.meta.TakenOffset), bag.NowMillis())
	if err != nil {
		return err
	}
	imageID, _ := res.LastInsertId()
	tagIDs := r.tagIDs
	if r.opts.TagFolders {
		dir := path.Dir(it.f.rel)
		if dir != "." {
			var names []string
			for _, part := range strings.Split(dir, "/") {
				if bag.ValidateLabel(part) == nil {
					names = append(names, part)
				}
			}
			ids, err := r.folderTags(ctx, tx, names)
			if err != nil {
				return err
			}
			tagIDs = append(append([]int64{}, tagIDs...), ids...)
		}
	}
	return library.TagImages(ctx, tx, []int64{imageID}, tagIDs)
}

func (r *run) folderTags(ctx context.Context, tx *sql.Tx, names []string) ([]int64, error) {
	var ids []int64
	for _, n := range names {
		key := bag.LabelKey(n)
		id, ok := r.tagCache[key]
		if !ok {
			got, err := library.EnsureTags(ctx, tx, []string{n})
			if err != nil {
				return nil, err
			}
			id = got[0]
			r.tagCache[key] = id
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// weighted is a counting semaphore over bytes.
type weighted struct {
	mu    sync.Mutex
	cond  *sync.Cond
	avail int64
}

func newWeighted(n int64) *weighted {
	w := &weighted{avail: n}
	w.cond = sync.NewCond(&w.mu)
	return w
}

// Acquire blocks until n units are available or ctx is done.
func (w *weighted) Acquire(ctx context.Context, n int64) error {
	stop := context.AfterFunc(ctx, func() {
		w.mu.Lock()
		w.cond.Broadcast()
		w.mu.Unlock()
	})
	defer stop()
	w.mu.Lock()
	defer w.mu.Unlock()
	for w.avail < n {
		if err := ctx.Err(); err != nil {
			return err
		}
		w.cond.Wait()
	}
	w.avail -= n
	return nil
}

// Release returns n units.
func (w *weighted) Release(n int64) {
	if n <= 0 {
		return
	}
	w.mu.Lock()
	w.avail += n
	w.cond.Broadcast()
	w.mu.Unlock()
}
