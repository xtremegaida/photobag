package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"photobag/internal/library"
	"photobag/internal/lru"
	"photobag/internal/testimg"
)

func TestThumbModes(t *testing.T) {
	b, src, _ := setup(t)
	ctx := context.Background()
	b.Thumbs = lru.New(64 << 20)
	if _, err := Run(ctx, b, src, Options{Recursive: true}, nil); err != nil {
		t.Fatal(err)
	}
	count := func(q string) (n int64) {
		t.Helper()
		if err := b.R.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// A blob without an image row (as generated and re-encoded images
	// have) is refreshed too: its format comes from its bytes.
	png := testimg.PNG(testimg.Scene(7, 300, 200), testimg.EXIF{})
	sum := sha256.Sum256(png)
	if _, err := b.W.Exec("INSERT INTO blobs(sha256, size, data) VALUES (?, ?, ?)", sum[:], len(png), png); err != nil {
		t.Fatal(err)
	}
	if n, _ := Outdated(ctx, b); n != 1 {
		t.Fatalf("outdated = %d, want the new blob", n)
	}
	if rep, err := Refresh(ctx, b, nil); err != nil || rep.Updated != 1 {
		t.Fatalf("refresh: %v %+v", err, rep)
	}

	info, err := library.GetThumbInfo(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	files := count("SELECT count(*) FROM blobs")
	if info.Mode != library.ThumbsStored || info.Files != files || info.Stored != files || info.StoredBytes == 0 || info.Estimated {
		t.Fatalf("stored info = %+v, files %d", info, files)
	}

	// Remember the stored thumbnails, to compare with ones made on demand.
	stored := map[int64][]byte{}
	rows, err := b.R.Query("SELECT id FROM images WHERE blob_id IS NOT NULL")
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if stored[id], err = library.ThumbByID(ctx, b, id); err != nil {
			t.Fatal(err)
		}
	}
	if b.Thumbs.Usage().Items != 0 {
		t.Fatal("stored thumbnails were copied to memory")
	}

	rep, err := SetThumbMode(ctx, b, library.ThumbsOnDemand, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := count("SELECT count(*) FROM thumbnails"); n != 0 {
		t.Fatalf("%d thumbnails left", n)
	}
	if rep.After >= rep.Before || rep.Before-rep.After < info.StoredBytes/2 {
		t.Errorf("bag went from %d to %d bytes, thumbnails took %d", rep.Before, rep.After, info.StoredBytes)
	}
	if st, _ := b.Stats(ctx); st.FreePages != 0 {
		t.Errorf("%d free pages left", st.FreePages)
	}
	if n, _ := Outdated(ctx, b); n != 0 {
		t.Errorf("missing thumbnails count as outdated on demand: %d", n)
	}
	for _, id := range ids {
		got, err := library.ThumbByID(ctx, b, id)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, stored[id]) {
			t.Errorf("image %d: thumbnail made on demand differs from the stored one", id)
		}
	}
	if u := b.Thumbs.Usage(); u.Items != len(ids)-1 { // one exact duplicate shares a blob
		t.Errorf("memory holds %d thumbnails, want %d", u.Items, len(ids)-1)
	}
	if data, err := library.Thumb(ctx, b, hex.EncodeToString(sum[:])); err != nil || len(data) == 0 {
		t.Errorf("thumbnail of a blob without an image: %v", err)
	}

	// Images imported now keep their thumbnails in memory only.
	more := t.TempDir()
	jpg := testimg.JPEG(testimg.Scene(99, 640, 480), 90, testimg.EXIF{Orientation: 6})
	if err := os.WriteFile(filepath.Join(more, "new.jpg"), jpg, 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err := Run(ctx, b, more, Options{}, nil); err != nil || r.Added != 1 {
		t.Fatalf("import: %v %+v", err, r)
	}
	if n := count("SELECT count(*) FROM thumbnails"); n != 0 {
		t.Errorf("an import stored %d thumbnails", n)
	}
	jsum := sha256.Sum256(jpg)
	if _, ok := b.Thumbs.Peek(hex.EncodeToString(jsum[:])); !ok {
		t.Error("the imported image's thumbnail is not in memory")
	}

	rep, err = SetThumbMode(ctx, b, library.ThumbsStored, nil)
	if err != nil {
		t.Fatal(err)
	}
	files = count("SELECT count(*) FROM blobs")
	if int64(rep.Made) != files || rep.Failed != 0 || count("SELECT count(*) FROM thumbnails") != files {
		t.Errorf("stored again: %+v, %d files", rep, files)
	}
	if n, _ := Outdated(ctx, b); n != 0 {
		t.Errorf("outdated after storing: %d", n)
	}
	for _, id := range ids {
		if got, _ := library.ThumbByID(ctx, b, id); !bytes.Equal(got, stored[id]) {
			t.Errorf("image %d: thumbnail stored again differs", id)
		}
	}
	if mode, _ := library.GetThumbMode(ctx, b); mode != library.ThumbsStored {
		t.Errorf("mode = %q", mode)
	}
}
