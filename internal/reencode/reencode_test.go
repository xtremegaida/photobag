package reencode_test

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"path/filepath"
	"strings"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/importer"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/reencode"
	"photobag/internal/testimg"
)

func setup(t *testing.T) (*bag.Bag, string, map[string]int64) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	if _, err := testimg.WriteTree(src, 0.15); err != nil {
		t.Fatal(err)
	}
	b, err := bag.Open(filepath.Join(dir, "r.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	ctx := context.Background()
	if _, err := importer.Run(ctx, b, src, importer.Options{Recursive: true}, nil); err != nil {
		t.Fatal(err)
	}
	ids, _ := library.ListIDs(ctx, b, query.ImageQuery{}, query.Sort{Field: query.SortName})
	ims, _ := library.GetImages(ctx, b, ids)
	byName := map[string]int64{}
	for _, im := range ims {
		byName[im.OriginalPath] = im.ID
	}
	return b, src, byName
}

func count(t *testing.T, b *bag.Bag, q string, args ...any) int {
	t.Helper()
	var n int
	if err := b.R.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSettings(t *testing.T) {
	if _, err := (reencode.Settings{Format: "gif"}).Normalize(); !errors.Is(err, reencode.ErrInvalid) {
		t.Errorf("gif: %v", err)
	}
	if _, err := (reencode.Settings{Format: "jpeg", Quality: 0}).Normalize(); !errors.Is(err, reencode.ErrInvalid) {
		t.Errorf("quality 0: %v", err)
	}
	s, err := reencode.Settings{Format: "png", Effort: 9, Quality: 50}.Normalize()
	if err != nil || !s.Lossless || s.Effort != 2 || s.Quality != 0 {
		t.Errorf("png %+v %v", s, err)
	}
	if m := reencode.DefaultSettings().DefaultMode(); m != reencode.ModeReplace {
		t.Errorf("lossless default mode %s", m)
	}
	lossy := reencode.Settings{Format: "webp", Quality: 80, Effort: 4, MaxWidth: 2000}
	if lossy.DefaultMode() != reencode.ModeReview || lossy.Describe() != "WebP q80, at most 2000 wide" {
		t.Errorf("lossy %s %q", lossy.DefaultMode(), lossy.Describe())
	}
	if d := (reencode.Settings{Format: "webp", Lossless: true, MaxWidth: 10, MaxHeight: 20}).Describe(); d != "lossless WebP, at most 10×20" {
		t.Errorf("describe %q", d)
	}
}

func TestReplaceAndReview(t *testing.T) {
	ctx := context.Background()
	b, src, ids := setup(t)

	// Replace at once, lossless: originals go, images keep their place.
	sunset, copySunset, portrait := ids["2019/Holiday/sunset.png"], ids["2020/copy of sunset.png"], ids["2020/portrait.jpg"]
	before, _ := library.GetImage(ctx, b, portrait)
	var oldSHA []byte
	b.R.QueryRow("SELECT sha256 FROM images WHERE id = ?", sunset).Scan(&oldSHA)
	s := reencode.DefaultSettings()
	s.OnlySmaller = false
	batch, err := reencode.Create(ctx, b, []int64{sunset, copySunset, portrait, ids["misc/anim.gif"], ids["misc/animated.webp"],
		ids["misc/rotated.tif"], ids["misc/plain.bmp"]}, s, "", "test")
	if err != nil || batch.Mode != reencode.ModeReplace || batch.Counts.Total != 7 {
		t.Fatalf("create %+v %v", batch, err)
	}
	prog, err := reencode.Run(ctx, b, batch.ID, nil)
	if err != nil || prog.Replaced != 5 || prog.Skipped != 2 || prog.Done != 7 {
		t.Fatalf("run %+v %v", prog, err)
	}
	after, _ := library.GetImage(ctx, b, portrait)
	if after.Format != "webp" || after.Name != "portrait.webp" || after.Width != before.Width || after.Height != before.Height ||
		after.SHA256 == before.SHA256 || after.ThumbW == 0 {
		t.Errorf("portrait %+v", after)
	}
	if n := count(t, b, "SELECT count(*) FROM blobs WHERE sha256 = ?", oldSHA); n != 0 {
		t.Error("the shared original was kept")
	}
	if h, _ := reencode.History(ctx, b, sunset); len(h) != 1 || h[0].OldFormat != "png" || h[0].NewFormat != "webp" {
		t.Errorf("history %+v", h)
	}
	d, _ := reencode.GetDetail(ctx, b, batch.ID)
	for _, it := range d.Items {
		if it.Name == "anim.gif" && (it.Status != reencode.Skipped || !strings.Contains(it.Reason, "animated")) {
			t.Errorf("gif %+v", it)
		}
	}

	// Importing the originals again skips them.
	rep, err := importer.Run(ctx, b, src, importer.Options{Recursive: true, SkipIdentical: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reencoded := 0
	for _, f := range rep.Files {
		if strings.Contains(f.Reason, "re-encoded") {
			reencoded++
		}
	}
	if reencoded != 5 { // sunset.png and its copy, portrait, rotated.tif, plain.bmp
		t.Errorf("re-import skipped %d originals: %+v", reencoded, rep.Files)
	}

	// Review: results wait, then are replaced, kept, or found stale.
	p1, p2, p3 := ids["2020/Party/party_01.jpg"], ids["2020/Party/party_02.jpg"], ids["2020/Party/party_03.jpg"]
	lossy := reencode.Settings{Format: "jpeg", Quality: 60, MaxWidth: 100, KeepMetadata: true, OnlySmaller: true}
	rb, err := reencode.Create(ctx, b, []int64{p1, p2, p3}, lossy, "", "party")
	if err != nil || rb.Mode != reencode.ModeReview {
		t.Fatalf("create review %+v %v", rb, err)
	}
	blobsBefore := count(t, b, "SELECT count(*) FROM blobs")
	if prog, err := reencode.Run(ctx, b, rb.ID, nil); err != nil || prog.Ready != 3 {
		t.Fatalf("review run %+v %v", prog, err)
	}
	if n := count(t, b, "SELECT count(*) FROM blobs"); n != blobsBefore+3 {
		t.Errorf("results held as %d new blobs", n-blobsBefore)
	}
	rd, _ := reencode.GetDetail(ctx, b, rb.ID)
	it := rd.Items[0]
	if it.Status != reencode.Ready || it.NewWidth != 100 || it.PSNR == nil || it.NewSHA256 == "" || it.NewSize >= it.OldSize {
		t.Fatalf("ready item %+v", it)
	}
	data, f, err := reencode.Result(ctx, b, rb.ID, p1)
	if _, derr := jpeg.Decode(bytes.NewReader(data)); err != nil || f != "jpeg" || derr != nil {
		t.Errorf("result %s %v %v", f, err, derr)
	}
	// p3 changes meanwhile (another batch replaces it).
	other, _ := reencode.Create(ctx, b, []int64{p3}, s, reencode.ModeReplace, "")
	reencode.Run(ctx, b, other.ID, nil)

	if d, err := reencode.Decide(ctx, b, rb.ID, []int64{p1}, true); err != nil || d.Replaced != 1 {
		t.Errorf("replace %+v %v", d, err)
	}
	if d, err := reencode.Decide(ctx, b, rb.ID, []int64{p2}, false); err != nil || d.Kept != 1 {
		t.Errorf("keep %+v %v", d, err)
	}
	if d, err := reencode.Decide(ctx, b, rb.ID, nil, true); err != nil || d.Stale != 1 {
		t.Errorf("stale %+v %v", d, err)
	}
	im1, _ := library.GetImage(ctx, b, p1)
	im2, _ := library.GetImage(ctx, b, p2)
	if im1.Width != 100 || im1.Format != "jpeg" || im2.Width == 100 {
		t.Errorf("after decisions: %dpx %s / %dpx", im1.Width, im1.Format, im2.Width)
	}
	// Kept and stale results are gone; the replaced original is gone; the
	// replaced result is now the image's.
	if n := count(t, b, "SELECT count(*) FROM blobs"); n != blobsBefore {
		t.Errorf("blobs %d, want %d", n, blobsBefore)
	}
	rb2, _ := reencode.Get(ctx, b, rb.ID)
	if c := rb2.Counts; c.Replaced != 1 || c.Kept != 1 || c.Skipped != 1 || rb2.ReplacedNew >= rb2.ReplacedOld {
		t.Errorf("counts %+v", rb2)
	}
	if err := reencode.Delete(ctx, b, rb.ID); err != nil {
		t.Fatal(err)
	}
	if h, _ := reencode.History(ctx, b, p1); len(h) != 1 || h[0].BatchID != 0 {
		t.Errorf("history outlives its batch: %+v", h)
	}

	// Emptying the trash drops results waiting for deleted images.
	p4 := ids["2020/Party/party_04.jpg"]
	tb, _ := reencode.Create(ctx, b, []int64{p4}, lossy, "", "")
	reencode.Run(ctx, b, tb.ID, nil)
	withResult := count(t, b, "SELECT count(*) FROM blobs")
	library.Trash(ctx, b, []int64{p4})
	if _, err := library.EmptyTrash(ctx, b, []int64{p4}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, b, "SELECT count(*) FROM blobs"); n != withResult-2 {
		t.Errorf("after purge %d blobs, want %d", n, withResult-2)
	}
	tbd, _ := reencode.Get(ctx, b, tb.ID)
	if tbd.Counts.Skipped != 1 {
		t.Errorf("purged item %+v", tbd.Counts)
	}

	// Stopped batches carry on where they left off.
	p5, p6 := ids["2020/Party/party_05.jpg"], ids["2020/Party/party_06.jpg"]
	sb, _ := reencode.Create(ctx, b, []int64{p5, p6}, lossy, "", "")
	reencode.SetState(ctx, b, sb.ID, reencode.StateRunning, "job", "")
	if err := reencode.Delete(ctx, b, sb.ID); !errors.Is(err, reencode.ErrBusy) {
		t.Errorf("delete running: %v", err)
	}
	reencode.Interrupted(ctx, b)
	if g, _ := reencode.Get(ctx, b, sb.ID); g.State != reencode.StateStopped {
		t.Errorf("interrupted state %s", g.State)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := reencode.Run(cctx, b, sb.ID, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled run: %v", err)
	}
	if prog, err := reencode.Run(ctx, b, sb.ID, nil); err != nil || prog.Ready != 2 || prog.Done != 2 {
		t.Errorf("resumed %+v %v", prog, err)
	}

	est, err := reencode.Try(ctx, b, []int64{p5, p6}, reencode.Settings{Format: "webp", Quality: 70, Effort: 2})
	if err != nil || est.Tried != 2 || est.NewBytes >= est.OldBytes || est.MinPSNR == nil || est.Exact {
		t.Errorf("estimate %+v %v", est, err)
	}
}
