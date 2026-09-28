package library_test

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/importer"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/testimg"
)

func setup(t *testing.T) (*bag.Bag, map[string]int64) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	if _, err := testimg.WriteTree(src, 0.1); err != nil {
		t.Fatal(err)
	}
	b, err := bag.Open(filepath.Join(dir, "l.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	ctx := context.Background()
	if _, err := importer.Run(ctx, b, src, importer.Options{Recursive: true, TagFolders: true}, nil); err != nil {
		t.Fatal(err)
	}
	ids, _ := library.ListIDs(ctx, b, query.ImageQuery{}, query.Sort{})
	ims, _ := library.GetImages(ctx, b, ids)
	byPath := map[string]int64{}
	for _, im := range ims {
		byPath[im.OriginalPath] = im.ID
	}
	return b, byPath
}

func names(t *testing.T, b *bag.Bag, q query.ImageQuery, s query.Sort) []string {
	t.Helper()
	ids, err := library.ListIDs(context.Background(), b, q, s)
	if err != nil {
		t.Fatal(err)
	}
	ims, _ := library.GetImages(context.Background(), b, ids)
	out := make([]string, len(ims))
	for i, im := range ims {
		out[i] = im.Name
	}
	return out
}

func TestQueries(t *testing.T) {
	b, byPath := setup(t)
	ctx := context.Background()
	count := func(q query.ImageQuery) int {
		n, err := library.Count(ctx, b, q)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(query.ImageQuery{TagsAll: []string{"2020", "party"}}); n != 7 {
		t.Errorf("2020 AND party = %d", n)
	}
	if n := count(query.ImageQuery{TagsAny: []string{"holiday", "MISC"}}); n != 3+6 {
		t.Errorf("holiday OR misc = %d", n)
	}
	if n := count(query.ImageQuery{TagsAll: []string{"2020"}, TagsNone: []string{"Party"}}); n != 3 {
		t.Errorf("2020 NOT party = %d", n)
	}
	if n := count(query.ImageQuery{NameGlob: "party"}); n != 7 { // plain text = substring
		t.Errorf("substring 'party' = %d", n)
	}
	if n := count(query.ImageQuery{NameGlob: "PARTY_0[1-3].JPG"}); n != 3 {
		t.Errorf("glob class = %d", n)
	}
	if n := count(query.ImageQuery{IDs: []int64{byPath["misc/plain.bmp"], byPath["misc/sky.jpg"], 99999}}); n != 2 {
		t.Errorf("explicit ids = %d", n)
	}
	if err := (query.ImageQuery{NameGlob: "[unclosed"}).Validate(); err == nil {
		t.Error("bad glob should not validate")
	}

	// Untagged: remove the folder tags from one image.
	sky := byPath["misc/sky.jpg"]
	if err := library.RemoveTags(ctx, b, []int64{sky}, []string{"misc"}); err != nil {
		t.Fatal(err)
	}
	ids, _ := library.ListIDs(ctx, b, query.ImageQuery{Untagged: true}, query.Sort{})
	if len(ids) != 1 || ids[0] != sky {
		t.Errorf("untagged = %v", ids)
	}

	// Sorts.
	got := names(t, b, query.ImageQuery{TagsAll: []string{"party"}}, query.Sort{Field: query.SortName})
	want := append([]string{}, got...)
	sort.Strings(want)
	if got[0] != "party_01.jpg" || got[len(got)-1] != "party_06.jpg" {
		t.Errorf("name sort %v", got)
	}
	taken := names(t, b, query.ImageQuery{}, query.Sort{Field: query.SortTaken})
	if taken[0] != "beach.jpg" || taken[1] != "beach.jpg" { // the two dated images come first
		t.Errorf("taken sort starts %v", taken[:3])
	}
	big := names(t, b, query.ImageQuery{}, query.Sort{Field: query.SortPixels, Desc: true})
	if big[0] != "beach.jpg" && big[0] != "portrait.jpg" {
		t.Errorf("largest image first, got %s", big[0])
	}
	r1 := names(t, b, query.ImageQuery{}, query.Sort{Field: query.SortRandom, Seed: 1})
	r2 := names(t, b, query.ImageQuery{}, query.Sort{Field: query.SortRandom, Seed: 1})
	if len(r1) != len(r2) || r1[0] != r2[0] || r1[len(r1)-1] != r2[len(r2)-1] {
		t.Error("random sort must be stable per seed")
	}
	if err := (query.Sort{Field: query.SortScore}).Validate(); err == nil {
		t.Error("score sort without metric should not validate")
	}
}

func TestRenameAndTrashLifecycle(t *testing.T) {
	b, byPath := setup(t)
	ctx := context.Background()
	id := byPath["2020/beach.jpg"]
	for _, bad := range []string{"", "a/b.jpg", `c:\x.jpg`, "CON.jpg", "dot.", "what?.jpg"} {
		if _, err := library.Rename(ctx, b, id, bad); err == nil {
			t.Errorf("rename to %q should fail", bad)
		}
	}
	if name, err := library.Rename(ctx, b, id, "  Winter beach.jpg "); err != nil || name != "Winter beach.jpg" {
		t.Fatalf("rename: %q %v", name, err)
	}
	im, _ := library.GetImage(ctx, b, id)
	if im.Name != "Winter beach.jpg" || im.OriginalName != "beach.jpg" {
		t.Errorf("after rename %+v", im)
	}

	// Trash → purge: the tombstone remains, shared blobs survive.
	copyID := byPath["2020/copy of sunset.png"]
	if n, _ := library.Trash(ctx, b, []int64{copyID, id}); n != 2 {
		t.Fatalf("trashed %d", n)
	}
	if n, _ := library.Count(ctx, b, query.ImageQuery{Scope: query.Trash}); n != 2 {
		t.Errorf("trash count %d", n)
	}
	before, _ := library.GetStats(ctx, b)
	res, err := library.EmptyTrash(ctx, b, nil)
	if err != nil || res.Purged != 2 || res.BlobsFreed != 1 {
		t.Fatalf("empty trash: %v %+v", err, res)
	}
	after, _ := library.GetStats(ctx, b)
	if after.Purged != 2 || after.Blobs != before.Blobs-1 {
		t.Errorf("stats before %+v after %+v", before, after)
	}
	// The original sunset still has its bytes (it shared the blob).
	if _, err := library.Original(ctx, b, byPath["2019/Holiday/sunset.png"]); err != nil {
		t.Errorf("shared blob lost: %v", err)
	}
	if _, err := library.Original(ctx, b, id); err == nil {
		t.Error("purged image should have no bytes")
	}
	if n, _ := library.Restore(ctx, b, []int64{id}); n != 0 {
		t.Error("purged images cannot be restored")
	}
}

func TestTagRenameMerge(t *testing.T) {
	b, byPath := setup(t)
	ctx := context.Background()
	tags, _ := library.ListTags(ctx, b)
	var holiday, misc int64
	for _, tg := range tags {
		switch tg.Name {
		case "Holiday":
			holiday = tg.ID
		case "misc":
			misc = tg.ID
		}
	}
	if id, err := library.RenameTag(ctx, b, holiday, "Vacation"); err != nil || id != holiday {
		t.Fatalf("rename: %v", err)
	}
	into, err := library.RenameTag(ctx, b, misc, "vacation")
	if err != nil || into != holiday {
		t.Fatalf("merge: %d %v", into, err)
	}
	n, _ := library.Count(ctx, b, query.ImageQuery{TagsAll: []string{"VACATION"}})
	if n != 3+6 {
		t.Errorf("merged tag count %d", n)
	}
	im, _ := library.GetImage(ctx, b, byPath["misc/sky.jpg"])
	if len(im.Tags) != 1 || im.Tags[0] != "Vacation" {
		t.Errorf("tags after merge %v", im.Tags)
	}
	if err := library.DeleteTag(ctx, b, holiday); err != nil {
		t.Fatal(err)
	}
	if n, _ := library.Count(ctx, b, query.ImageQuery{Untagged: true}); n != 6 { // misc images had only that tag
		t.Errorf("untagged after delete %d", n)
	}
}
