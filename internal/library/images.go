// Package library implements the core image and tag operations on a bag:
// listing, metadata, renaming, tagging and the trash lifecycle.
package library

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"photobag/internal/bag"
	"photobag/internal/query"
)

// ErrNotFound is returned for unknown ids.
var ErrNotFound = errors.New("not found")

// Image is the metadata of one image.
type Image struct {
	ID           int64    `json:"id"`
	UID          string   `json:"uid"`
	Name         string   `json:"name"`
	OriginalName string   `json:"originalName"`
	OriginalPath string   `json:"originalPath"`
	Format       string   `json:"format"`
	Size         int64    `json:"size"`
	Width        int      `json:"width"`
	Height       int      `json:"height"`
	TakenAt      string   `json:"takenAt,omitempty"`
	TakenOffset  string   `json:"takenOffset,omitempty"`
	FileMtime    int64    `json:"fileMtime,omitempty"`
	ImportedAt   int64    `json:"importedAt"`
	DeletedAt    int64    `json:"deletedAt,omitempty"`
	MergedInto   int64    `json:"mergedInto,omitempty"`
	SHA256       string   `json:"sha256"`
	ThumbW       int      `json:"thumbW"`
	ThumbH       int      `json:"thumbH"`
	Tags         []string `json:"tags"`
	// Caption is the image's description (alt text), if analysed.
	Caption string `json:"caption,omitempty"`
}

const imageCols = `i.id, i.uid, i.name, i.original_name, i.original_path, i.format, i.size,
	i.width, i.height, COALESCE(i.taken_at, ''), COALESCE(i.taken_offset, ''), COALESCE(i.file_mtime, 0),
	i.imported_at, COALESCE(i.deleted_at, 0), COALESCE(i.merged_into, 0), i.sha256,
	COALESCE(th.width, 0), COALESCE(th.height, 0), COALESCE(ac.text, '')`

const imageFrom = ` FROM images i LEFT JOIN thumbnails th ON th.blob_id = i.blob_id
	LEFT JOIN analyses ac ON ac.image_id = i.id AND ac.pipeline = 'caption'`

func scanImage(sc interface{ Scan(...any) error }) (Image, error) {
	var im Image
	var sha []byte
	err := sc.Scan(&im.ID, &im.UID, &im.Name, &im.OriginalName, &im.OriginalPath, &im.Format, &im.Size,
		&im.Width, &im.Height, &im.TakenAt, &im.TakenOffset, &im.FileMtime,
		&im.ImportedAt, &im.DeletedAt, &im.MergedInto, &sha, &im.ThumbW, &im.ThumbH, &im.Caption)
	im.SHA256 = hex.EncodeToString(sha)
	im.Tags = []string{}
	return im, err
}

