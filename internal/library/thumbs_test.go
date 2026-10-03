package library_test

import (
	"context"
	"database/sql"
	"encoding/binary"
	"path/filepath"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/library"
)

// The space thumbnails take is measured in small bags and estimated from a
// sample in large ones.
func TestThumbInfo(t *testing.T) {
	b, err := bag.Open(filepath.Join(t.TempDir(), "t.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	add := func(from, to int, every int) {
		t.Helper()
		err := b.Tx(ctx, func(tx *sql.Tx) error {
			for i := from; i < to; i++ {
				sha := binary.BigEndian.AppendUint64(make([]byte, 24), uint64(i))
				r, err := tx.Exec("INSERT INTO blobs(sha256, size, data) VALUES (?, 1, x'00')", sha)
				if err != nil {
					return err
				}
				if i%every == 0 {
					id, _ := r.LastInsertId()
					if _, err := tx.Exec("INSERT INTO thumbnails(blob_id, width, height, data) VALUES (?, 1, 1, zeroblob(1000))", id); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	add(0, 500, 1)
	info, err := library.GetThumbInfo(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != library.ThumbsStored || info.Files != 500 || info.Stored != 500 || info.StoredBytes != 500_000 || info.Estimated {
		t.Fatalf("small bag: %+v", info)
	}

	add(500, 20_000, 2) // every other one has a thumbnail
	if info, err = library.GetThumbInfo(ctx, b); err != nil {
		t.Fatal(err)
	}
	want := int64(500 + 19_500/2)
	if !info.Estimated || info.Files != 20_000 || info.Stored < want*9/10 || info.Stored > want*11/10 ||
		info.StoredBytes != info.Stored*1000 {
		t.Fatalf("large bag: %+v, want about %d", info, want)
	}
}
