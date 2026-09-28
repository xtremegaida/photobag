package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/importer"
	"photobag/internal/jobs"
	"photobag/internal/llm/llmtest"
	"photobag/internal/testimg"
)

func TestAnalysisAPI(t *testing.T) {
	t.Setenv("PHOTOBAG_API_KEY", "")
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

	fake := llmtest.New(func(r llmtest.Request) (string, int) {
		if r.Auth != "Bearer sk-test-1234abcd" {
			return "Incorrect API key provided", 401
		}
		switch {
		case strings.Contains(r.Prompt, "Say hi"):
			return "Hi!", 0
		case strings.Contains(r.Prompt, "alt text"):
			return "People at a party.", 0
		case strings.Contains(r.Prompt, "Transcribe"):
			return "HAPPY BIRTHDAY", 0
		case strings.Contains(r.Prompt, "Classify"):
			return "Events", 0
		}
		return "?", 0
	})
	defer fake.Close()

	keyFile := filepath.Join(dir, "config", "credentials.json")
	srv := New(b, Config{Addr: "127.0.0.1:0", NoToken: true, KeyStore: keyFile})
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

	type view struct {
		Settings map[string]any `json:"settings"`
		Key      keyInfo        `json:"key"`
	}
	var v view
	c.do("GET", "/api/analysis/settings", nil, &v)
	if v.Key.Set || v.Settings["endpoint"] != "" || v.Settings["concurrency"] != float64(2) {
		t.Fatalf("initial settings %+v", v)
	}
	// Starting without an endpoint is refused.
	if st := c.do("POST", "/api/jobs/analyze", map[string]any{"pipelines": []string{"caption"}}, nil); st != http.StatusBadRequest {
		t.Errorf("analyze without endpoint: %d", st)
	}

	set := v.Settings
	set["endpoint"] = fake.URL + "/chat/completions" // pasted full URL is trimmed
	set["model"] = "fake-vision"
	set["category"] = map[string]any{"levels": 1, "categories": "Events\nNature", "prefix": "cat:"}
	if st := c.do("PUT", "/api/analysis/settings", map[string]any{"settings": set, "apiKey": "sk-test-1234abcd"}, &v); st != 200 {
		t.Fatalf("save settings: %d", st)
	}
	if !v.Key.Set || v.Key.Hint != "…abcd" || v.Settings["endpoint"] != fake.URL {
		t.Errorf("saved settings %+v", v)
	}
	// The key lives in the credentials file, not in the bag.
	stored, _ := b.Meta(context.Background(), "analysis.settings")
	creds, _ := os.ReadFile(keyFile)
	if strings.Contains(stored, "sk-test") || !strings.Contains(string(creds), "sk-test-1234abcd") {
		t.Errorf("key placement: meta %s creds %s", stored, creds)
	}

	var check struct {
		OK      bool     `json:"ok"`
		Message string   `json:"message"`
		Models  []string `json:"models"`
	}
	c.do("POST", "/api/analysis/check", map[string]any{}, &check)
	if check.OK || len(check.Models) != 1 || check.Message == "" { // the fake cannot read the test image
		t.Errorf("check %+v", check)
	}
	c.do("POST", "/api/analysis/check", map[string]any{"apiKey": "wrong"}, &check)
	if check.OK || !strings.Contains(check.Message, "Incorrect API key") {
		t.Errorf("check with a wrong key %+v", check)
	}

	opts := map[string]any{"pipelines": []string{"caption", "ocr", "category"}}
	var plan struct {
		Requests int `json:"requests"`
		Todo     int `json:"todo"`
	}
	c.do("POST", "/api/analysis/plan", opts, &plan)
	if plan.Requests != 3*n || plan.Todo != n {
		t.Fatalf("plan %+v for %d images", plan, n)
	}
	var job jobs.Job
	if st := c.do("POST", "/api/jobs/analyze", opts, &job); st != 200 {
		t.Fatalf("start analysis: %d", st)
	}
	job = c.waitJob(job.ID)
	res := job.Result.(map[string]any)
	if job.Status != jobs.Done || res["stored"] != float64(3*n) {
		t.Fatalf("analysis job %+v", job)
	}

	var ids struct {
		IDs []int64 `json:"ids"`
	}
	c.do("POST", "/api/images/ids", map[string]any{"query": map[string]any{"text": "birthday"}}, &ids)
	if len(ids.IDs) != n {
		t.Fatalf("text search found %d of %d", len(ids.IDs), n)
	}
	id := itoa(ids.IDs[0])
	var detail struct {
		Image struct {
			Caption string   `json:"caption"`
			Tags    []string `json:"tags"`
		} `json:"image"`
		Analyses []struct {
			Pipeline string `json:"pipeline"`
			Text     string `json:"text"`
			Edited   bool   `json:"edited"`
		} `json:"analyses"`
	}
	c.do("GET", "/api/images/"+id, nil, &detail)
	if detail.Image.Caption != "People at a party." || len(detail.Analyses) != 3 || strings.Join(detail.Image.Tags, ",") != "cat:Events" {
		t.Errorf("detail %+v", detail)
	}

	// A correction is kept; trying a prompt stores nothing.
	c.do("PUT", "/api/images/"+id+"/analysis/caption", map[string]any{"text": "A birthday party."}, nil)
	set["caption"] = map[string]any{"prompt": "Say hi."}
	var tried struct {
		ImageID  int64 `json:"imageId"`
		Outcomes []struct {
			Text   string `json:"text"`
			Stored bool   `json:"stored"`
		} `json:"outcomes"`
	}
	c.do("POST", "/api/analysis/try", map[string]any{"settings": set, "imageId": ids.IDs[0], "pipelines": []string{"caption"}}, &tried)
	if len(tried.Outcomes) != 1 || tried.Outcomes[0].Text != "Hi!" || tried.Outcomes[0].Stored {
		t.Errorf("try %+v", tried)
	}
	c.do("GET", "/api/images/"+id, nil, &detail)
	if detail.Image.Caption != "A birthday party." || !detail.Analyses[0].Edited {
		t.Errorf("after try %+v", detail)
	}

	// Analyse one image now; remove a result and its tags.
	var now struct {
		Outcomes []struct {
			Stored bool `json:"stored"`
		} `json:"outcomes"`
	}
	c.do("POST", "/api/images/"+id+"/analyze", map[string]any{"pipelines": []string{"ocr"}}, &now)
	if len(now.Outcomes) != 1 || !now.Outcomes[0].Stored {
		t.Errorf("analyze now %+v", now)
	}
	c.do("DELETE", "/api/images/"+id+"/analysis/category", nil, nil)
	c.do("GET", "/api/images/"+id, nil, &detail)
	if len(detail.Analyses) != 2 || len(detail.Image.Tags) != 0 {
		t.Errorf("after delete %+v", detail)
	}
	var removed struct {
		Removed int `json:"removed"`
	}
	c.do("POST", "/api/analysis/remove-tags", map[string]any{"pipeline": "category"}, &removed)
	if removed.Removed != n-1 {
		t.Errorf("removed %d tags, want %d", removed.Removed, n-1)
	}
	var stats []struct {
		Pipeline string `json:"pipeline"`
		Analysed int    `json:"analysed"`
		Edited   int    `json:"edited"`
		TagLinks int    `json:"tagLinks"`
	}
	c.do("GET", "/api/analysis/stats", nil, &stats)
	if len(stats) != 4 || stats[0].Pipeline != "caption" || stats[0].Analysed != n || stats[0].Edited != 1 || stats[3].Analysed != n-1 || stats[3].TagLinks != 0 {
		t.Errorf("stats %+v", stats)
	}
}
