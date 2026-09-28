// Package backup copies a bag to a standalone, compacted file with
// VACUUM INTO, and compacts bags in place.
package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"photobag/internal/bag"
	"photobag/internal/sysutil"
)

// Result describes a finished backup.
type Result struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Millis int64  `json:"millis"`
}

// Required estimates the bytes a backup needs (used pages).
func Required(ctx context.Context, b *bag.Bag) (int64, error) {
	s, err := b.Stats(ctx)
	if err != nil {
		return 0, err
	}
	return s.SizeBytes - s.FreeBytes, nil
}

// checkSpace fails when the destination volume cannot hold the backup.
func checkSpace(ctx context.Context, b *bag.Bag, dest string) error {
	need, err := Required(ctx, b)
	if err != nil {
		return err
	}
	free, err := sysutil.DiskFree(filepath.Dir(dest))
	if err != nil {
		return nil // unknown: let SQLite report a full disk
	}
	margin := need/20 + 64<<20
	if uint64(need+margin) > free {
		return fmt.Errorf("not enough free space for the backup: need about %d MB, %d MB available",
			(need+margin)>>20, free>>20)
	}
	return nil
}

// To writes a compacted copy of the bag to dest, which must not exist. The
// copy is written under a temporary name and renamed into place, and it is
// a single file in DELETE journal mode.
func To(ctx context.Context, b *bag.Bag, dest string) (*Result, error) {
	start := time.Now()
	dest, err := filepath.Abs(dest)
	if err != nil {
		return nil, err
	}
	if dest == b.Path {
		return nil, errors.New("backup destination is the bag itself")
	}
	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("%s already exists", dest)
	}
	if err := checkSpace(ctx, b, dest); err != nil {
		return nil, err
	}
	tmp := dest + ".partial"
	os.Remove(tmp)
	if err := vacuumInto(ctx, b, tmp); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	st, err := os.Stat(dest)
	if err != nil {
		return nil, err
	}
	return &Result{Path: dest, Bytes: st.Size(), Millis: time.Since(start).Milliseconds()}, nil
}

func vacuumInto(ctx context.Context, b *bag.Bag, dest string) error {
	db, err := b.OpenAux()
	if err != nil {
		// DELETE/exclusive mode: only the writer can read the file.
		_, err = b.W.ExecContext(ctx, "VACUUM INTO ?", dest)
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, "VACUUM INTO ?", dest)
	return err
}

// TempPath returns a temporary backup path next to the bag.
func TempPath(bagPath string) string {
	dir, base := filepath.Split(bagPath)
	return filepath.Join(dir, fmt.Sprintf(".%s.backup-%d.tmp", base, time.Now().UnixNano()))
}

// CleanStale removes temporary backups left next to the bag by a previous
// run (including ".partial" files).
func CleanStale(bagPath string) {
	dir, base := filepath.Split(bagPath)
	matches, _ := filepath.Glob(filepath.Join(dir, "."+base+".backup-*.tmp*"))
	for _, m := range matches {
		if strings.HasSuffix(m, ".tmp") || strings.HasSuffix(m, ".tmp.partial") {
			os.Remove(m)
		}
	}
}

// Compact rebuilds the bag in place (VACUUM), reclaiming free pages.
func Compact(ctx context.Context, b *bag.Bag) (before, after int64, err error) {
	s, err := b.Stats(ctx)
	if err != nil {
		return 0, 0, err
	}
	before = s.SizeBytes
	if err := checkSpace(ctx, b, b.Path); err != nil {
		return before, before, err
	}
	if _, err := b.W.ExecContext(ctx, "VACUUM"); err != nil {
		return before, before, err
	}
	b.Checkpoint(ctx)
	s, err = b.Stats(ctx)
	return before, s.SizeBytes, err
}
