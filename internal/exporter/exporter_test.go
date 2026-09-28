package exporter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"photobag/internal/backup"
	"photobag/internal/bag"
	"photobag/internal/imaging"
	"photobag/internal/importer"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/testimg"
)

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"beach.jpg":        "beach.jpg",
		"beach":            "beach.jpg",
		"a:b*c?.JPG":       "a_b_c_.JPG",
		"con.jpg":          "_con.jpg",
		"COM1":             "_COM1.jpg",
		"trailing. . ":     "trailing.jpg",
		"photo.jpeg":       "photo.jpeg",
		"sunset.png":       "sunset.png.jpg",
		"   ":              "image.jpg",
		"tab\there.jpg":    "tab_here.jpg",
		"LPT9.holiday.jpg": "_LPT9.holiday.jpg",
	}
	for in, want := range cases {
		if got := SafeName(in, imaging.JPEG); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SafeDir("2019/../Holi:day/ ./x"); got != "2019/Holi_day/x" {
		t.Errorf("SafeDir = %q", got)
	}
}

func sha(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestExportRoundTripAndBackup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	tree, err := testimg.WriteTree(src, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bag.Open(filepath.Join(dir, "rt.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	if _, err := importer.Run(ctx, b, src, importer.Options{Recursive: true, TagFolders: true}, nil); err != nil {
		t.Fatal(err)
	}
	images := tree.Count("image", "exact-dup", "near-dup", "flat")

	out := filepath.Join(dir, "out")
	rep, err := Run(ctx, b, out, Options{Manifest: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Written != images || rep.Failed != 0 {
		t.Fatalf("export %+v", rep)
	}
	// Flat export: two beach.jpg collide and one gets a suffix.
	if _, err := os.Stat(filepath.Join(out, "beach (2).jpg")); err != nil {
		t.Errorf("expected collision suffix: %v", err)
	}
	var doc struct {
		Images []ManifestEntry `json:"images"`
	}
	data, _ := os.ReadFile(filepath.Join(out, ManifestName))
	if err := json.Unmarshal(data, &doc); err != nil || len(doc.Images) != images {
		t.Fatalf("manifest: %v (%d entries)", err, len(doc.Images))
	}
	for _, e := range doc.Images {
		got := sha(t, filepath.Join(out, filepath.FromSlash(e.File)))
		want := sha(t, filepath.Join(src, filepath.FromSlash(e.OriginalPath)))
		if got != want || got != e.SHA256 {
			t.Errorf("%s: exported bytes differ from source %s", e.File, e.OriginalPath)
		}
	}

	// Second export into the same folder, filtered by tag and keeping the
	// structure; existing files are skipped.
	rep, err = Run(ctx, b, out, Options{Query: query.ImageQuery{TagsAll: []string{"party"}}, KeepStructure: true}, nil)
	if err != nil || rep.Written != 7 {
		t.Fatalf("tag export: %v %+v", err, rep)
	}
	if _, err := os.Stat(filepath.Join(out, "2020", "Party", "party_03_edit.jpg")); err != nil {
		t.Error(err)
	}
	rep, err = Run(ctx, b, out, Options{Query: query.ImageQuery{TagsAll: []string{"party"}}, KeepStructure: true, Existing: ExistingSkip}, nil)
	if err != nil || rep.Written != 0 || rep.Skipped != 7 {
		t.Fatalf("skip existing: %v %+v", err, rep)
	}

	// Backup: a standalone single file in DELETE mode with the same content.
	res, err := backup.To(ctx, b, filepath.Join(dir, "copy.photobag"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backup.To(ctx, b, res.Path); err == nil {
		t.Error("backup over an existing file should fail")
	}
	cp, err := bag.Open(res.Path, bag.Options{Journal: bag.JournalDelete})
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	s1, _ := library.GetStats(ctx, b)
	s2, _ := library.GetStats(ctx, cp)
	if s1.Images != s2.Images || s1.Blobs != s2.Blobs || s1.Tags != s2.Tags {
		t.Errorf("backup differs: %+v vs %+v", s1, s2)
	}
}