// ListIDs returns the ids matching q in sort order. Similarity sorts are
// returned in import order; the caller reorders them.
func ListIDs(ctx context.Context, b *bag.Bag, q query.ImageQuery, s query.Sort) ([]int64, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	where, args := q.Where("i")
	join, order, oargs := s.SQL("i")
	sqlText := "SELECT i.id FROM images i " + join + " WHERE " + where + " ORDER BY " + order
	rows, err := b.R.QueryContext(ctx, sqlText, append(oargs, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Count returns how many images match q.
func Count(ctx context.Context, b *bag.Bag, q query.ImageQuery) (int, error) {
	if err := q.Validate(); err != nil {
		return 0, err
	}
	where, args := q.Where("i")
	var n int
	err := b.R.QueryRowContext(ctx, "SELECT count(*) FROM images i WHERE "+where, args...).Scan(&n)
	return n, err
}

// GetImages returns metadata (with tags) for ids, in the order given.
// Unknown ids are skipped.
func GetImages(ctx context.Context, b *bag.Bag, ids []int64) ([]Image, error) {
	if len(ids) == 0 {
		return []Image{}, nil
	}
	js, _ := json.Marshal(ids)
	rows, err := b.R.QueryContext(ctx, "SELECT "+imageCols+imageFrom+
		" WHERE i.id IN (SELECT value FROM json_each(?))", string(js))
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*Image, len(ids))
	var list []Image
	for rows.Next() {
		im, err := scanImage(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, im)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range list {
		byID[list[i].ID] = &list[i]
	}
	tagRows, err := b.R.QueryContext(ctx, `SELECT it.image_id, t.name FROM image_tags it
		JOIN tags t ON t.id = it.tag_id
		WHERE it.image_id IN (SELECT value FROM json_each(?)) ORDER BY t.name COLLATE NOCASE`, string(js))
	if err != nil {
		return nil, err
	}
	for tagRows.Next() {
		var id int64
		var name string
		if err := tagRows.Scan(&id, &name); err != nil {
			tagRows.Close()
			return nil, err
		}
		if im := byID[id]; im != nil {
			im.Tags = append(im.Tags, name)
		}
	}
	tagRows.Close()
	out := make([]Image, 0, len(ids))
	for _, id := range ids {
		if im := byID[id]; im != nil {
			out = append(out, *im)
		}
	}
	return out, tagRows.Err()
}

// GetImage returns one image.
func GetImage(ctx context.Context, b *bag.Bag, id int64) (*Image, error) {
	list, err := GetImages(ctx, b, []int64{id})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// ImageScore is an image's standing on one metric.
type ImageScore struct {
	MetricID   int64   `json:"metricId"`
	MetricName string  `json:"metricName"`
	Score      float64 `json:"score"`
	Stderr     float64 `json:"stderr"`
	N          int     `json:"n"`
	Rank       int     `json:"rank"`
	Of         int     `json:"of"`
}

// Scores lists the image's scores on every metric it has been compared on.
func Scores(ctx context.Context, b *bag.Bag, id int64) ([]ImageScore, error) {
	rows, err := b.R.QueryContext(ctx, `SELECT m.id, m.name, s.score, s.stderr, s.n,
		(SELECT count(*) + 1 FROM scores s2 WHERE s2.metric_id = s.metric_id AND s2.score > s.score),
		(SELECT count(*) FROM scores s3 WHERE s3.metric_id = s.metric_id)
		FROM scores s JOIN metrics m ON m.id = s.metric_id
		WHERE s.image_id = ? ORDER BY m.name COLLATE NOCASE`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImageScore{}
	for rows.Next() {
		var s ImageScore
		if err := rows.Scan(&s.MetricID, &s.MetricName, &s.Score, &s.Stderr, &s.N, &s.Rank, &s.Of); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Rename changes an image's display/export name.
func Rename(ctx context.Context, b *bag.Bag, id int64, name string) (string, error) {
	name, err := ValidateName(name)
	if err != nil {
		return "", err
	}
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE images SET name = ? WHERE id = ? AND purged_at IS NULL", name, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	return name, err
}

// Blob is an image's original bytes.
type Blob struct {
	Image       Image
	Data        []byte
	Orientation int
}

// Original loads the original bytes of an image.
func Original(ctx context.Context, b *bag.Bag, id int64) (*Blob, error) {
	im, err := GetImage(ctx, b, id)
	if err != nil {
		return nil, err
	}
	var data []byte
	var orientation int
	err = b.R.QueryRowContext(ctx,
		"SELECT bl.data, i.orientation FROM images i JOIN blobs bl ON bl.id = i.blob_id WHERE i.id = ?", id).Scan(&data, &orientation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("image %d has been purged: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return &Blob{Image: *im, Data: data, Orientation: orientation}, nil
}

// BlobInfo identifies the blob behind an image for preview caching.
func BlobInfo(ctx context.Context, b *bag.Bag, id int64) (blobID int64, format string, err error) {
	err = b.R.QueryRowContext(ctx,
		"SELECT blob_id, format FROM images WHERE id = ? AND blob_id IS NOT NULL", id).Scan(&blobID, &format)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// BlobData loads blob bytes by blob id.
func BlobData(ctx context.Context, b *bag.Bag, blobID int64) ([]byte, error) {
	var data []byte
	err := b.R.QueryRowContext(ctx, "SELECT data FROM blobs WHERE id = ?", blobID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return data, err
}

// Thumb returns the thumbnail JPEG for a blob sha256 (hex).
func Thumb(ctx context.Context, b *bag.Bag, shaHex string) ([]byte, error) {
	sha, err := hex.DecodeString(shaHex)
	if err != nil || len(sha) != 32 {
		return nil, ErrNotFound
	}
	var data []byte
	err = b.R.QueryRowContext(ctx,
		"SELECT th.data FROM blobs bl JOIN thumbnails th ON th.blob_id = bl.id WHERE bl.sha256 = ?", sha).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return data, err
}

// BlobSHA returns a blob's sha256 in hex.
func BlobSHA(ctx context.Context, b *bag.Bag, blobID int64) (string, error) {
	var sha []byte
	err := b.R.QueryRowContext(ctx, "SELECT sha256 FROM blobs WHERE id = ?", blobID).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return hex.EncodeToString(sha), err
}

// BlobDataBySHA loads blob bytes by their sha256 (hex).
func BlobDataBySHA(ctx context.Context, b *bag.Bag, shaHex string) ([]byte, error) {
	sha, err := hex.DecodeString(shaHex)
	if err != nil || len(sha) != 32 {
		return nil, ErrNotFound
	}
	var data []byte
	err = b.R.QueryRowContext(ctx, "SELECT data FROM blobs WHERE sha256 = ?", sha).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return data, err
}

// ThumbByID returns the thumbnail JPEG of an image.
func ThumbByID(ctx context.Context, b *bag.Bag, id int64) ([]byte, error) {
	var data []byte
	err := b.R.QueryRowContext(ctx,
		"SELECT th.data FROM images i JOIN thumbnails th ON th.blob_id = i.blob_id WHERE i.id = ?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return data, err
}

// ValidateName checks and normalises an image name. Names must be valid
// file names on both Windows and Linux.
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", fmt.Errorf("name must not be empty")
	case name == "." || name == "..":
		return "", fmt.Errorf("name %q is not allowed", name)
	case len(name) > 255:
		return "", fmt.Errorf("name is longer than 255 bytes")
	case strings.HasSuffix(name, "."):
		return "", fmt.Errorf("name must not end with a dot")
	}
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return "", fmt.Errorf("name must not contain %q", r)
		}
	}
	if reservedWindowsName(name) {
		return "", fmt.Errorf("%q is a reserved file name on Windows", name)
	}
	return name, nil
}

// reservedWindowsName reports CON, PRN, AUX, NUL, COM1-9, LPT1-9 (with any
// extension), including the superscript-digit variants.
func reservedWindowsName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) >= 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		switch base[3:] {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return true
		}
	}
	return false
}

// IsReservedWindowsName is exported for the exporter's sanitiser.
func IsReservedWindowsName(name string) bool { return reservedWindowsName(name) }

// MarkAllMetricsDirty flags every metric for score recalculation (image
// activity changed, so the comparison set changed).
func MarkAllMetricsDirty(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "UPDATE metrics SET scores_dirty = scores_dirty + 1")
	return err
}
