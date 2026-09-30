package decks_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/decks"
	"photobag/internal/dedup"
	"photobag/internal/importer"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/testimg"
)

func setup(t *testing.T) (*bag.Bag, []int64) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	if _, err := testimg.WriteTree(src, 0.1); err != nil {
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
	ids, err := library.ListIDs(ctx, b, query.ImageQuery{}, query.Sort{Field: query.SortImported})
	if err != nil || len(ids) < 8 {
		t.Fatalf("%d images: %v", len(ids), err)
	}
	return b, ids
}

func TestSettings(t *testing.T) {
	s, err := decks.Settings{Crossfade: true, Loop: true}.Normalize()
	if err != nil || s != decks.DefaultSettings() {
		t.Errorf("empty settings = %+v, %v", s, err)
	}
	s, err = decks.Settings{Advance: decks.AdvanceManual, Interval: 2.25, Fade: 0.35, Fit: decks.FitCenter, Background: " #AbC "}.Normalize()
	if err != nil || s.Background != "#aabbcc" || s.Interval != 2.3 || s.Fade != 0.4 || s.Fit != decks.FitCenter {
		t.Errorf("normalised %+v, %v", s, err)
	}
	for _, bad := range []decks.Settings{
		{Advance: "sometimes"},
		{Interval: 0.5},
		{Interval: 4000},
		{Fade: 11},
		{Fit: "tile"},
		{Background: "red"},
		{Background: "#12345"},
	} {
		if _, err := bad.Normalize(); !errors.Is(err, decks.ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestMoveIDs(t *testing.T) {
	order := []int64{1, 2, 3, 4, 5, 6}
	for _, c := range []struct {
		ids    []int64
		before int64
		want   []int64
	}{
		{[]int64{5}, 2, []int64{1, 5, 2, 3, 4, 6}},
		{[]int64{5, 2}, 1, []int64{2, 5, 1, 3, 4, 6}}, // the block keeps its order
		{[]int64{1, 2}, 0, []int64{3, 4, 5, 6, 1, 2}},
		{[]int64{2, 3}, 3, []int64{1, 2, 3, 4, 5, 6}}, // before a moved image: before the next unmoved one
		{[]int64{1, 4}, 3, []int64{2, 1, 4, 3, 5, 6}},
		{[]int64{5, 6}, 6, []int64{1, 2, 3, 4, 5, 6}},
		{[]int64{3, 99}, 99, []int64{1, 2, 4, 5, 6, 3}},
		{[]int64{4}, 5, []int64{1, 2, 3, 4, 5, 6}},
	} {
		if got := decks.MoveIDs(order, c.ids, c.before); !slices.Equal(got, c.want) {
			t.Errorf("move %v before %d = %v, want %v", c.ids, c.before, got, c.want)
		}
	}
}

func deckIDs(t *testing.T, b *bag.Bag, id int64) []int64 {
	t.Helper()
	d, err := decks.Get(context.Background(), b, id)
	if err != nil {
		t.Fatal(err)
	}
	return d.IDs
}

func TestDecks(t *testing.T) {
	b, im := setup(t)
	ctx := context.Background()
	if _, err := decks.Create(ctx, b, "  ", "", nil); !errors.Is(err, decks.ErrInvalid) {
		t.Errorf("blank name: %v", err)
	}
	d, err := decks.Create(ctx, b, " Summer   trip ", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Summer trip" || d.Settings != decks.DefaultSettings() || len(d.IDs) != 0 {
		t.Errorf("created %+v", d)
	}
	if _, err := decks.Create(ctx, b, "SUMMER TRIP", "", nil); !errors.Is(err, decks.ErrConflict) {
		t.Errorf("duplicate name: %v", err)
	}

	// Adding keeps the order given and skips images already there.
	r, err := decks.Add(ctx, b, d.ID, []int64{im[3], im[1], im[2], im[1]})
	if err != nil || r.Added != 3 || r.Present != 0 {
		t.Fatalf("add = %+v, %v", r, err)
	}
	r, err = decks.Add(ctx, b, d.ID, []int64{im[2], im[0], 999999})
	if err != nil || r.Added != 1 || r.Present != 1 {
		t.Fatalf("second add = %+v, %v", r, err)
	}
	want := []int64{im[3], im[1], im[2], im[0]}
	if got := deckIDs(t, b, d.ID); !slices.Equal(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}

	if err := decks.Move(ctx, b, d.ID, []int64{im[0], im[3]}, im[2]); err != nil {
		t.Fatal(err)
	}
	want = []int64{im[1], im[3], im[0], im[2]}
	if got := deckIDs(t, b, d.ID); !slices.Equal(got, want) {
		t.Fatalf("after move = %v, want %v", got, want)
	}
	if err := decks.SetOrder(ctx, b, d.ID, []int64{im[2], im[1]}); err != nil {
		t.Fatal(err)
	}
	want = []int64{im[2], im[1], im[3], im[0]}
	if got := deckIDs(t, b, d.ID); !slices.Equal(got, want) {
		t.Fatalf("after sort = %v, want %v", got, want)
	}

	// Trashed images are hidden in place and come back when restored.
	if _, err := library.Trash(ctx, b, []int64{im[1]}); err != nil {
		t.Fatal(err)
	}
	if _, err := decks.Add(ctx, b, d.ID, []int64{im[4]}); err != nil {
		t.Fatal(err)
	}
	if r, _ := decks.Add(ctx, b, d.ID, []int64{im[1]}); r.Added != 0 {
		t.Error("added a trashed image")
	}
	g, _ := decks.Get(ctx, b, d.ID)
	if g.Count != 4 || g.Hidden != 1 || !slices.Equal(g.IDs, []int64{im[2], im[3], im[0], im[4]}) {
		t.Fatalf("with one trashed: %+v", g)
	}
	if err := decks.Move(ctx, b, d.ID, []int64{im[4]}, im[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Restore(ctx, b, []int64{im[1]}); err != nil {
		t.Fatal(err)
	}
	want = []int64{im[2], im[1], im[4], im[3], im[0]}
	if got := deckIDs(t, b, d.ID); !slices.Equal(got, want) {
		t.Fatalf("after restore = %v, want %v", got, want)
	}

	// A duplicate merged away is replaced by its keeper, or dropped when
	// the keeper is in the deck already.
	other, _ := decks.Create(ctx, b, "Other", "notes", &decks.Settings{Advance: decks.AdvanceManual})
	decks.Add(ctx, b, other.ID, []int64{im[5], im[6]})
	if _, err := dedup.Resolve(ctx, b, []dedup.Resolution{{Keep: im[7], Delete: []int64{im[3]}}, {Keep: im[6], Delete: []int64{im[5]}}}); err != nil {
		t.Fatal(err)
	}
	want = []int64{im[2], im[1], im[4], im[7], im[0]}
	if got := deckIDs(t, b, d.ID); !slices.Equal(got, want) {
		t.Fatalf("after dedup = %v, want %v", got, want)
	}
	if got := deckIDs(t, b, other.ID); !slices.Equal(got, []int64{im[6]}) {
		t.Fatalf("other deck after dedup = %v", got)
	}

	// Purging removes images from decks.
	library.Trash(ctx, b, []int64{im[4]})
	if _, err := library.EmptyTrash(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	g, _ = decks.Get(ctx, b, d.ID)
	if g.Hidden != 0 || !slices.Equal(g.IDs, []int64{im[2], im[1], im[7], im[0]}) {
		t.Fatalf("after purge: %+v", g)
	}

	n, err := decks.Remove(ctx, b, d.ID, []int64{im[1], im[5]})
	if err != nil || n != 1 {
		t.Fatalf("remove = %d, %v", n, err)
	}
	refs, _ := decks.ForImage(ctx, b, im[6])
	if len(refs) != 1 || refs[0].Name != "Other" {
		t.Errorf("decks of image = %+v", refs)
	}

	name, bg := "Summer 2026", "#FFF"
	u, err := decks.Update(ctx, b, d.ID, decks.Change{Name: &name, Settings: &decks.Settings{Fit: decks.FitCover, Background: bg}})
	if err != nil || u.Name != name || u.Settings.Fit != decks.FitCover || u.Settings.Background != "#ffffff" || u.Settings.Interval != 5 {
		t.Fatalf("update = %+v, %v", u, err)
	}
	if _, err := decks.Update(ctx, b, d.ID, decks.Change{Name: ptr("other")}); !errors.Is(err, decks.ErrConflict) {
		t.Errorf("rename to a taken name: %v", err)
	}

	list, err := decks.List(ctx, b)
	if err != nil || len(list) != 2 || list[0].ID != d.ID || list[0].Count != 3 || !slices.Equal(list[0].Covers, []int64{im[2], im[7], im[0]}) {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := decks.Delete(ctx, b, d.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	b.R.QueryRow("SELECT count(*) FROM deck_images WHERE deck_id = ?", d.ID).Scan(&rows)
	if _, err := decks.Get(ctx, b, d.ID); !errors.Is(err, decks.ErrNotFound) || rows != 0 {
		t.Errorf("after delete: %v, %d rows", err, rows)
	}
	if _, err := decks.Add(ctx, b, d.ID, []int64{im[0]}); !errors.Is(err, decks.ErrNotFound) {
		t.Errorf("add to a deleted deck: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
