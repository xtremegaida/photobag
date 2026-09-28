package importer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/testimg"
)

func setup(t *testing.T) (*bag.Bag, string, testimg.Tree) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	tree, err := testimg.WriteTree(src, 0.25)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bag.Open(filepath.Join(dir, "test.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b, src, tree
}

func TestImportTree(t *testing.T) {
	b, src, tree := setup(t)
	ctx := context.Background()
	var last Progress
	rep, err := Run(ctx, b, src, Options{Recursive: true, TagFolders: true, Tags: []string{"Imported"}}, func(p Progress) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	images := tree.Count("image", "exact-dup", "near-dup", "flat")
	if rep.Added != images {
		t.Errorf("added %d, want %d; report %+v", rep.Added, images, rep.Files)
	}
	if rep.Skipped != tree.Count("unsupported") || rep.Failed != tree.Count("corrupt") {
		t.Errorf("skipped=%d failed=%d; files %+v", rep.Skipped, rep.Failed, rep.Files)
	}
	for _, f := range rep.Files {
		if strings.Contains(f.Path, "secret") {
			t.Errorf("hidden folder was scanned: %s", f.Path)
		}
		if strings.HasSuffix(f.Path, "phone.heic") && !strings.Contains(f.Reason, "HEIC") {
			t.Errorf("heic reason = %q", f.Reason)
		}
	}
	if last.Phase != "done" || last.Done != rep.Found {
		t.Errorf("final progress %+v, found %d", last, rep.Found)
	}

	stats, _ := library.GetStats(ctx, b)
	if stats.Images != int64(images) || stats.Blobs != int64(images-1) {
		t.Errorf("stats images=%d blobs=%d (exact duplicate should share a blob)", stats.Images, stats.Blobs)
	}

	tags, _ := library.ListTags(ctx, b)
	got := map[string]int{}
	for _, tg := range tags {
		got[tg.Name] = tg.Count
	}
	if got["Imported"] != images || got["Party"] != 7 || got["Holiday"] != 3 || got["2019"] != 3 {
		t.Errorf("tag counts %v", got)
	}

	// Portrait is stored rotated with orientation 6: display size is tall.
	ids, _ := library.ListIDs(ctx, b, query.ImageQuery{NameGlob: "portrait*"}, query.Sort{})
	if len(ids) != 1 {
		t.Fatalf("portrait lookup: %v", ids)
	}
	im, _ := library.GetImage(ctx, b, ids[0])
	if im.Width >= im.Height || im.ThumbW >= im.ThumbH {
		t.Errorf("portrait %dx%d thumb %dx%d, want tall", im.Width, im.Height, im.ThumbW, im.ThumbH)
	}
	if im.OriginalPath != "2020/portrait.jpg" {
		t.Errorf("original path %q", im.OriginalPath)
	}

	// Two images named beach.jpg coexist.
	ids, _ = library.ListIDs(ctx, b, query.ImageQuery{NameGlob: "beach.jpg"}, query.Sort{})
	if len(ids) != 2 {
		t.Errorf("want two beach.jpg, got %d", len(ids))
	}
	beach, _ := library.GetImages(ctx, b, ids)
	var dated int
	for _, im := range beach {
		if im.TakenAt != "" {
			dated++
		}
	}
	if dated != 2 {
		t.Errorf("EXIF dates missing: %+v", beach)
	}
}

func TestReimportSkipsIdentical(t *testing.T) {
	b, src, tree := setup(t)
	ctx := context.Background()
	if _, err := Run(ctx, b, src, Options{Recursive: true}, nil); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(ctx, b, src, Options{Recursive: true, SkipIdentical: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	images := tree.Count("image", "exact-dup", "near-dup", "flat")
	if rep.Added != 0 || rep.Skipped != images+tree.Count("unsupported") {
		t.Errorf("reimport added=%d skipped=%d", rep.Added, rep.Skipped)
	}
	// Without the flag, identical files are imported again (sharing blobs).
	rep, err = Run(ctx, b, filepath.Join(src, "2019"), Options{Recursive: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Added != 3 {
		t.Errorf("added %d, want 3", rep.Added)
	}
	stats, _ := library.GetStats(ctx, b)
	if stats.Blobs != int64(images-1) {
		t.Errorf("blobs = %d, duplicate imports must not add blobs", stats.Blobs)
	}
}

func TestNonRecursiveAndRemoved(t *testing.T) {
	b, src, _ := setup(t)
	ctx := context.Background()
	rep, err := Run(ctx, b, filepath.Join(src, "2020"), Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Added != 3 { // beach, copy of sunset, portrait (not Party/)
		t.Fatalf("non-recursive added %d", rep.Added)
	}
	ids, _ := library.ListIDs(ctx, b, query.ImageQuery{NameGlob: "portrait.jpg"}, query.Sort{})
	if _, err := library.Trash(ctx, b, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := library.EmptyTrash(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	rep, err = Run(ctx, b, filepath.Join(src, "2020", "portrait.jpg"), Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Added != 0 || len(rep.Files) != 1 || !strings.Contains(rep.Files[0].Reason, "previously removed") {
		t.Fatalf("expected previously-removed skip, got %+v", rep)
	}
	rep, err = Run(ctx, b, filepath.Join(src, "2020", "portrait.jpg"), Options{IncludeRemoved: true}, nil)
	if err != nil || rep.Added != 1 {
		t.Fatalf("include-removed: %v %+v", err, rep)
	}
}

func TestRefreshRebuildsDerivatives(t *testing.T) {
	b, src, _ := setup(t)
	ctx := context.Background()
	if _, err := Run(ctx, b, filepath.Join(src, "2019"), Options{Recursive: true}, nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := Outdated(ctx, b); n != 0 {
		t.Fatalf("fresh import has %d outdated blobs", n)
	}
	// Simulate an older thumbprint version and a lost thumbnail.
	if _, err := b.W.Exec("UPDATE fingerprints SET version = 0, phash = 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.W.Exec("DELETE FROM thumbnails WHERE blob_id = (SELECT min(id) FROM blobs)"); err != nil {
		t.Fatal(err)
	}
	if n, _ := Outdated(ctx, b); n != 3 {
		t.Fatalf("outdated = %d, want 3", n)
	}
	rep, err := Refresh(ctx, b, nil)
	if err != nil || rep.Updated != 3 || rep.Failed != 0 {
		t.Fatalf("refresh: %v %+v", err, rep)
	}
	if n, _ := Outdated(ctx, b); n != 0 {
		t.Fatalf("still outdated: %d", n)
	}
	var zero int
	b.R.QueryRow("SELECT count(*) FROM fingerprints WHERE phash = 0").Scan(&zero)
	if zero != 0 {
		t.Error("thumbprints were not recomputed")
	}
}
