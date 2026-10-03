package importer

import (
	"context"
	"database/sql"
	"fmt"

	"photobag/internal/bag"
	"photobag/internal/library"
)

// ThumbReport is the outcome of changing where a bag keeps its thumbnails.
type ThumbReport struct {
	Mode library.ThumbMode `json:"mode" tstype:"'stored' | 'on-demand'"`
	// Made and Failed count the thumbnails made to store (switching to
	// stored thumbnails).
	Made   int `json:"made"`
	Failed int `json:"failed"`
	// Before and After are the bag file's size, around dropping the stored
	// thumbnails.
	Before int64 `json:"before"`
	After  int64 `json:"after"`
}

// vacuumStep is how many pages are given back to the disk at a time, with
// a checkpoint after each, so the WAL stays small.
const vacuumStep = 4096

// SetThumbMode changes where the bag keeps its thumbnails. Storing them
// makes the missing ones from every image file (which takes a while: each
// is decoded); making them on demand drops the stored ones and gives their
// space back to the disk. progress reports files made or pages released.
func SetThumbMode(ctx context.Context, b *bag.Bag, mode library.ThumbMode, progress func(done, total int)) (*ThumbReport, error) {
	if !mode.Valid() {
		return nil, fmt.Errorf("unknown thumbnail mode %q", mode)
	}
	if progress == nil {
		progress = func(int, int) {}
	}
	rep := &ThumbReport{Mode: mode}
	if mode == library.ThumbsStored {
		if err := b.SetMeta(ctx, library.ThumbModeKey, string(mode)); err != nil {
			return nil, err
		}
		r, err := Refresh(ctx, b, progress)
		if r != nil {
			rep.Made, rep.Failed = r.Updated, r.Failed
		}
		return rep, err
	}

	st, err := b.Stats(ctx)
	if err != nil {
		return nil, err
	}
	rep.Before = st.SizeBytes
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, library.ThumbModeKey, string(mode)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM thumbnails")
		return err
	})
	if err != nil {
		return nil, err
	}
	if st, err = b.Stats(ctx); err != nil {
		return nil, err
	}
	total := st.FreePages
	for st.FreePages > 0 && ctx.Err() == nil {
		left := st.FreePages
		if _, err := b.W.ExecContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", vacuumStep)); err != nil {
			return nil, err
		}
		b.Checkpoint(ctx)
		if st, err = b.Stats(ctx); err != nil {
			return nil, err
		}
		progress(int(total-st.FreePages), int(total))
		if st.FreePages >= left {
			break // not an incrementally vacuumed file: Compact gives the space back
		}
	}
	rep.After = st.SizeBytes
	return rep, ctx.Err()
}
