package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"photobag/internal/imaging"
	"photobag/internal/testimg"
)

func TestPlanUpload(t *testing.T) {
	jpeg := testimg.JPEG(testimg.Scene(1, 64, 48), 80, testimg.EXIF{})
	png := testimg.PNG(testimg.Scene(2, 64, 48), testimg.EXIF{})
	head := func(b []byte) []byte { return b[:min(len(b), imaging.SniffLen)] }
	offers := []UploadOffer{
		{Path: "Holiday/beach.jpg", Size: 1000, Head: head(jpeg)},
		{Path: "Holiday/day 2/sea.png", Size: 2000, Head: head(png)},
		{Path: "Holiday/notes.txt", Size: 5, Head: []byte("hello")},
		{Path: "Holiday/.hidden.jpg", Size: 1000, Head: head(jpeg)},
		{Path: "Holiday/Thumbs.db", Size: 1000, Head: []byte("whatever")},
		{Path: "Holiday/@eaDir/beach.jpg", Size: 1000, Head: head(jpeg)},
		{Path: "HOLIDAY/BEACH.JPG", Size: 1000, Head: head(jpeg)},
		{Path: "bad:name.png", Size: 2000, Head: head(png)},
		{Path: "big.png", Size: MaxFileSize + 1, Head: head(png)},
		{Path: "../up.jpg", Size: 1000, Head: head(jpeg)},
		{Path: "phone.heic", Size: 100, Head: []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00")},
	}
	accepted, skip, bytes := PlanUpload(offers)
	want := map[string]string{"Holiday/beach.jpg": "Holiday/beach.jpg", "Holiday/day 2/sea.png": "Holiday/day 2/sea.png"}
	if len(accepted) != len(want) || bytes != 3000 {
		t.Errorf("accepted %v (%d bytes), want %v", accepted, bytes, want)
	}
	for k, v := range want {
		if accepted[k] != v {
			t.Errorf("accepted[%q] = %q, want %q", k, accepted[k], v)
		}
	}
	reasons := map[string]FileReport{}
	for _, fr := range skip {
		reasons[fr.Path] = fr
	}
	for path, want := range map[string]string{
		"Holiday/notes.txt":        "skipped:not a supported image",
		"Holiday/.hidden.jpg":      "skipped:hidden",
		"Holiday/Thumbs.db":        "skipped:hidden",
		"Holiday/@eaDir/beach.jpg": "skipped:hidden",
		"HOLIDAY/BEACH.JPG":        "skipped:same path",
		"bad:name.png":             "failed:must not contain",
		"big.png":                  "failed:too large",
		"../up.jpg":                "failed:..",
		"phone.heic":               "skipped:HEIC",
	} {
		status, reason, _ := strings.Cut(want, ":")
		fr, ok := reasons[path]
		if !ok || fr.Status != status || !strings.Contains(fr.Reason, reason) {
			t.Errorf("%s: %+v, want %s", path, fr, want)
		}
	}
	if len(skip)+len(accepted) != len(offers) {
		t.Errorf("%d skipped + %d accepted of %d offered", len(skip), len(accepted), len(offers))
	}

	for _, c := range []struct {
		paths []string
		want  string
	}{
		{[]string{"a.jpg"}, "a.jpg"},
		{[]string{"Trip/a.jpg", "Trip/b/c.jpg"}, "Trip"},
		{[]string{"Trip/a.jpg", "x.png"}, "Trip and x.png"},
		{[]string{"Trip/a.jpg", "x.png", "y.png", "z.png"}, "Trip, x.png and 2 more"},
	} {
		var o []UploadOffer
		for _, p := range c.paths {
			o = append(o, UploadOffer{Path: p})
		}
		if got := DescribeUpload(o); got != c.want {
			t.Errorf("DescribeUpload(%v) = %q, want %q", c.paths, got, c.want)
		}
	}
}

// An upload is imported from its folder under a name of its own, with
// what was left out before it in the report, and paths relative to it.
func TestImportUploadFolder(t *testing.T) {
	b, _, _ := setup(t)
	ctx := context.Background()
	dir := UploadDir(b.Path, "test")
	write := func(rel string, data []byte) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("Trip/a.jpg", testimg.JPEG(testimg.Scene(3, 64, 48), 80, testimg.EXIF{}))
	write("Trip/day 2/b.png", testimg.PNG(testimg.Scene(4, 64, 48), testimg.EXIF{}))
	write("Trip/c.jpg", []byte("\xFF\xD8\xFFnot really"))
	var last Progress
	rep, err := Run(ctx, b, dir, Options{
		Recursive: true, TagFolders: true,
		Source:  "Trip (uploaded)",
		LeftOut: []FileReport{{Path: "Trip/notes.txt", Status: "skipped", Reason: "not a supported image"}},
	}, func(p Progress) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	if rep.Source != "Trip (uploaded)" || rep.Found != 4 || rep.Added != 2 || rep.Skipped != 1 || rep.Failed != 1 {
		t.Errorf("report %+v", rep)
	}
	paths := map[string]string{}
	for _, f := range rep.Files {
		paths[f.Path] = f.Status
	}
	if paths["Trip/notes.txt"] != "skipped" || paths["Trip/c.jpg"] != "failed" {
		t.Errorf("file reports %+v", rep.Files)
	}
	if last.Done != last.Found || last.Found != 4 {
		t.Errorf("final progress %+v", last)
	}
	var source string
	if err := b.R.QueryRowContext(ctx, "SELECT source FROM imports WHERE id = ?", rep.ImportID).Scan(&source); err != nil || source != "Trip (uploaded)" {
		t.Errorf("imports.source = %q, %v", source, err)
	}
	var tags string
	if err := b.R.QueryRowContext(ctx, `SELECT group_concat(t.name, ',') FROM images i JOIN image_tags it ON it.image_id = i.id
		JOIN tags t ON t.id = it.tag_id WHERE i.name = 'b.png'`).Scan(&tags); err != nil || (tags != "Trip,day 2" && tags != "day 2,Trip") {
		t.Errorf("tags of b.png = %q, %v", tags, err)
	}

	CleanUploads(b.Path)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the upload folder is still there: %v", err)
	}
	if _, err := os.Stat(b.Path); err != nil {
		t.Errorf("the bag went with it: %v", err)
	}
}
