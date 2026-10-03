package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"photobag/internal/bag"
	"photobag/internal/jobs"
	"photobag/internal/testimg"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil && res.StatusCode < 300 {
			c.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return res.StatusCode
}

func (c *client) waitJob(id string) jobs.Job {
	c.t.Helper()
	var j jobs.Job
	for i := 0; i < 200; i++ {
		c.do("GET", "/api/jobs/"+id, nil, &j)
		if j.Status != jobs.Queued && j.Status != jobs.Running {
			return j
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.t.Fatalf("job %s did not finish", id)
	return j
}

func TestBagTag(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{KeyStore: filepath.Join(dir, "credentials.json"), TaggerDir: filepath.Join(dir, "tagger")}
	tag := func(name string) string {
		t.Helper()
		b, err := bag.Open(filepath.Join(dir, name), bag.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		s := New(b, cfg)
		defer s.cancel()
		return s.bagTag()
	}
	a := tag("a.photobag")
	if len(a) != 12 || tag("a.photobag") != a {
		t.Errorf("tag %q is not stable", a)
	}
	if tag("b.photobag") == a {
		t.Error("two bags share a tag")
	}
}

func TestServerEndToEnd(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	if _, err := testimg.WriteTree(src, 0.15); err != nil {
		t.Fatal(err)
	}
	bagPath := filepath.Join(dir, "srv.photobag")
	b, err := bag.Open(bagPath, bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(b, Config{Addr: "127.0.0.1:0"})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()
	base := strings.TrimSuffix(strings.Split(srv.URL(), "?")[0], "/")

	// Without the token everything is refused.
	res, err := http.Get(base + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated stats: %d", res.StatusCode)
	}
	// A foreign Host header is refused (DNS rebinding).
	req, _ := http.NewRequest("GET", base+"/api/stats", nil)
	req.Host = "evil.example:80"
	req.Header.Set("X-PhotoBag-Token", srv.Token())
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign host: %d", res.StatusCode)
	}

	// The launch URL swaps the token for a cookie.
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: base, http: &http.Client{Jar: jar}}
	res, err = c.http.Get(srv.URL())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Request.URL.RawQuery != "" {
		t.Fatalf("token exchange: %d %s", res.StatusCode, res.Request.URL)
	}

	// Cross-site POSTs are rejected even with the cookie.
	req, _ = http.NewRequest("POST", base+"/api/tags", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	res, _ = c.http.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site POST: %d", res.StatusCode)
	}

	// Event stream: expect job events while importing.
	evReq, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/events", nil)
	evRes, err := c.http.Do(evReq)
	if err != nil {
		t.Fatal(err)
	}
	gotJobEvent := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(evRes.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") && strings.Contains(sc.Text(), `"type":"job"`) {
				gotJobEvent <- true
				return
			}
		}
	}()

	var job jobs.Job
	if st := c.do("POST", "/api/jobs/import", map[string]any{"path": src, "options": map[string]any{"recursive": true, "tagFolders": true}}, &job); st != 200 {
		t.Fatalf("start import: %d", st)
	}
	job = c.waitJob(job.ID)
	if job.Status != jobs.Done {
		t.Fatalf("import job %+v", job)
	}
	select {
	case <-gotJobEvent:
	case <-time.After(5 * time.Second):
		t.Error("no job event on the event stream")
	}

	var ids struct {
		IDs   []int64 `json:"ids"`
		Total int     `json:"total"`
	}
	c.do("POST", "/api/images/ids", map[string]any{"query": map[string]any{"tagsAll": []string{"party"}}, "sort": map[string]any{"field": "name"}}, &ids)
	if ids.Total != 7 {
		t.Fatalf("party images: %d", ids.Total)
	}
	for _, p := range []string{"/thumb", "/preview?size=800", "/original"} {
		res, err := c.http.Get(base + "/api/images/" + itoa(ids.IDs[0]) + p)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || n == 0 {
			t.Errorf("GET %s: %d (%d bytes)", p, res.StatusCode, n)
		}
	}
	// Previews are validated by content, not by ids that can be used again.
	var detail struct {
		Image struct {
			SHA256 string `json:"sha256"`
		} `json:"image"`
	}
	c.do("GET", "/api/images/"+itoa(ids.IDs[0]), nil, &detail)
	im := detail.Image
	pres, err := c.http.Get(base + "/api/images/" + itoa(ids.IDs[0]) + "/preview?size=800")
	if err != nil {
		t.Fatal(err)
	}
	pres.Body.Close()
	if etag := pres.Header.Get("ETag"); im.SHA256 == "" || !strings.HasPrefix(etag, `"`+im.SHA256+"-800-") {
		t.Errorf("preview ETag %s for sha %s", etag, im.SHA256)
	}

	// Scoring over the API: answer everything with a fixed preference.
	var run struct {
		ID int64 `json:"id"`
	}
	c.do("POST", "/api/runs", map[string]any{"metricName": "Sharpness", "query": map[string]any{"ids": ids.IDs[:3]}}, &run)
	var pair struct {
		Done  bool  `json:"done"`
		Pos   int64 `json:"pos"`
		Left  int64 `json:"left"`
		Right int64 `json:"right"`
	}
	c.do("GET", "/api/runs/"+itoa(run.ID)+"/next", nil, &pair)
	for i := 0; !pair.Done && i < 10; i++ {
		c.do("POST", "/api/runs/"+itoa(run.ID)+"/answer", map[string]any{"pos": pair.Pos, "winner": min(pair.Left, pair.Right)}, &pair)
	}
	if st := c.do("POST", "/api/runs/"+itoa(run.ID)+"/answer", map[string]any{"pos": 0, "winner": ids.IDs[0]}, nil); st != http.StatusConflict {
		t.Errorf("stale answer status %d, want 409", st)
	}
	var ranks struct {
		Rankings []struct {
			ImageID int64 `json:"imageId"`
		} `json:"rankings"`
	}
	c.do("GET", "/api/metrics/1/rankings", nil, &ranks)
	if len(ranks.Rankings) != 3 || ranks.Rankings[0].ImageID != min(ids.IDs[0], min(ids.IDs[1], ids.IDs[2])) {
		t.Errorf("rankings %+v", ranks.Rankings)
	}

	// Backup for download is consumed by one download.
	c.do("POST", "/api/jobs/backup", map[string]any{"mode": "download"}, &job)
	job = c.waitJob(job.ID)
	url := job.Result.(map[string]any)["url"].(string)
	res, _ = c.http.Get(base + url)
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.HasPrefix(data, []byte("SQLite format 3")) {
		t.Fatalf("backup download: %d", res.StatusCode)
	}
	res, _ = c.http.Get(base + url)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("second download: %d", res.StatusCode)
	}

	// The SPA fallback serves something for client routes.
	res, _ = c.http.Get(base + "/scoring/runs/1")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("SPA route: %d", res.StatusCode)
	}

	// Graceful shutdown leaves a single file.
	cancel()
	evRes.Body.Close()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "srv.photobag-") || strings.Contains(e.Name(), ".backup-") {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
