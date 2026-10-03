package dedup

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/importer"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/similar"
	"photobag/internal/testimg"
)

func setup(t *testing.T) (*bag.Bag, map[string]int64) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	if _, err := testimg.WriteTree(src, 0.25); err != nil {
		t.Fatal(err)
	}
	b, err := bag.Open(filepath.Join(dir, "d.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	ctx := context.Background()
	if _, err := importer.Run(ctx, b, src, importer.Options{Recursive: true}, nil); err != nil {
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

func names(ids []int64, byPath map[string]int64) string {
	inv := map[int64]string{}
	for p, id := range byPath {
		inv[id] = p
	}
	var out []string
	for _, id := range ids {
		out = append(out, inv[id])
	}
	sort.Strings(out)
	return strings.Join(out, " + ")
}

func TestExactScan(t *testing.T) {
	b, byPath := setup(t)
	s, err := NewScan(context.Background(), b, Params{Mode: ModeExact}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cl := s.Clusters(0.99)
	if len(cl) != 1 {
		t.Fatalf("want 1 exact cluster, got %d", len(cl))
	}
	if got := names(cl[0].Members, byPath); got != "2019/Holiday/sunset.png + 2020/copy of sunset.png" {
		t.Errorf("exact cluster: %s", got)
	}
	if len(cl[0].Proposed) != 1 {
		t.Errorf("proposed %v", cl[0].Proposed)
	}
}

func TestSimilarScanResolveRestore(t *testing.T) {
	b, byPath := setup(t)
	ctx := context.Background()
	s, err := NewScan(ctx, b, Params{Mode: ModeSimilar, Neighbors: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	clusters := s.Clusters(DefaultThreshold)
	got := map[string]bool{}
	for _, c := range clusters {
		got[names(c.Members, byPath)] = true
	}
	want := []string{
		"2019/Holiday/beach.jpg + 2019/Holiday/beach_small.jpg",
		"2019/Holiday/sunset.png + 2020/copy of sunset.png",
		"2020/Party/party_03.jpg + 2020/Party/party_03_edit.jpg",
		// The four Pillow fixtures are the same picture in different formats.
		"misc/animated.webp + misc/plain.bmp + misc/rotated.tif + misc/still.webp",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing cluster %q", w)
		}
	}
	if len(clusters) != len(want) {
		t.Errorf("got %d clusters, want %d: %v", len(clusters), len(want), got)
	}
	// The larger original is proposed as keeper.
	for _, c := range clusters {
		if c.Members[0] == byPath["2019/Holiday/beach_small.jpg"] {
			t.Error("small copy chosen as keeper")
		}
	}

	// Tag the duplicate, then resolve all proposals.
	small := byPath["2019/Holiday/beach_small.jpg"]
	if err := library.AddTags(ctx, b, []int64{small}, []string{"favourite"}); err != nil {
		t.Fatal(err)
	}
	res := Proposals(clusters)
	n, err := Resolve(ctx, b, res)
	if err != nil {
		t.Fatal(err)
	}
	// 1 + 1 + 1 from the pairs, and all 3 fixture copies besides the
	// keeper. (Lossy WebP decoded with JPEG's colour range used to look
	// different enough that the animated one was not pre-marked.)
	if n != 6 {
		t.Errorf("trashed %d, want 6", n)
	}
	s.MarkResolved(res)
	if left := s.Clusters(DefaultThreshold); len(left) != 0 {
		t.Errorf("clusters remain after resolve: %d", len(left))
	}
	beach, _ := library.GetImage(ctx, b, byPath["2019/Holiday/beach.jpg"])
	if len(beach.Tags) != 1 || beach.Tags[0] != "favourite" {
		t.Errorf("keeper should inherit tags, got %v", beach.Tags)
	}
	smallIm, _ := library.GetImage(ctx, b, small)
	if smallIm.DeletedAt == 0 || smallIm.MergedInto != beach.ID {
		t.Errorf("duplicate not merged: %+v", smallIm)
	}
	// A new scan does not see trashed images.
	s2, _ := NewScan(ctx, b, Params{Mode: ModeSimilar}, nil)
	if c := s2.Clusters(DefaultThreshold); len(c) != 0 {
		t.Errorf("rescan found %d clusters", len(c))
	}
	// Restore undoes the merge.
	if _, err := library.Restore(ctx, b, []int64{small}); err != nil {
		t.Fatal(err)
	}
	smallIm, _ = library.GetImage(ctx, b, small)
	if smallIm.DeletedAt != 0 || smallIm.MergedInto != 0 {
		t.Errorf("restore: %+v", smallIm)
	}
}

func TestNotDuplicatesPersist(t *testing.T) {
	b, byPath := setup(t)
	ctx := context.Background()
	a, c := byPath["2020/Party/party_03.jpg"], byPath["2020/Party/party_03_edit.jpg"]
	if _, err := Resolve(ctx, b, []Resolution{{Keep: a, KeepAlso: []int64{c}}}); err != nil {
		t.Fatal(err)
	}
	s, _ := NewScan(ctx, b, Params{Mode: ModeSimilar}, nil)
	for _, cl := range s.Clusters(DefaultThreshold) {
		if names(cl.Members, byPath) == "2020/Party/party_03.jpg + 2020/Party/party_03_edit.jpg" {
			t.Error("pair declared distinct was proposed again")
		}
	}
}

func TestChainKeepsDuplicatesAdjacent(t *testing.T) {
	b, byPath := setup(t)
	ctx := context.Background()
	ids, _ := library.ListIDs(ctx, b, query.ImageQuery{}, query.Sort{})
	order, err := similar.Order(ctx, b, ids, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != len(ids) {
		t.Fatalf("order has %d ids, want %d", len(order), len(ids))
	}
	pos := map[int64]int{}
	for i, id := range order {
		pos[id] = i
	}
	d := pos[byPath["2019/Holiday/beach.jpg"]] - pos[byPath["2019/Holiday/beach_small.jpg"]]
	if d != 1 && d != -1 {
		t.Errorf("beach and its copy are %d apart in similarity order", d)
	}
	target := byPath["2020/Party/party_03.jpg"]
	byT, _ := similar.Order(ctx, b, ids, target)
	if byT[0] != target || byT[1] != byPath["2020/Party/party_03_edit.jpg"] {
		t.Errorf("similar-to order starts %v", byT[:2])
	}
}
