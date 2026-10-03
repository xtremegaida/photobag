package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/imaging"
	"photobag/internal/importer"
	"photobag/internal/jobs"
	"photobag/internal/testimg"
)

func sortedList(s string) string {
	parts := strings.Split(s, ",")
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

func TestImportUpload(t *testing.T) {
	dir := t.TempDir()
	b, err := bag.Open(filepath.Join(dir, "u.photobag"), bag.Options{})
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

	jpeg := testimg.JPEG(testimg.Scene(1, 64, 48), 80, testimg.EXIF{})
	png := testimg.PNG(testimg.Scene(2, 64, 48), testimg.EXIF{})
	offer := func(path string, data []byte) importer.UploadOffer {
		return importer.UploadOffer{Path: path, Size: int64(len(data)), Head: data[:min(len(data), imaging.SniffLen)]}
	}
	send := func(id, path string, data []byte) int {
		t.Helper()
		q := url.Values{"path": {path}, "modified": {"1700000000000"}}
		req, _ := http.NewRequest("PUT", c.base+"/api/imports/uploads/"+id+"/files?"+q.Encode(), strings.NewReader(string(data)))
		res, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	var plan importer.UploadPlan
	c.do("POST", "/api/imports/uploads", map[string]any{"files": []importer.UploadOffer{
		offer("Trip/a.jpg", jpeg),
		offer("Trip/day 2/b.png", png),
		offer("Trip/notes.txt", []byte("some notes")),
		offer("Trip/c.jpg", jpeg), // offered, never sent
	}}, &plan)
	if plan.ID == "" || len(plan.Skip) != 1 || plan.Skip[0].Path != "Trip/notes.txt" {
		t.Fatalf("plan %+v", plan)
	}
	waiting := importer.UploadDir(b.Path, plan.ID)
	if st, err := os.Stat(waiting); err != nil || !st.IsDir() {
		t.Fatalf("no upload folder: %v", err)
	}
	if st := send(plan.ID, "Trip/a.jpg", jpeg); st != 200 {
		t.Errorf("upload a.jpg: %d", st)
	}
	if st := send(plan.ID, "Trip/day 2/b.png", png); st != 200 {
		t.Errorf("upload b.png: %d", st)
	}
	if st := send(plan.ID, "Trip/notes.txt", []byte("some notes")); st != http.StatusBadRequest {
		t.Errorf("upload of a file left out: %d", st)
	}
	if st := send("nonesuch", "Trip/a.jpg", jpeg); st != http.StatusNotFound {
		t.Errorf("upload to no upload: %d", st)
	}

	var job jobs.Job
	c.do("POST", "/api/imports/uploads/"+plan.ID+"/import", map[string]any{
		"options": map[string]any{"tagFolders": true, "tags": []string{"Uploaded"}},
	}, &job)
	if job = c.waitJob(job.ID); job.Status != jobs.Done || job.Kind != "import" || job.Title != "Import Trip (uploaded)" {
		t.Fatalf("import job %+v", job)
	}
	var rep importer.Report
	data, _ := json.Marshal(job.Result)
	json.Unmarshal(data, &rep)
	if rep.Found != 4 || rep.Added != 2 || rep.Skipped != 1 || rep.Failed != 1 || rep.Source != "Trip (uploaded)" {
		t.Errorf("report %+v", rep)
	}
	for _, f := range rep.Files {
		if f.Path == "Trip/c.jpg" && f.Reason != "not uploaded" {
			t.Errorf("c.jpg: %+v", f)
		}
	}
	if _, err := os.Stat(waiting); !os.IsNotExist(err) {
		t.Errorf("the upload folder was not removed: %v", err)
	}
	var tags string
	var mtime int64
	if err := b.R.QueryRowContext(ctx, `SELECT group_concat(t.name, ','), i.file_mtime FROM images i
		JOIN image_tags it ON it.image_id = i.id JOIN tags t ON t.id = it.tag_id WHERE i.name = 'b.png'`).Scan(&tags, &mtime); err != nil {
		t.Fatal(err)
	}
	if sortedList(tags) != "Trip,Uploaded,day 2" || mtime != 1700000000000 {
		t.Errorf("b.png: tags %q, modified %d", tags, mtime)
	}
	if st := send(plan.ID, "Trip/c.jpg", jpeg); st != http.StatusNotFound {
		t.Errorf("upload after the import: %d", st)
	}

	// Cancelled: the files are thrown away.
	c.do("POST", "/api/imports/uploads", map[string]any{"files": []importer.UploadOffer{offer("x.jpg", jpeg)}}, &plan)
	if st := send(plan.ID, "x.jpg", jpeg); st != 200 {
		t.Errorf("upload x.jpg: %d", st)
	}
	if st := c.do("DELETE", "/api/imports/uploads/"+plan.ID, nil, nil); st != 200 {
		t.Errorf("cancel: %d", st)
	}
	if _, err := os.Stat(importer.UploadDir(b.Path, plan.ID)); !os.IsNotExist(err) {
		t.Errorf("the cancelled upload's folder is still there: %v", err)
	}
	if st := c.do("POST", "/api/imports/uploads/"+plan.ID+"/import", map[string]any{}, nil); st != http.StatusNotFound {
		t.Errorf("import of a cancelled upload: %d", st)
	}
	if st := c.do("POST", "/api/imports/uploads", map[string]any{"files": []any{}}, nil); st != http.StatusBadRequest {
		t.Errorf("empty offer: %d", st)
	}
}
