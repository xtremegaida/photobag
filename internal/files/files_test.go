package files_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"photobag/internal/bag"
	"photobag/internal/files"
)

func open(t *testing.T) *bag.Bag {
	t.Helper()
	b, err := bag.Open(filepath.Join(t.TempDir(), "f.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func store(t *testing.T, b *bag.Bag, parent int64, path, content, conflict string) *files.PutResult {
	t.Helper()
	r, err := files.Store(context.Background(), b, strings.NewReader(content), files.PutOptions{Parent: parent, Path: path, Conflict: conflict})
	if err != nil {
		t.Fatalf("store %s: %v", path, err)
	}
	return r
}

func read(t *testing.T, b *bag.Bag, id int64) string {
	t.Helper()
	r, _, err := files.Open(context.Background(), b, id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func names(nodes []files.Node) []string {
	out := []string{}
	for _, n := range nodes {
		out = append(out, n.Name)
	}
	return out
}

func contents(t *testing.T, b *bag.Bag) (n int, bytes int64) {
	t.Helper()
	if err := b.R.QueryRow("SELECT count(*), COALESCE(sum(size), 0) FROM file_contents").Scan(&n, &bytes); err != nil {
		t.Fatal(err)
	}
	return
}

func TestTypes(t *testing.T) {
	for _, c := range []struct{ name, sniffed, typ, kind string }{
		{"notes.md", "", "text/markdown", files.KindMarkdown},
		{"README", "text/plain; charset=utf-8", "text/plain", files.KindText},
		{"data.JSON", "", "application/json", files.KindText},
		{"page.html", "", "text/html", files.KindText},
		{"photo.jpg", "", "image/jpeg", files.KindImage},
		{"manual.pdf", "", "application/pdf", files.KindPDF},
		{"blob.bin", "application/octet-stream", "application/octet-stream", files.KindOther},
		{"mystery", "text/plain; charset=utf-8", "text/plain", files.KindText},
		{".gitignore", "", "text/plain", files.KindText},
	} {
		typ, kind := files.TypeOf(c.name, c.sniffed)
		if typ != c.typ || kind != c.kind {
			t.Errorf("TypeOf(%q) = %s %s, want %s %s", c.name, typ, kind, c.typ, c.kind)
		}
	}
	if _, err := files.SplitPath("a/../b"); !errors.Is(err, files.ErrInvalid) {
		t.Errorf("..: %v", err)
	}
	if got, _ := files.SplitPath(`a\b/./c/`); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("split %v", got)
	}
	if _, err := files.CleanName("CON.txt"); !errors.Is(err, files.ErrInvalid) {
		t.Errorf("reserved name: %v", err)
	}
}

func TestTree(t *testing.T) {
	ctx := context.Background()
	b := open(t)

	// Paths create their folders; a big file spans several chunks and
	// reads back exactly, including from the middle.
	big := strings.Repeat("0123456789abcdef", files.ChunkSize/16*2+1000)
	r := store(t, b, 0, "docs/guide/big.txt", big, "")
	if r.Outcome != files.Added || r.Node.Size != int64(len(big)) || r.Node.Kind != files.KindText {
		t.Fatalf("stored %+v", r.Node)
	}
	if got := read(t, b, r.Node.ID); got != big {
		t.Fatalf("read back %d bytes, want %d", len(got), len(big))
	}
	rd, _, _ := files.Open(ctx, b, r.Node.ID)
	rd.Seek(files.ChunkSize-3, io.SeekStart)
	part := make([]byte, 10)
	if _, err := io.ReadFull(rd, part); err != nil || string(part) != big[files.ChunkSize-3:files.ChunkSize+7] {
		t.Fatalf("read across chunks: %q %v", part, err)
	}

	docs, err := files.Resolve(ctx, b, "DOCS") // names match ignoring case
	if err != nil || docs == nil || !docs.Dir || docs.Size != int64(len(big)) || docs.Items != 1 {
		t.Fatalf("resolve docs: %+v %v", docs, err)
	}
	l, err := files.Browse(ctx, b, "docs/guide/big.txt")
	if err != nil || l.Node.ID != r.Node.ID || len(l.Path) != 3 || l.Children != nil {
		t.Fatalf("browse file: %+v %v", l, err)
	}
	if p, _ := files.PathOf(ctx, b, r.Node.ID); p != "docs/guide/big.txt" {
		t.Errorf("path %q", p)
	}

	// Identical content is stored once.
	a := store(t, b, docs.ID, "a.md", "# Notes\n", "")
	a2 := store(t, b, 0, "copy.md", "# Notes\n", "")
	if n, _ := contents(t, b); n != 2 || a.Node.SHA256 != a2.Node.SHA256 {
		t.Errorf("contents %d, shas %s %s", n, a.Node.SHA256, a2.Node.SHA256)
	}

	// Name clashes: keep both, replace, or skip.
	if r := store(t, b, docs.ID, "A.md", "second", ""); r.Outcome != files.Renamed || r.Node.Name != "A (2).md" {
		t.Errorf("rename: %+v", r)
	}
	if r := store(t, b, docs.ID, "a.md", "third", files.ConflictReplace); r.Outcome != files.Replaced || r.Node.ID != a.Node.ID || read(t, b, a.Node.ID) != "third" {
		t.Errorf("replace: %+v", r)
	}
	if r := store(t, b, docs.ID, "a.md", "fourth", files.ConflictSkip); r.Outcome != files.Skipped || read(t, b, a.Node.ID) != "third" {
		t.Errorf("skip: %+v", r)
	}
	if _, err := files.Store(ctx, b, strings.NewReader("x"), files.PutOptions{Path: "copy.md/inner.txt"}); !errors.Is(err, files.ErrInvalid) {
		t.Errorf("file in the way of a folder: %v", err)
	}
	if n, _ := contents(t, b); n != 4 { // big, "# Notes", "second", "third"
		t.Errorf("after clashes %d contents", n)
	}

	// Folders: create, rename (case only too), conflicts.
	if _, err := files.MakeDir(ctx, b, 0, "Docs"); !errors.Is(err, files.ErrConflict) {
		t.Errorf("duplicate folder: %v", err)
	}
	arch, err := files.MakeDir(ctx, b, 0, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := files.Rename(ctx, b, docs.ID, "Docs"); err != nil || n.Name != "Docs" {
		t.Errorf("case rename: %+v %v", n, err)
	}
	if _, err := files.Rename(ctx, b, arch.ID, "copy.MD"); !errors.Is(err, files.ErrConflict) {
		t.Errorf("rename onto another: %v", err)
	}

	// Moves keep both on a clash and refuse cycles.
	guide, _ := files.Resolve(ctx, b, "docs/guide")
	if _, err := files.Move(ctx, b, []int64{docs.ID}, guide.ID); !errors.Is(err, files.ErrInvalid) {
		t.Errorf("move into itself: %v", err)
	}
	store(t, b, arch.ID, "copy.md", "older", "")
	mr, err := files.Move(ctx, b, []int64{a2.Node.ID, docs.ID}, arch.ID)
	if err != nil || mr.Moved != 2 || mr.Renamed != 1 {
		t.Fatalf("move %+v %v", mr, err)
	}
	kids, _ := files.List(ctx, b, arch.ID)
	if got := names(kids); !slices.Equal(got, []string{"Docs", "copy (2).md", "copy.md"}) {
		t.Errorf("archive holds %v", got)
	}
	top, _ := files.List(ctx, b, 0)
	if got := names(top); !slices.Equal(got, []string{"Archive"}) {
		t.Errorf("top holds %v", got)
	}

	// Deleting a folder takes everything in it, and contents nothing uses.
	dr, err := files.Delete(ctx, b, []int64{docs.ID})
	if err != nil || dr.Files != 3 || dr.Folders != 2 {
		t.Fatalf("delete %+v %v", dr, err)
	}
	if n, _ := contents(t, b); n != 2 { // "# Notes" (copy (2).md) and "older"
		t.Errorf("after delete %d contents", n)
	}
	if _, err := files.Get(ctx, b, r.Node.ID); !errors.Is(err, files.ErrNotFound) {
		t.Errorf("deleted file: %v", err)
	}
	s, _ := files.GetSummary(ctx, b)
	if s.Files != 2 || s.Folders != 1 || s.Bytes != int64(len("# Notes\n")+len("older")) {
		t.Errorf("summary %+v", s)
	}

	// Unfinished uploads are cleared away.
	if _, err := b.W.Exec("INSERT INTO file_contents(created_at) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if err := files.Tidy(ctx, b); err != nil {
		t.Fatal(err)
	}
	if n, _ := contents(t, b); n != 2 {
		t.Errorf("after tidy %d contents", n)
	}
}

func TestTransfer(t *testing.T) {
	ctx := context.Background()
	b := open(t)
	src := filepath.Join(t.TempDir(), "Notes")
	mtime := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	write := func(rel, content string) {
		p := filepath.Join(src, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, mtime, mtime)
	}
	write("readme.md", "# Readme")
	write("sub/a.txt", "alpha")
	write(".hidden/secret.txt", "no")
	write("Thumbs.db", "junk")
	os.MkdirAll(filepath.Join(src, "empty"), 0o755)

	rep, err := files.Import(ctx, b, src, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Added != 2 || rep.Failed != 0 || rep.Folders != 3 { // Notes, empty, sub
		t.Fatalf("import %+v", rep)
	}
	l, err := files.Browse(ctx, b, "Notes")
	if err != nil || !slices.Equal(names(l.Children), []string{"empty", "sub", "readme.md"}) {
		t.Fatalf("imported %v %v", names(l.Children), err)
	}
	readme, _ := files.Resolve(ctx, b, "notes/readme.md")
	if readme.ModifiedAt != mtime.UnixMilli() {
		t.Errorf("mtime %d", readme.ModifiedAt)
	}
	// Importing again skips what is there.
	if rep, _ := files.Import(ctx, b, src, 0, files.ConflictSkip, nil); rep.Skipped != 2 || rep.Added != 0 {
		t.Errorf("reimport %+v", rep)
	}

	dest := t.TempDir()
	rep, err = files.Export(ctx, b, nil, dest, "", nil)
	if err != nil || rep.Added != 2 || rep.Folders != 3 {
		t.Fatalf("export %+v %v", rep, err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "Notes", "sub", "a.txt"))
	st, _ := os.Stat(filepath.Join(dest, "Notes", "readme.md"))
	if string(got) != "alpha" || st == nil || !st.ModTime().Equal(mtime) {
		t.Errorf("exported %q %v", got, st)
	}
	if _, err := os.Stat(filepath.Join(dest, "Notes", "empty")); err != nil {
		t.Errorf("empty folder: %v", err)
	}
	// Exporting again keeps both.
	if rep, _ := files.Export(ctx, b, []int64{readme.ID}, filepath.Join(dest, "Notes"), "", nil); rep.Renamed != 1 {
		t.Errorf("re-export %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(dest, "Notes", "readme (2).md")); err != nil {
		t.Error(err)
	}

	var buf bytes.Buffer
	if err := files.WriteZip(ctx, b, nil, &buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var entries []string
	for _, f := range zr.File {
		entries = append(entries, f.Name)
		if f.Name == "Notes/sub/a.txt" {
			rc, _ := f.Open()
			data, _ := io.ReadAll(rc)
			if string(data) != "alpha" {
				t.Errorf("zip content %q", data)
			}
		}
	}
	if want := []string{"Notes/", "Notes/empty/", "Notes/sub/", "Notes/sub/a.txt", "Notes/readme.md"}; !slices.Equal(entries, want) {
		t.Errorf("zip entries %v", entries)
	}
}
