package tagger

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeInstall makes dir look like an installation whose "python" is this
// test binary (see fakeServer).
func fakeInstall(t *testing.T, device string) string {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	py := VenvPython(dir)
	os.MkdirAll(filepath.Dir(py), 0o755)
	if err := os.WriteFile(py, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{ScriptFile: script, ModelFile: []byte("onnx"), TagsFile: []byte("tag_id,name,category\n")} {
		os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}
	if err := writeInfo(dir, installInfo{Model: "org/wd-test-tagger-v3", Device: device, Python: "3.12.0", Script: sha(script)}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	if in := Detect(dir); in.Installed || in.Ready || !strings.Contains(in.Problem, "photobag tagger install") {
		t.Fatalf("empty dir: %+v", in)
	}
	dir = fakeInstall(t, "cpu")
	os.Remove(filepath.Join(dir, ModelFile))
	in := Detect(dir)
	if !in.Installed || in.ModelReady || !strings.Contains(in.Problem, "--download-model") || !strings.Contains(in.Problem, "huggingface.co/org/wd-test-tagger-v3") {
		t.Fatalf("no model: %+v", in)
	}
	os.WriteFile(filepath.Join(dir, ModelFile), []byte("onnx"), 0o644)
	if in := Detect(dir); !in.Ready || in.ModelName() != "wd-test-tagger-v3" || in.Device != "cpu" || in.Problem != "" {
		t.Fatalf("ready: %+v", in)
	}
}

func TestRequirements(t *testing.T) {
	cases := map[string]string{
		requirementsFor("cpu", false):  "onnxruntime>=1.17\n",
		requirementsFor("cuda", true):  "onnxruntime-gpu[cuda,cudnn]>=1.21\n",
		requirementsFor("cuda", false): "onnxruntime-gpu>=1.17\n",
	}
	for got, first := range cases {
		if !strings.HasPrefix(got, first) || !strings.Contains(got, "fastapi\n") || strings.Count(got, "onnxruntime") != 1 {
			t.Errorf("requirements:\n%s", got)
		}
	}
}

func TestUpdateScript(t *testing.T) {
	dir := fakeInstall(t, "cpu")
	path := filepath.Join(dir, ScriptFile)
	// An older script PhotoBag installed is replaced...
	old := []byte("# old version\n")
	os.WriteFile(path, old, 0o644)
	info, _ := readInfo(dir)
	info.Script = sha(old)
	writeInfo(dir, info)
	if err := updateScript(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(script) {
		t.Fatal("the old script was not updated")
	}
	// ...but one edited by hand is kept.
	os.WriteFile(path, []byte("# my changes\n"), 0o644)
	updateScript(dir)
	if got, _ := os.ReadFile(path); string(got) != "# my changes\n" {
		t.Fatal("an edited script was replaced")
	}
}

func TestLocal(t *testing.T) {
	dir := fakeInstall(t, "cuda")
	t.Setenv(fakeEnv, "slow")
	var changes atomic.Int32
	l := &Local{Dir: dir, OnChange: func() { changes.Add(1) }}
	defer l.Close()
	ctx := context.Background()
	if st := l.Status(); st.State != "stopped" || !st.Installation.Ready {
		t.Fatalf("status before: %+v", st)
	}

	// Requests while it starts wait for one server.
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := l.Tag(ctx, []byte("jpeg"), Options{GeneralThreshold: 0.35})
			if err == nil && (len(r.General) != 1 || r.Rating.Tag != "general") {
				t.Errorf("result %+v", r)
			}
			errs[i] = err
		}()
	}
	time.Sleep(300 * time.Millisecond)
	if st := l.Status(); st.State != "starting" {
		t.Errorf("status while starting: %s", st.State)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	st := l.Status()
	if st.State != "running" || st.Port == 0 || !st.OnGPU || l.Name() != "wd-test-tagger-v3" {
		t.Fatalf("status: %+v", st)
	}
	h, err := l.Health(ctx)
	if err != nil || h.Name != "wd-test-tagger-v3" {
		t.Fatalf("health %+v %v", h, err)
	}
	if log, _ := os.ReadFile(filepath.Join(dir, logFile)); !strings.Contains(string(log), "Uvicorn running") {
		t.Fatalf("log: %q", log)
	}

	// A crash is noticed and the next request starts it again.
	port := st.Port
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/crash")
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	waitFor(t, func() bool { return l.Status().State == "stopped" })
	if st := l.Status(); !strings.Contains(st.LastError, "stopped unexpectedly") || !strings.Contains(st.LastError, "Segmentation fault") {
		t.Fatalf("after crash: %+v", st)
	}
	t.Setenv(fakeEnv, "serve")
	if _, err := l.Tag(ctx, []byte("jpeg"), Options{}); err != nil {
		t.Fatal(err)
	}
	if st := l.Status(); st.State != "running" || st.Port == port || st.LastError != "" {
		t.Fatalf("restarted: %+v", st)
	}

	// Stop, then idle shutdown.
	l.Stop()
	if st := l.Status(); st.State != "stopped" {
		t.Fatalf("after Stop: %+v", st)
	}
	l.IdleTimeout = 200 * time.Millisecond
	if _, err := l.Tag(ctx, []byte("jpeg"), Options{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return l.Status().State == "stopped" })
	if changes.Load() < 6 {
		t.Errorf("only %d change notifications", changes.Load())
	}
}

func TestLocalStartFailure(t *testing.T) {
	dir := fakeInstall(t, "cuda")
	t.Setenv(fakeEnv, "fail")
	l := &Local{Dir: dir}
	defer l.Close()
	_, err := l.Tag(context.Background(), []byte("jpeg"), Options{})
	if err == nil || !strings.Contains(err.Error(), "CUDAExecutionProvider is not available") {
		t.Fatalf("error %v", err)
	}
	// The failure is reported again at once rather than retried per image.
	start := time.Now()
	_, err2 := l.Tag(context.Background(), []byte("jpeg"), Options{})
	if err2 == nil || err2.Error() != err.Error() || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("second attempt: %v after %s", err2, time.Since(start))
	}
	if st := l.Status(); st.State != "stopped" || st.LastError == "" {
		t.Fatalf("status %+v", st)
	}
	os.Remove(filepath.Join(dir, ModelFile))
	l2 := &Local{Dir: dir}
	if _, err := l2.Tag(context.Background(), nil, Options{}); err == nil || !strings.Contains(err.Error(), "not downloaded") {
		t.Fatalf("no model: %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
