package library

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"runtime"

	"photobag/internal/bag"
	"photobag/internal/imaging"
)

// ThumbMode says where a bag keeps its thumbnails.
type ThumbMode string

// Thumbnail modes.
const (
	// ThumbsStored keeps every thumbnail in the bag: browsing is fast, at
	// the cost of space (the default).
	ThumbsStored ThumbMode = "stored"
	// ThumbsOnDemand stores none: they are made from the original when
	// first shown and kept in memory until the server stops.
	ThumbsOnDemand ThumbMode = "on-demand"
)

// Valid reports whether m is a known mode.
func (m ThumbMode) Valid() bool { return m == ThumbsStored || m == ThumbsOnDemand }

// ThumbModeKey is the meta key holding the bag's ThumbMode.
const ThumbModeKey = "thumbnails"

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func thumbMode(ctx context.Context, q rowQueryer) (ThumbMode, error) {
	var v string
	err := q.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", ThumbModeKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !ThumbMode(v).Valid()) {
		return ThumbsStored, nil
	}
	return ThumbMode(v), err
}

// GetThumbMode reports where the bag keeps its thumbnails.
func GetThumbMode(ctx context.Context, b *bag.Bag) (ThumbMode, error) {
	return thumbMode(ctx, b.R)
}

// SaveThumb keeps the thumbnail made with a new (or refreshed) blob: in the
// bag when it stores thumbnails, otherwise in memory only, if the bag has a
// thumbnail cache.
func SaveThumb(ctx context.Context, tx *sql.Tx, b *bag.Bag, blobID int64, sha []byte, res *imaging.Result) error {
	mode, err := thumbMode(ctx, tx)
	if err != nil {
		return err
	}
	if mode == ThumbsOnDemand {
		if b.Thumbs != nil {
			b.Thumbs.Put(hex.EncodeToString(sha), res.Thumb)
		}
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO thumbnails(blob_id, width, height, data) VALUES (?, ?, ?, ?)
		ON CONFLICT(blob_id) DO UPDATE SET width = excluded.width, height = excluded.height, data = excluded.data`,
		blobID, res.ThumbW, res.ThumbH, res.Thumb)
	return err
}

// Thumb returns the thumbnail JPEG of a blob by its sha256 (hex): the
// stored one, or one made from the original (see madeThumb).
func Thumb(ctx context.Context, b *bag.Bag, shaHex string) ([]byte, error) {
	sha, err := hex.DecodeString(shaHex)
	if err != nil || len(sha) != 32 {
		return nil, ErrNotFound
	}
	var data []byte
	err = b.R.QueryRowContext(ctx,
		"SELECT th.data FROM blobs bl JOIN thumbnails th ON th.blob_id = bl.id WHERE bl.sha256 = ?", sha).Scan(&data)
	if !errors.Is(err, sql.ErrNoRows) {
		return data, err
	}
	return madeThumb(ctx, b, sha)
}

// ThumbByID returns the thumbnail JPEG of an image.
func ThumbByID(ctx context.Context, b *bag.Bag, id int64) ([]byte, error) {
	var sha, data []byte
	err := b.R.QueryRowContext(ctx, `SELECT bl.sha256, th.data FROM images i JOIN blobs bl ON bl.id = i.blob_id
		LEFT JOIN thumbnails th ON th.blob_id = bl.id WHERE i.id = ?`, id).Scan(&sha, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil || data != nil {
		return data, err
	}
	return madeThumb(ctx, b, sha)
}

// thumbSlots bounds the originals decoded at once for thumbnails.
var thumbSlots = make(chan struct{}, max(2, runtime.NumCPU()/2))

// madeThumb makes a thumbnail from the original, for bags that do not
// store them (or one not made yet), and keeps it in the bag's memory
// cache.
func madeThumb(ctx context.Context, b *bag.Bag, sha []byte) ([]byte, error) {
	render := func() ([]byte, error) {
		select {
		case thumbSlots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-thumbSlots }()
		var data []byte
		err := b.R.QueryRowContext(ctx, "SELECT data FROM blobs WHERE sha256 = ?", sha).Scan(&data)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		f, err := imaging.Identify(data)
		if err != nil {
			return nil, err
		}
		thumb, _, _, err := imaging.Thumbnail(f, data)
		if err != nil {
			return nil, fmt.Errorf("making a thumbnail: %w", err)
		}
		return thumb, nil
	}
	if b.Thumbs == nil {
		return render()
	}
	return b.Thumbs.Get(hex.EncodeToString(sha), render)
}

// ThumbInfo describes where a bag keeps its thumbnails, and the space and
// memory they take.
type ThumbInfo struct {
	Mode ThumbMode `json:"mode" tstype:"'stored' | 'on-demand'"`
	// Files counts the image files held (originals, generated images,
	// re-encoded versions): each has a thumbnail.
	Files int64 `json:"files"`
	// Stored counts the thumbnails in the bag file and StoredBytes is their
	// size; in large bags both are estimated from a sample.
	Stored      int64 `json:"stored"`
	StoredBytes int64 `json:"storedBytes"`
	Estimated   bool  `json:"estimated"`
	// InMemory and MemoryBytes are the thumbnails made on demand held in
	// memory now, up to MemoryLimit bytes.
	InMemory    int   `json:"inMemory"`
	MemoryBytes int64 `json:"memoryBytes"`
	MemoryLimit int64 `json:"memoryLimit"`
}

// thumbSample is how many thumbnails are measured to estimate the space
// they take; tables spanning fewer ids are measured in full.
const thumbSample = 200

// GetThumbInfo reports the bag's thumbnail mode and use of space. Reading
// every thumbnail of a large bag takes a while (each fills most of a
// page), so their number and size are estimated from a sample of ids,
// spread by the golden ratio so the sample does not fall in step with
// some pattern in the ids (and gives the same answer each time).
func GetThumbInfo(ctx context.Context, b *bag.Bag) (ThumbInfo, error) {
	var t ThumbInfo
	var err error
	if t.Mode, err = GetThumbMode(ctx, b); err != nil {
		return t, err
	}
	if b.Thumbs != nil {
		u := b.Thumbs.Usage()
		t.InMemory, t.MemoryBytes, t.MemoryLimit = u.Items, u.Bytes, u.Capacity
	}
	var lo, hi sql.NullInt64
	err = b.R.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM blobs),
		(SELECT min(blob_id) FROM thumbnails), (SELECT max(blob_id) FROM thumbnails)`).Scan(&t.Files, &lo, &hi)
	if err != nil || !lo.Valid {
		return t, err
	}
	exact := func() error {
		t.Estimated = false
		return b.R.QueryRowContext(ctx, "SELECT count(*), COALESCE(sum(length(data)), 0) FROM thumbnails").
			Scan(&t.Stored, &t.StoredBytes)
	}
	span := hi.Int64 - lo.Int64 + 1
	if span <= 10*thumbSample {
		return t, exact()
	}
	// length() of a blob is read from the row header, without loading it.
	stmt, err := b.R.PrepareContext(ctx, "SELECT length(data) FROM thumbnails WHERE blob_id = ?")
	if err != nil {
		return t, err
	}
	defer stmt.Close()
	var hits, bytes int64
	for i := range int64(thumbSample) {
		var n int64
		_, f := math.Modf((float64(i) + 0.5) * math.Phi)
		err := stmt.QueryRowContext(ctx, lo.Int64+int64(f*float64(span))).Scan(&n)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return t, err
		}
		hits++
		bytes += n
	}
	if hits < thumbSample/20 {
		return t, exact() // few thumbnails over a wide range of ids
	}
	t.Estimated = true
	t.Stored = min(t.Files, span*hits/thumbSample)
	t.StoredBytes = t.Stored * bytes / hits
	return t, nil
}
