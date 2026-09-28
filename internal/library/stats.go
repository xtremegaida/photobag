package library

import (
	"context"

	"photobag/internal/bag"
)

// Stats summarises a bag.
type Stats struct {
	Name          string        `json:"name"`
	Path          string        `json:"path"`
	Images        int64         `json:"images"`
	Trashed       int64         `json:"trashed"`
	Purged        int64         `json:"purged"`
	Tags          int64         `json:"tags"`
	Metrics       int64         `json:"metrics"`
	Runs          int64         `json:"runs"`
	Comparisons   int64         `json:"comparisons"`
	Blobs         int64         `json:"blobs"`
	OriginalBytes int64         `json:"originalBytes"`
	File          bag.FileStats `json:"file"`
}

// GetStats computes bag statistics.
func GetStats(ctx context.Context, b *bag.Bag) (Stats, error) {
	s := Stats{Path: b.Path}
	var err error
	if s.Name, err = b.Meta(ctx, "name"); err != nil {
		return s, err
	}
	err = b.R.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM images WHERE deleted_at IS NULL AND purged_at IS NULL),
		(SELECT count(*) FROM images WHERE deleted_at IS NOT NULL AND purged_at IS NULL),
		(SELECT count(*) FROM images WHERE purged_at IS NOT NULL),
		(SELECT count(*) FROM tags),
		(SELECT count(*) FROM metrics),
		(SELECT count(*) FROM score_runs),
		(SELECT count(*) FROM comparisons WHERE winner IS NOT NULL),
		(SELECT count(*) FROM blobs),
		(SELECT COALESCE(sum(size), 0) FROM blobs)`).Scan(
		&s.Images, &s.Trashed, &s.Purged, &s.Tags, &s.Metrics, &s.Runs, &s.Comparisons, &s.Blobs, &s.OriginalBytes)
	if err != nil {
		return s, err
	}
	s.File, err = b.Stats(ctx)
	return s, err
}
