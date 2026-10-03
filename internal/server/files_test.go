package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/files"
	"photobag/internal/jobs"
)

// l0 is the first folder at the top level.
func l0(c *client, t *testing.T) files.Node {
	t.Helper()
	var l files.Listing
	c.do("GET", "/api/files", nil, &l)
	for _, n := range l.Children {
		if n.Dir {
			return n
		}
	}
	t.Fatal("no folder")
	return files.Node{}
}

func TestFilesAPI(t *testing.T) {
	dir := t.TempDir()
	b, err := bag.Open(filepath.Join(dir, "f.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	srv := New(b, Config{Addr: "127.0.0.1:0", NoToken: true, KeyStore: filepath.Join(dir, "credentials.json"), TaggerDir: filepath.Join(dir, "tagger")})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()
	defer func() {
		cancel()
		<-served
	}()
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: strings.TrimSuffix(srv.URL(), "/"), http: &http.Client{Jar: jar}}

	upload := func(parent int64, path, body, conflict string) (int, files.PutResult) {
		t.Helper()
		q := url.Values{"path": {path}, "conflict": {conflict}, "modified": {"1700000000000"}}
		if parent != 0 {
			q.Set("parent", strconv.FormatInt(parent, 10))
		}
		req, _ := http.NewRequest("PUT", c.base+"/api/files/upload?"+q.Encode(), strings.NewReader(body))
		res, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out files.PutResult
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	get := func(path string, hdr ...string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", c.base+path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		res, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return res, string(data)
	}

	st, up := upload(0, "notes/Read me.md", "# Hello\n\nSee ![](img/x.png).", "")
	if st != 200 || up.Outcome != files.Added || up.Node.Kind != files.KindMarkdown || up.Node.ModifiedAt != 1700000000000 {
		t.Fatalf("upload: %d %+v", st, up)
	}
	note := up.Node
	if st, up := upload(0, "notes/read ME.md", "other", files.ConflictSkip); st != 200 || up.Outcome != files.Skipped {
		t.Errorf("skip: %d %+v", st, up)
	}
	_, page := upload(0, "notes/page.html", "<script>alert(1)</script>", "")
	if st, _ := upload(0, "notes/PAGE.html", "x", files.ConflictFail); st != http.StatusConflict {
		t.Errorf("fail on conflict: %d", st)
	}

	// Saving an edit in place: refused (412) when the file is no longer the
	// version the edit started from.
	save := func(id int64, body, ifSHA string) (int, files.Node) {
		t.Helper()
		req, _ := http.NewRequest("PUT", c.base+"/api/files/"+strconv.FormatInt(id, 10)+"/content", strings.NewReader(body))
		if ifSHA != "" {
			req.Header.Set("If-Match", `"`+ifSHA+`"`)
		}
		res, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var n files.Node
		json.NewDecoder(res.Body).Decode(&n)
		return res.StatusCode, n
	}
	_, draft := upload(0, "notes/draft.txt", "one", "")
	st, edited := save(draft.Node.ID, "two", draft.Node.SHA256)
	if st != 200 || edited.ID != draft.Node.ID || edited.Name != "draft.txt" || edited.Size != 3 || edited.SHA256 == draft.Node.SHA256 {
		t.Errorf("save: %d %+v", st, edited)
	}
	if st, _ := save(draft.Node.ID, "three", draft.Node.SHA256); st != http.StatusPreconditionFailed {
		t.Errorf("stale save: %d", st)
	}
	if st, n := save(draft.Node.ID, "three", ""); st != 200 || n.Size != 5 {
		t.Errorf("unconditional save: %d %+v", st, n)
	}
	if st, _ := save(l0(c, t).ID, "x", ""); st != http.StatusBadRequest {
		t.Errorf("saving a folder: %d", st)
	}
	c.do("POST", "/api/files/delete", map[string]any{"ids": []int64{draft.Node.ID}}, nil)

	var l files.Listing
	if st := c.do("GET", "/api/files?path=notes", nil, &l); st != 200 || len(l.Children) != 2 || len(l.Path) != 1 {
		t.Fatalf("browse: %d %+v", st, l)
	}
	notes := l.Path[0]

	// Content: text as plain text, sandboxed, with ranges and revalidation.
	res, body := get("/api/files/" + strconv.FormatInt(note.ID, 10) + "/content")
	if body != "# Hello\n\nSee ![](img/x.png)." || res.Header.Get("Content-Type") != "text/plain; charset=utf-8" ||
		res.Header.Get("Content-Security-Policy") != "sandbox" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "inline") {
		t.Errorf("content %q %v", body, res.Header)
	}
	if res, body := get("/api/files/"+strconv.FormatInt(note.ID, 10)+"/content", "Range", "bytes=2-6"); res.StatusCode != 206 || body != "Hello" {
		t.Errorf("range: %d %q", res.StatusCode, body)
	}
	if res, _ := get("/api/files/"+strconv.FormatInt(note.ID, 10)+"/content", "If-None-Match", res.Header.Get("ETag")); res.StatusCode != 304 {
		t.Errorf("revalidate: %d", res.StatusCode)
	}
	if res, _ := get("/api/files/" + strconv.FormatInt(page.Node.ID, 10) + "/content"); res.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("html served as %s", res.Header.Get("Content-Type"))
	}
	if res, body := get("/api/files-raw/NOTES/read%20me.md"); res.StatusCode != 200 || !strings.HasPrefix(body, "# Hello") {
		t.Errorf("by path: %d %q", res.StatusCode, body)
	}

	// Folders, rename, move, conflicts.
	var arch files.Node
	if st := c.do("POST", "/api/files/folders", map[string]any{"name": "Archive"}, &arch); st != 200 {
		t.Fatalf("mkdir %d", st)
	}
	if st := c.do("POST", "/api/files/folders", map[string]any{"name": "archive"}, nil); st != http.StatusConflict {
		t.Errorf("duplicate folder: %d", st)
	}
	if st := c.do("PATCH", "/api/files/"+strconv.FormatInt(note.ID, 10), map[string]any{"name": "a/b"}, nil); st != http.StatusBadRequest {
		t.Errorf("bad name: %d", st)
	}
	var moved files.MoveResult
	if st := c.do("POST", "/api/files/move", map[string]any{"ids": []int64{notes.ID}, "parent": arch.ID}, &moved); st != 200 || moved.Moved != 1 {
		t.Errorf("move: %d %+v", st, moved)
	}
	if st := c.do("POST", "/api/files/move", map[string]any{"ids": []int64{arch.ID}, "parent": notes.ID}, nil); st != http.StatusBadRequest {
		t.Errorf("cycle: %d", st)
	}
	var check struct{ Existing []string }
	c.do("POST", "/api/files/check", map[string]any{"parent": arch.ID, "paths": []string{"notes/page.html", "notes/new.txt"}}, &check)
	if len(check.Existing) != 1 || check.Existing[0] != "notes/page.html" {
		t.Errorf("check %+v", check)
	}

	res, body = get("/api/files/zip?id=" + strconv.FormatInt(arch.ID, 10))
	if res.StatusCode != 200 || !strings.HasPrefix(body, "PK") || !strings.Contains(res.Header.Get("Content-Disposition"), "Archive.zip") {
		t.Errorf("zip: %d %v", res.StatusCode, res.Header)
	}

	// Export to disk as a job, then import it back somewhere else.
	out := filepath.Join(dir, "out")
	var j jobs.Job
	c.do("POST", "/api/jobs/files-export", map[string]any{"path": out, "ids": []int64{arch.ID}}, &j)
	if j = c.waitJob(j.ID); j.Status != jobs.Done {
		t.Fatalf("export job %+v", j)
	}
	if data, err := os.ReadFile(filepath.Join(out, "Archive", "notes", "Read me.md")); err != nil || !strings.HasPrefix(string(data), "# Hello") {
		t.Fatalf("exported %q %v", data, err)
	}
	var back files.Node
	c.do("POST", "/api/files/folders", map[string]any{"name": "Back"}, &back)
	c.do("POST", "/api/jobs/files-import", map[string]any{"path": filepath.Join(out, "Archive"), "parent": back.ID}, &j)
	if j = c.waitJob(j.ID); j.Status != jobs.Done {
		t.Fatalf("import job %+v", j)
	}
	if st := c.do("GET", "/api/files?path=Back/Archive/notes/page.html", nil, &l); st != 200 || l.Node == nil || l.Node.SHA256 != page.Node.SHA256 {
		t.Errorf("imported back: %d %+v", st, l.Node)
	}

	var del files.DeleteResult
	if st := c.do("POST", "/api/files/delete", map[string]any{"ids": []int64{arch.ID, back.ID}}, &del); st != 200 || del.Files != 4 || del.Folders != 5 {
		t.Errorf("delete: %d %+v", st, del)
	}
	var sum files.Summary
	if c.do("GET", "/api/files/summary", nil, &sum); sum != (files.Summary{}) {
		t.Errorf("summary after delete %+v", sum)
	}
	if res, _ := get("/api/files/" + strconv.FormatInt(note.ID, 10) + "/content"); res.StatusCode != 404 {
		t.Errorf("deleted content: %d", res.StatusCode)
	}
}
