package server

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/comfy/comfytest"
	"photobag/internal/jobs"
)

func TestGenerateAPI(t *testing.T) {
	dir := t.TempDir()
	b, err := bag.Open(filepath.Join(dir, "g.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	fake := comfytest.New()
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

	// Without an address, generating is refused with a pointer to the setting.
	var check comfyCheck
	if st := c.do("POST", "/api/comfy/check", map[string]string{}, nil); st != http.StatusBadRequest {
		t.Fatalf("check without address: %d", st)
	}
	c.do("POST", "/api/comfy/check", map[string]string{"endpoint": strings.TrimPrefix(fake.URL, "http://")}, &check)
	if !check.OK || !strings.Contains(check.Message, "0.99.0-fake on cuda:0 Fake GPU") {
		t.Fatalf("check %+v", check)
	}
	var set struct {
		Endpoint string `json:"endpoint"`
	}
	c.do("PUT", "/api/comfy/settings", map[string]string{"endpoint": fake.URL + "/"}, &set)
	if set.Endpoint != fake.URL {
		t.Errorf("endpoint %q", set.Endpoint)
	}
	var nodes struct {
		Nodes   map[string]struct{ Inputs []struct{ Name, Type string } } `json:"nodes"`
		Missing []string                                                  `json:"missing"`
	}
	c.do("GET", "/api/comfy/nodes?class=KSampler&class=Nope", nil, &nodes)
	if len(nodes.Nodes["KSampler"].Inputs) != 10 || len(nodes.Missing) != 1 {
		t.Errorf("nodes %+v", nodes)
	}

	// Workflows: the editor format is refused, the API format kept.
	if st := c.do("POST", "/api/workflows", map[string]string{"name": "x", "json": `{"nodes":[],"links":[]}`}, nil); st != http.StatusBadRequest {
		t.Errorf("editor format: %d", st)
	}
	var wf struct {
		Workflow struct {
			ID        int64 `json:"id"`
			VersionID int64 `json:"versionId"`
		} `json:"workflow"`
	}
	c.do("POST", "/api/workflows", map[string]string{"name": "T2I", "json": comfytest.Workflow}, &wf)
	if st := c.do("POST", "/api/workflows", map[string]string{"name": "t2i", "json": comfytest.Workflow}, nil); st != http.StatusConflict {
		t.Errorf("duplicate name: %d", st)
	}

	var exp struct {
		ID int64 `json:"id"`
	}
	c.do("POST", "/api/experiments", map[string]string{"name": "Steps"}, &exp)
	req := map[string]any{"request": map[string]any{
		"workflowId": wf.Workflow.ID, "count": 1,
		"overrides": []any{map[string]any{"node": "KSampler", "input": "steps", "sweep": map[string]any{"range": map[string]any{"from": 3, "to": 5, "step": 1}}}},
	}}
	var plan struct {
		Prompts int    `json:"prompts"`
		Error   string `json:"error"`
	}
	c.do("POST", "/api/experiments/"+itoa(exp.ID)+"/plan", req, &plan)
	if plan.Prompts != 3 || plan.Error != "" {
		t.Fatalf("plan %+v", plan)
	}
	bad := map[string]any{"request": map[string]any{"workflowId": wf.Workflow.ID, "overrides": []any{map[string]any{"node": "Nope", "input": "x", "value": 1}}}}
	if st := c.do("POST", "/api/experiments/"+itoa(exp.ID)+"/generate", bad, nil); st != http.StatusBadRequest {
		t.Errorf("bad request: %d", st)
	}
	var job jobs.Job
	c.do("POST", "/api/experiments/"+itoa(exp.ID)+"/generate", req, &job)
	if job = c.waitJob(job.ID); job.Status != jobs.Done {
		t.Fatalf("job %+v", job)
	}

	var gens []struct {
		ID      int64  `json:"id"`
		SHA256  string `json:"sha256"`
		ImageID int64  `json:"imageId"`
	}
	c.do("GET", "/api/experiments/"+itoa(exp.ID)+"/generations", nil, &gens)
	if len(gens) != 3 {
		t.Fatalf("%d generations", len(gens))
	}
	var runs []struct {
		Status string `json:"status"`
		Images int    `json:"images"`
		JobID  string `json:"jobId"`
	}
	c.do("GET", "/api/experiments/"+itoa(exp.ID)+"/runs", nil, &runs)
	if len(runs) != 1 || runs[0].Status != "done" || runs[0].Images != 3 || runs[0].JobID != job.ID {
		t.Errorf("runs %+v", runs)
	}
	for _, path := range []string{"/api/generations/" + itoa(gens[0].ID) + "/preview?size=800", "/api/thumbs/" + gens[0].SHA256,
		"/api/generations/" + itoa(gens[0].ID) + "/original"} {
		res, err := c.http.Get(c.base + path)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Errorf("%s: %d", path, res.StatusCode)
		}
	}
	var eff struct {
		JSON string `json:"json"`
	}
	c.do("GET", "/api/generations/"+itoa(gens[1].ID)+"/workflow", nil, &eff)
	if !strings.Contains(eff.JSON, `"steps": 4`) {
		t.Errorf("effective workflow %s", eff.JSON)
	}

	// Move one to the library with a tag; its detail names the generation.
	var mv struct {
		Moved    int     `json:"moved"`
		ImageIDs []int64 `json:"imageIds"`
	}
	c.do("POST", "/api/generations/move", map[string]any{"ids": []int64{gens[0].ID}, "tags": []string{"comfy"}}, &mv)
	if mv.Moved != 1 {
		t.Fatalf("move %+v", mv)
	}
	var detail struct {
		Image struct {
			Tags []string `json:"tags"`
		} `json:"image"`
		Generation *struct {
			ID      int64 `json:"id"`
			Current bool  `json:"current"`
		} `json:"generation"`
	}
	c.do("GET", "/api/images/"+itoa(mv.ImageIDs[0]), nil, &detail)
	if detail.Generation == nil || detail.Generation.ID != gens[0].ID || !detail.Generation.Current || len(detail.Image.Tags) != 1 {
		t.Errorf("image detail %+v", detail)
	}
	var stats struct {
		Images int `json:"images"`
	}
	c.do("GET", "/api/stats", nil, &stats)
	if stats.Images != 1 {
		t.Errorf("%d library images", stats.Images)
	}

	var discarded struct {
		Discarded int `json:"discarded"`
	}
	c.do("POST", "/api/generations/discard", map[string]any{"ids": []int64{gens[1].ID}}, &discarded)
	var saved struct {
		Workflow struct {
			Name string `json:"name"`
		} `json:"workflow"`
	}
	c.do("POST", "/api/generations/"+itoa(gens[2].ID)+"/save-workflow", map[string]string{"name": "Five steps"}, &saved)
	if discarded.Discarded != 1 || saved.Workflow.Name != "Five steps" {
		t.Errorf("discard %+v, saved %+v", discarded, saved)
	}
	// Discarding the newest generation and generating again reuses its id
	// (and its blob's): the preview must not come from what was cached for
	// the image discarded.
	last := gens[2]
	for _, g := range gens {
		if g.ID > last.ID {
			last = g
		}
	}
	getPreview := func(id int64, etag string) (*http.Response, []byte) {
		t.Helper()
		r, _ := http.NewRequest("GET", c.base+"/api/generations/"+itoa(id)+"/preview?size=800", nil)
		if etag != "" {
			r.Header.Set("If-None-Match", etag)
		}
		res, err := c.http.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return res, data
	}
	before, oldData := getPreview(last.ID, "")
	if last.ImageID != 0 || last.ID == gens[1].ID {
		t.Fatalf("the newest generation %+v is not the one left", last)
	}
	c.do("POST", "/api/generations/discard", map[string]any{"ids": []int64{last.ID}}, nil)
	// Two new ones: the second takes the discarded id (the first fills the
	// gap of the one discarded before).
	again := map[string]any{"request": map[string]any{"workflowId": wf.Workflow.ID, "count": 1,
		"overrides": []any{map[string]any{"node": "KSampler", "input": "seed", "sweep": map[string]any{"range": map[string]any{"from": 777, "to": 778, "step": 1}}}}}}
	c.do("POST", "/api/experiments/"+itoa(exp.ID)+"/generate", again, &job)
	if job = c.waitJob(job.ID); job.Status != jobs.Done {
		t.Fatalf("job %+v", job)
	}
	c.do("GET", "/api/experiments/"+itoa(exp.ID)+"/generations", nil, &gens)
	var reused bool
	for _, g := range gens {
		if g.ID == last.ID && g.SHA256 != last.SHA256 {
			reused = true
		}
	}
	if !reused {
		t.Fatalf("generation id %d was not used again: %+v", last.ID, gens)
	}
	if res, data := getPreview(last.ID, before.Header.Get("ETag")); res.StatusCode != 200 || string(data) == string(oldData) {
		t.Errorf("preview of a reused id: %d, same picture %v", res.StatusCode, string(data) == string(oldData))
	}

	if st := c.do("DELETE", "/api/experiments/"+itoa(exp.ID), nil, nil); st != 200 {
		t.Errorf("delete: %d", st)
	}
	if st := c.do("GET", "/api/experiments/"+itoa(exp.ID), nil, nil); st != http.StatusNotFound {
		t.Errorf("deleted experiment: %d", st)
	}
}
