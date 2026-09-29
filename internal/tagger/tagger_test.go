package tagger_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"photobag/internal/tagger"
	"photobag/internal/tagger/taggertest"
)

func TestNormalizeEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.20:8001":             "http://192.168.1.20:8001",
		" http://host:8000/ ":           "http://host:8000",
		"http://host:8000/tag/details":  "http://host:8000",
		"https://tagger.example/health": "https://tagger.example",
		"http://host/api/":              "http://host/api",
		"":                              "",
	} {
		if got := tagger.NormalizeEndpoint(in); got != want {
			t.Errorf("NormalizeEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
	if err := tagger.ValidateEndpoint("http://"); err == nil {
		t.Error("http:// accepted")
	}
}

func TestClient(t *testing.T) {
	fake := taggertest.New(func(r taggertest.Request) (taggertest.Reply, int) {
		return taggertest.Reply{
			General:    map[string]float64{"outdoors": 0.9, "sky": 0.6, "cloud": 0.2},
			Characters: map[string]float64{"hatsune_miku": 0.95},
			Rating:     "general", Score: 0.97,
		}, 0
	})
	defer fake.Close()
	c := tagger.New(fake.URL+"/", time.Minute)
	ctx := context.Background()
	h, err := c.Health(ctx)
	if err != nil || h.TagCount != 10861 || h.OnGPU() || c.Name() != "fake-tagger" {
		t.Fatalf("health %+v, %v, name %q", h, err, c.Name())
	}
	r, err := c.Tag(ctx, []byte("jpeg"), tagger.Options{GeneralThreshold: 0.5, CharacterThreshold: 0.9, Characters: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.General) != 2 || r.General[0].Tag != "outdoors" || len(r.Characters) != 0 || r.Rating == nil || r.Rating.Tag != "general" {
		t.Fatalf("result %+v", r)
	}
	req := fake.Requests()[0]
	if string(req.Image) != "jpeg" || req.Query.Get("general_threshold") != "0.5" || req.Query.Get("include_characters") != "false" {
		t.Fatalf("request %+v", req)
	}
}

func TestClientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		switch r.URL.Path {
		case "/busy/tag/details":
			if n < 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"detail":"Model not loaded"}`)
				return
			}
			fmt.Fprint(w, `{"general":[{"tag":"sky","score":0.8}],"characters":[],"rating":null}`)
		case "/bad/tag/details":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"detail":"Uploaded file is not a supported image"}`)
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`) // e.g. llama.cpp
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	r, err := tagger.FastRetries(tagger.New(srv.URL+"/busy", 0)).Tag(ctx, []byte("x"), tagger.Options{})
	if err != nil || len(r.General) != 1 || calls.Load() != 2 {
		t.Fatalf("503 not retried: %+v %v (%d calls)", r, err, calls.Load())
	}
	_, err = tagger.New(srv.URL+"/bad", 0).Tag(ctx, []byte("x"), tagger.Options{})
	if err == nil || tagger.IsFatal(err) || !strings.Contains(err.Error(), "not a supported image") {
		t.Fatalf("400: %v", err)
	}
	_, err = tagger.New(srv.URL+"/nowhere", 0).Tag(ctx, []byte("x"), tagger.Options{})
	if !tagger.IsFatal(err) || !strings.Contains(err.Error(), "not a WD tagger") {
		t.Fatalf("404: %v", err)
	}
	if _, err := tagger.New(srv.URL, 0).Health(ctx); !tagger.IsFatal(err) {
		t.Fatalf("a server that is not a tagger passed the health check: %v", err)
	}
	srv.Close()
	_, err = tagger.FastRetries(tagger.New(srv.URL, time.Second)).Tag(ctx, []byte("x"), tagger.Options{})
	if err == nil || tagger.IsFatal(err) || !strings.Contains(err.Error(), "could not be reached") {
		t.Fatalf("unreachable: %v", err)
	}
}

// fakeHF serves a model repository like Hugging Face: HEAD on resolve
// redirects (with the size and hash of LFS files), GET honours Range.
func fakeHF(t *testing.T, files map[string][]byte, cut *atomic.Bool) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/{file}", func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.PathValue("file")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.PathValue("file") == tagger.ModelFile {
			h := sha256.Sum256(data)
			w.Header().Set("X-Linked-Size", fmt.Sprint(len(data)))
			w.Header().Set("X-Linked-Etag", `"`+hex.EncodeToString(h[:])+`"`)
		} else {
			h := sha1.New()
			fmt.Fprintf(h, "blob %d\x00", len(data))
			h.Write(data)
			w.Header().Set("X-Linked-Etag", `"`+hex.EncodeToString(h.Sum(nil))+`"`)
		}
		http.Redirect(w, r, "/cdn/"+r.PathValue("file"), http.StatusFound)
	})
	mux.HandleFunc("/cdn/{file}", func(w http.ResponseWriter, r *http.Request) {
		data := files[r.PathValue("file")]
		if cut != nil && cut.Load() && r.Header.Get("Range") == "" && r.PathValue("file") == tagger.ModelFile {
			// Send half, then drop the connection.
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.Write(data[:len(data)/2])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		http.ServeContent(w, r, "", time.Time{}, strings.NewReader(string(data)))
	})
	return httptest.NewServer(mux)
}

func TestDownload(t *testing.T) {
	model := []byte(strings.Repeat("onnx", 100000))
	files := map[string][]byte{tagger.ModelFile: model, tagger.TagsFile: []byte("tag_id,name,category,count\n1,sky,0,100\n")}
	var cut atomic.Bool
	cut.Store(true)
	hf := fakeHF(t, files, &cut)
	defer hf.Close()
	restore := tagger.SetHFBase(hf.URL)
	defer restore()
	dir := t.TempDir()
	ctx := context.Background()

	// The first attempt breaks off; the second continues where it stopped.
	if err := tagger.Download(ctx, "org/model", dir, nil, nil); err == nil {
		t.Fatal("broken download succeeded")
	}
	part, err := os.Stat(filepath.Join(dir, tagger.ModelFile+".part"))
	if err != nil || part.Size() == 0 || part.Size() >= int64(len(model)) {
		t.Fatalf("partial file: %v %v", part, err)
	}
	cut.Store(false)
	var reported int64
	if err := tagger.Download(ctx, "org/model", dir, nil, func(file string, done, total int64) { reported = done }); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, tagger.ModelFile))
	if string(got) != string(model) || reported != int64(len(model)) {
		t.Fatalf("model: %d bytes (reported %d), want %d", len(got), reported, len(model))
	}
	// Already there: nothing is fetched again. A damaged file is replaced.
	os.WriteFile(filepath.Join(dir, tagger.TagsFile), []byte("damaged"), 0o644)
	var out strings.Builder
	if err := tagger.Download(ctx, "org/model", dir, &out, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "model.onnx is already downloaded") || !strings.Contains(out.String(), "Downloading selected_tags.csv") {
		t.Fatalf("output: %s", out.String())
	}
	tags, _ := os.ReadFile(filepath.Join(dir, tagger.TagsFile))
	if string(tags) != string(files[tagger.TagsFile]) {
		t.Fatalf("tags file %q", tags)
	}
	if err := tagger.Download(ctx, "org/missing", dir, nil, nil); err == nil || !strings.Contains(err.Error(), "not in") {
		t.Fatalf("missing repository: %v", err)
	}
}
