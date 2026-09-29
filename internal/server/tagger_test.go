package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/importer"
	"photobag/internal/jobs"
	"photobag/internal/tagger/taggertest"
	"photobag/internal/testimg"
)

func TestTaggerAPI(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	if _, err := testimg.WriteTree(src, 0.1); err != nil {
		t.Fatal(err)
	}
	b, err := bag.Open(filepath.Join(dir, "a.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	rep, err := importer.Run(context.Background(), b, filepath.Join(src, "2020"), importer.Options{Recursive: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := rep.Added
	fake := taggertest.New(func(r taggertest.Request) (taggertest.Reply, int) {
		return taggertest.Reply{General: map[string]float64{"outdoors": 0.9, "sky": 0.5}, Rating: "general", Score: 0.99}, 0
	})
	defer fake.Close()

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

	var status struct {
		State        string `json:"state"`
		Installation struct {
			Installed bool   `json:"installed"`
			Problem   string `json:"problem"`
		} `json:"installation"`
	}
	c.do("GET", "/api/analysis/tagger", nil, &status)
	if status.State != "stopped" || status.Installation.Installed || !strings.Contains(status.Installation.Problem, "photobag tagger install") {
		t.Fatalf("status %+v", status)
	}

	var v struct {
		Settings map[string]any `json:"settings"`
	}
	c.do("GET", "/api/analysis/settings", nil, &v)
	set := v.Settings
	db := set["danbooru"].(map[string]any)
	tg := db["tagger"].(map[string]any)
	db["source"], db["addTags"], tg["local"] = "tagger", true, true
	// The local tagger is not installed.
	if st := c.do("POST", "/api/analysis/tagger/check", map[string]any{"settings": set}, nil); st != http.StatusBadRequest {
		t.Fatalf("check without installation: %d", st)
	}
	tg["local"], tg["endpoint"] = false, strings.TrimPrefix(fake.URL, "http://")
	var check struct {
		OK   bool     `json:"ok"`
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	c.do("POST", "/api/analysis/tagger/check", map[string]any{"settings": set}, &check)
	if !check.OK || check.Name != "fake-tagger" || len(check.Tags) != 2 {
		t.Fatalf("check %+v", check)
	}
	if st := c.do("PUT", "/api/analysis/settings", map[string]any{"settings": set}, &v); st != 200 {
		t.Fatalf("save: %d", st)
	}
	if got := v.Settings["danbooru"].(map[string]any)["tagger"].(map[string]any)["endpoint"]; got != fake.URL {
		t.Fatalf("saved endpoint %v", got)
	}

	// Danbooru tags run without a vision model; captions need one.
	if st := c.do("POST", "/api/jobs/analyze", map[string]any{"pipelines": []string{"caption"}}, nil); st != http.StatusBadRequest {
		t.Errorf("caption without an endpoint: %d", st)
	}
	var job jobs.Job
	if st := c.do("POST", "/api/jobs/analyze", map[string]any{"pipelines": []string{"danbooru"}}, &job); st != 200 {
		t.Fatalf("start: %d", st)
	}
	job = c.waitJob(job.ID)
	if res := job.Result.(map[string]any); job.Status != jobs.Done || res["stored"] != float64(n) || res["model"] != "fake-tagger" {
		t.Fatalf("job %+v", job)
	}
	var ids struct {
		IDs []int64 `json:"ids"`
	}
	c.do("POST", "/api/images/ids", map[string]any{"query": map[string]any{"text": "rating:general"}}, &ids)
	if len(ids.IDs) != n {
		t.Fatalf("search found %d of %d", len(ids.IDs), n)
	}
	var detail struct {
		Image struct {
			Tags []string `json:"tags"`
		} `json:"image"`
		Analyses []struct {
			Pipeline string             `json:"pipeline"`
			Rating   string             `json:"rating"`
			Scores   map[string]float64 `json:"scores"`
		} `json:"analyses"`
	}
	c.do("GET", "/api/images/"+itoa(ids.IDs[0]), nil, &detail)
	if len(detail.Analyses) != 1 || detail.Analyses[0].Rating != "general" || detail.Analyses[0].Scores["sky"] != 0.5 ||
		strings.Join(detail.Image.Tags, ",") != "outdoors,rating:general,sky" {
		t.Fatalf("detail %+v", detail)
	}
	c.do("POST", "/api/analysis/tagger/stop", nil, &status)
	if status.State != "stopped" {
		t.Fatalf("stop %+v", status)
	}
}
