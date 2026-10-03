package server

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/importer"
	"photobag/internal/jobs"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/reencode"
	"photobag/internal/testimg"
)

func TestReencodeAPI(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	if _, err := testimg.WriteTree(src, 0.1); err != nil {
		t.Fatal(err)
	}
	b, err := bag.Open(filepath.Join(dir, "r.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	if _, err := importer.Run(ctx, b, src, importer.Options{Recursive: true}, nil); err != nil {
		t.Fatal(err)
	}
	srv := New(b, Config{Addr: "127.0.0.1:0", NoToken: true, KeyStore: filepath.Join(dir, "credentials.json"), TaggerDir: filepath.Join(dir, "tagger")})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithCancel(ctx)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(sctx) }()
	defer func() {
		cancel()
		<-served
	}()
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: strings.TrimSuffix(srv.URL(), "/"), http: &http.Client{Jar: jar}}
	get := func(path string, hdr ...string) (*http.Response, []byte) {
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
		return res, data
	}
	wait := func(bt reencode.Batch) reencode.Detail {
		t.Helper()
		if bt.JobID == "" {
			t.Fatalf("no job for %+v", bt)
		}
		if j := c.waitJob(bt.JobID); j.Status != jobs.Done {
			t.Fatalf("job %+v", j)
		}
		var d reencode.Detail
		c.do("GET", "/api/reencode/"+strconv.FormatInt(bt.ID, 10), nil, &d)
		return d
	}

	// The PNGs, by query: lossless WebP replaces them straight away.
	var bt reencode.Batch
	st := c.do("POST", "/api/reencode", map[string]any{"query": map[string]any{"formats": []string{"png"}},
		"settings": map[string]any{"format": "webp", "lossless": true, "effort": 2}}, &bt)
	if st != 200 || bt.Mode != reencode.ModeReplace || bt.Counts.Total != 2 || !strings.Contains(bt.Description, "PNG") {
		t.Fatalf("create: %d %+v", st, bt)
	}
	d := wait(bt)
	if d.State != reencode.StateFinished || d.Counts.Replaced+d.Counts.Skipped != 2 {
		t.Fatalf("replace batch %+v", d.Batch)
	}
	if n, _ := library.Count(ctx, b, query.ImageQuery{Formats: []string{"png"}}); n != d.Counts.Skipped {
		t.Errorf("%d PNGs left", n)
	}

	// JPEGs, scaled down for review.
	ids, _ := library.ListIDs(ctx, b, query.ImageQuery{NameGlob: "party_0[12].jpg"}, query.Sort{Field: query.SortName})
	var est reencode.Estimate
	if st := c.do("POST", "/api/reencode/try", map[string]any{"ids": ids, "settings": map[string]any{"format": "jpeg", "quality": 70, "maxWidth": 60}}, &est); st != 200 || est.Tried != 2 || est.NewBytes >= est.OldBytes {
		t.Errorf("try: %d %+v", st, est)
	}
	if st := c.do("POST", "/api/reencode", map[string]any{"ids": ids, "settings": map[string]any{"format": "jpeg", "quality": 0}}, nil); st != http.StatusBadRequest {
		t.Errorf("bad settings: %d", st)
	}
	c.do("POST", "/api/reencode", map[string]any{"ids": ids, "settings": map[string]any{"format": "jpeg", "quality": 70, "maxWidth": 60, "onlySmaller": true}}, &bt)
	d = wait(bt)
	if bt.Mode != reencode.ModeReview || d.Counts.Ready != 2 || d.Items[0].NewWidth != 60 {
		t.Fatalf("review batch %+v %+v", d.Batch, d.Items)
	}
	path := "/api/reencode/" + strconv.FormatInt(bt.ID, 10)
	if res, data := get(path + "/result/" + strconv.FormatInt(ids[0], 10)); res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || len(data) != int(d.Items[0].NewSize) {
		t.Errorf("result: %d %s %d", res.StatusCode, res.Header.Get("Content-Type"), len(data))
	}
	if st := c.do("DELETE", path, nil, nil); st != 200 {
		// Finished batches can go; this one is finished.
		t.Errorf("delete: %d", st)
	}
	c.do("POST", "/api/reencode", map[string]any{"ids": ids, "settings": map[string]any{"format": "jpeg", "quality": 70, "maxWidth": 60}}, &bt)
	wait(bt)
	path = "/api/reencode/" + strconv.FormatInt(bt.ID, 10)
	var dec reencode.Decided
	if st := c.do("POST", path+"/decide", map[string]any{"ids": ids[:1], "replace": true}, &dec); st != 200 || dec.Replaced != 1 {
		t.Errorf("decide: %d %+v", st, dec)
	}
	var detail struct {
		Reencodes []reencode.Change `json:"reencodes"`
	}
	c.do("GET", "/api/images/"+strconv.FormatInt(ids[0], 10), nil, &detail)
	if len(detail.Reencodes) != 1 || detail.Reencodes[0].NewWidth != 60 {
		t.Errorf("image history %+v", detail.Reencodes)
	}
	if st := c.do("POST", path+"/resume", nil, nil); st != http.StatusBadRequest {
		t.Errorf("resume with nothing left: %d", st)
	}

	// Originals revalidate, and come upright on request.
	tif, _ := library.ListIDs(ctx, b, query.ImageQuery{Formats: []string{"tiff"}}, query.Sort{})
	res, _ := get("/api/images/" + strconv.FormatInt(tif[0], 10) + "/original?view=1")
	if res.Header.Get("Content-Type") != "image/png" || res.Header.Get("Cache-Control") != "private, no-cache" {
		t.Errorf("tiff view: %v", res.Header)
	}
	if res, _ := get("/api/images/"+strconv.FormatInt(tif[0], 10)+"/original", "If-None-Match", res.Header.Get("ETag")); res.StatusCode != http.StatusNotModified {
		t.Errorf("revalidate original: %d", res.StatusCode)
	}
	var list []reencode.Batch
	if c.do("GET", "/api/reencode", nil, &list); len(list) != 2 {
		t.Errorf("%d batches", len(list))
	}
}
