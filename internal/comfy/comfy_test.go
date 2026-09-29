package comfy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"photobag/internal/comfy"
	"photobag/internal/comfy/comfytest"
)

func parse(t *testing.T, s string) *comfy.Workflow {
	t.Helper()
	w, err := comfy.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestParse(t *testing.T) {
	w := parse(t, comfytest.Workflow)
	nodes := w.Nodes()
	var ids []string
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	if got := strings.Join(ids, ","); got != "3,4,6,7,8,27,28,29,30,31" {
		t.Errorf("node order %s", got)
	}
	ks := nodes[0]
	if ks.Title != "KSampler" || ks.Ref != "KSampler" || ks.ClassType != "KSampler" {
		t.Errorf("KSampler node %+v", ks)
	}
	var names []string
	for _, in := range ks.Inputs {
		names = append(names, in.Name)
		if in.Name == "seed" && !in.Seed {
			t.Error("seed not recognised")
		}
	}
	// Links (model, positive...) are not overridable; the key order is kept.
	if got := strings.Join(names, ","); got != "seed,steps,cfg,sampler_name,scheduler,denoise" {
		t.Errorf("inputs %s", got)
	}
	if kind, outs := w.Outputs(); kind != comfy.OutputWebsocket || len(outs) != 1 || outs[0] != "29" {
		t.Errorf("outputs %s %v", kind, outs)
	}
	if len(w.Problems()) != 0 {
		t.Errorf("problems %v", w.Problems())
	}

	// Canonical, and stable across a round trip.
	again := parse(t, string(w.JSON()))
	if !bytes.Equal(again.JSON(), w.JSON()) {
		t.Error("JSON is not stable")
	}
	if !strings.HasPrefix(string(w.JSON()), "{\n  \"3\": {\n    \"inputs\": {\n      \"seed\": 1234,") {
		t.Errorf("JSON layout:\n%s", w.JSON()[:80])
	}

	for _, tc := range []struct{ in, want string }{
		{`{"last_node_id": 9, "nodes": [], "links": []}`, "editor format"},
		{`[1, 2]`, "JSON object of nodes"},
		{`{"3": {"inputs": {}}}`, "no class_type"},
		{`{"3": `, "not valid JSON"},
		{``, "empty"},
		{`{}`, "no nodes"},
	} {
		if _, err := comfy.Parse([]byte(tc.in)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%s) = %v, want %q", tc.in, err, tc.want)
		}
	}
	// A /prompt request body works too.
	if _, err := comfy.Parse([]byte(`{"prompt": ` + comfytest.Workflow + `, "client_id": "x"}`)); err != nil {
		t.Error(err)
	}
	// Save Image instead of the websocket node.
	if kind, _ := parse(t, comfytest.SaveImageWorkflow).Outputs(); kind != comfy.OutputFile {
		t.Errorf("save image kind %q", kind)
	}
}

func TestFindAndSet(t *testing.T) {
	w := parse(t, `{
		"1": {"inputs": {"text": "a", "seed": 5}, "class_type": "CLIPTextEncode", "_meta": {"title": "Prompt"}},
		"2": {"inputs": {"text": "b"}, "class_type": "CLIPTextEncode", "_meta": {"title": "Prompt"}},
		"3": {"inputs": {"value": 1024, "flag": true, "link": ["1", 0]}, "class_type": "PrimitiveInt", "_meta": {"title": "Width"}},
		"4": {"inputs": {}, "class_type": "SaveImage"}
	}`)
	if _, err := w.Find("Prompt"); err == nil || !strings.Contains(err.Error(), "several nodes") {
		t.Errorf("duplicate title: %v", err)
	}
	if id, err := w.Find("#2"); err != nil || id != "2" {
		t.Errorf("#2 → %s %v", id, err)
	}
	if id, err := w.Find("width"); err != nil || id != "3" {
		t.Errorf("case-insensitive → %s %v", id, err)
	}
	if id, err := w.Find("SaveImage"); err != nil || id != "4" {
		t.Errorf("untitled node by class → %s %v", id, err)
	}
	refs := map[string]string{}
	for _, n := range w.Nodes() {
		refs[n.ID] = n.Ref
	}
	if refs["1"] != "#1" || refs["2"] != "#2" || refs["3"] != "Width" {
		t.Errorf("refs %v", refs)
	}
	if p := w.Problems(); len(p) != 1 || !strings.Contains(p[0], "“Prompt” (#1, #2)") {
		t.Errorf("problems %v", p)
	}

	c, err := w.Apply([]comfy.Applied{
		{Node: "Width", Input: "value", Value: "2048"}, // text → number
		{Node: "#2", Input: "text", Value: 3.5},        // number → text
		{Node: "Width", Input: "flag", Value: "false"},
		{Node: "#1", Input: "text", Value: "<lora:x> & more"},
	})
	if err != nil {
		t.Fatal(err)
	}
	js := string(c.Compact())
	for _, want := range []string{`"value":2048`, `"text":"3.5"`, `"flag":false`, `"text":"<lora:x> & more"`} {
		if !strings.Contains(js, want) {
			t.Errorf("%s missing from %s", want, js)
		}
	}
	if strings.Contains(string(w.Compact()), "2048") {
		t.Error("Apply changed the original")
	}
	for _, a := range []comfy.Applied{
		{Node: "Width", Input: "link", Value: 1},
		{Node: "Width", Input: "nope", Value: 1},
		{Node: "Width", Input: "value", Value: "wide"},
		{Node: "Width", Input: "flag", Value: 3.0},
		{Node: "Height", Input: "value", Value: 1},
	} {
		if _, err := w.Apply([]comfy.Applied{a}); err == nil {
			t.Errorf("%+v applied", a)
		}
	}
}

func values(p *comfy.Plan, input string) []any {
	var out []any
	for _, it := range p.Items {
		for _, a := range it.Applied {
			if a.Input == input {
				out = append(out, a.Value)
			}
		}
	}
	return out
}

func TestExpand(t *testing.T) {
	w := parse(t, comfytest.Workflow)
	rng := rand.New(rand.NewPCG(1, 2))

	// N images with random seeds.
	p, err := comfy.Expand(w, []comfy.Override{{Node: "Width", Input: "value", Value: 1024.0}}, 3, "", rng)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 3 || p.Combos != 1 || len(p.SeedInputs) != 1 || p.SeedInputs[0] != "KSampler › seed" {
		t.Fatalf("plan %+v", p)
	}
	seeds := values(p, "seed")
	if seeds[0] == seeds[1] || seeds[1] == seeds[2] || *p.Items[0].Seed != seeds[0].(int64) {
		t.Errorf("seeds %v", seeds)
	}
	for _, s := range seeds {
		if s.(int64) < 0 || s.(int64) >= comfy.MaxRandomSeed {
			t.Errorf("seed %v out of range", s)
		}
	}

	// A sweep: 2 models × 3 step counts × 2 repeats. Models vary slowest,
	// and each repeat shares its seed across combinations.
	p, err = comfy.Expand(w, []comfy.Override{
		{Node: "KSampler", Input: "steps", Sweep: &comfy.Sweep{Range: &comfy.Range{From: 25, To: 27, Step: 1}}},
		{Node: "Checkpoint", Input: "ckpt_name", Sweep: &comfy.Sweep{Values: []any{"a.safetensors", "b.safetensors"}}},
	}, 2, comfy.SeedRandom, rng)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 12 || p.Combos != 6 || p.Dims[0].Input != "ckpt_name" {
		t.Fatalf("sweep plan: %d items, %d combos, dims %+v", len(p.Items), p.Combos, p.Dims)
	}
	models := values(p, "ckpt_name")
	if models[0] != "a.safetensors" || models[5] != "a.safetensors" || models[6] != "b.safetensors" {
		t.Errorf("models %v", models)
	}
	if got := values(p, "steps"); got[0] != 25.0 || got[1] != 26.0 || got[2] != 27.0 || got[3] != 25.0 {
		t.Errorf("steps %v", got)
	}
	seeds = values(p, "seed")
	if seeds[0] != seeds[1] || seeds[0] != seeds[6] || seeds[0] == seeds[3] || p.Items[3].Repeat != 1 {
		t.Errorf("seeds are not shared per repeat: %v", seeds)
	}
	if p.Items[7].Combo != 4 {
		t.Errorf("combo index %d", p.Items[7].Combo)
	}

	// Fixed and incrementing seeds; an explicit seed leaves the policy out.
	p, _ = comfy.Expand(w, nil, 3, comfy.SeedIncrement, rng)
	if got := values(p, "seed"); got[0] != int64(1234) || got[2] != int64(1236) {
		t.Errorf("increment %v", got)
	}
	p, _ = comfy.Expand(w, nil, 2, comfy.SeedFixed, rng)
	if len(values(p, "seed")) != 0 || len(p.Warnings) != 1 {
		t.Errorf("fixed: %v %v", values(p, "seed"), p.Warnings)
	}
	p, _ = comfy.Expand(w, []comfy.Override{{Node: "KSampler", Input: "seed", Sweep: &comfy.Sweep{Values: []any{1.0, 2.0}}}}, 1, "", rng)
	if got := values(p, "seed"); len(got) != 2 || got[0] != 1.0 || p.Items[1].Seed == nil || *p.Items[1].Seed != 2 {
		t.Errorf("swept seed %v", got)
	}

	// Decimal ranges come out clean.
	p, _ = comfy.Expand(w, []comfy.Override{{Node: "KSampler", Input: "cfg", Sweep: &comfy.Sweep{Range: &comfy.Range{From: 0.1, To: 0.5, Step: 0.1}}}}, 1, "", rng)
	if got := values(p, "cfg"); len(got) != 5 || got[2] != 0.3 || got[4] != 0.5 {
		t.Errorf("cfg %v", got)
	}
	p, _ = comfy.Expand(w, []comfy.Override{{Node: "KSampler", Input: "steps", Sweep: &comfy.Sweep{Range: &comfy.Range{From: 30, To: 20, Step: 5}}}}, 1, "", rng)
	if got := values(p, "steps"); len(got) != 3 || got[0] != 30.0 || got[2] != 20.0 {
		t.Errorf("descending %v", got)
	}

	for _, tc := range []struct {
		o    []comfy.Override
		n    int
		want string
	}{
		{[]comfy.Override{{Node: "KSampler", Input: "steps", Value: 1.0}, {Node: "KSampler", Input: "steps", Value: 2.0}}, 1, "twice"},
		{[]comfy.Override{{Node: "KSampler", Input: "steps", Sweep: &comfy.Sweep{Range: &comfy.Range{From: 1, To: 2}}}}, 1, "step"},
		{[]comfy.Override{{Node: "KSampler", Input: "steps", Sweep: &comfy.Sweep{}}}, 1, "no values"},
		{[]comfy.Override{{Node: "KSampler", Input: "steps", Value: "many"}}, 1, "not a number"},
		{[]comfy.Override{{Node: "KSampler", Input: "model", Value: 1.0}}, 1, "connected to another node"},
		{[]comfy.Override{{Node: "KSampler", Input: "steps", Sweep: &comfy.Sweep{Range: &comfy.Range{From: 1, To: 100, Step: 1}}}}, 30, "at most 2000"},
		{nil, comfy.MaxCount + 1, "per combination"},
	} {
		if _, err := comfy.Expand(w, tc.o, tc.n, "", rng); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc.o, err, tc.want)
		}
	}
}

func TestEmbedPrompt(t *testing.T) {
	w := parse(t, `{"1": {"inputs": {"text": "café ☕ 𝄞"}, "class_type": "SaveImage"}}`)
	var buf bytes.Buffer
	png.Encode(&buf, testImage())
	out := comfy.EmbedPrompt(buf.Bytes(), w)
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("not a valid PNG any more: %v", err)
	}
	got := comfy.PromptFromPNG(out)
	for _, c := range got {
		if c >= 0x80 {
			t.Fatalf("non-ASCII in the chunk: %s", got)
		}
	}
	var back map[string]struct {
		Inputs map[string]string `json:"inputs"`
	}
	if err := json.Unmarshal(got, &back); err != nil || back["1"].Inputs["text"] != "café ☕ 𝄞" {
		t.Errorf("prompt %s → %v %v", got, back, err)
	}
	if again := comfy.EmbedPrompt(out, w); !bytes.Equal(again, out) {
		t.Error("embedded twice")
	}
	if jpg := []byte{0xff, 0xd8, 0xff}; !bytes.Equal(comfy.EmbedPrompt(jpg, w), jpg) {
		t.Error("changed a non-PNG")
	}
}

// recorder collects run events.
type recorder struct {
	mu       sync.Mutex
	started  []int
	images   map[int][]comfy.Output
	errs     map[int]error
	done     []int
	previews int
	steps    int
}

func newRecorder() *recorder {
	return &recorder{images: map[int][]comfy.Output{}, errs: map[int]error{}}
}

func (r *recorder) events() comfy.Events {
	return comfy.Events{
		Started:  func(i int) { r.mu.Lock(); r.started = append(r.started, i); r.mu.Unlock() },
		Progress: func(i int, node string, v, m int) { r.mu.Lock(); r.steps++; r.mu.Unlock() },
		Preview:  func(i int, data []byte, f string) { r.mu.Lock(); r.previews++; r.mu.Unlock() },
		Image: func(i int, o comfy.Output) error {
			r.mu.Lock()
			r.images[i] = append(r.images[i], o)
			r.mu.Unlock()
			return nil
		},
		Finished: func(i int, ms int64, err error) {
			r.mu.Lock()
			r.done = append(r.done, i)
			if err != nil {
				r.errs[i] = err
			}
			r.mu.Unlock()
		},
	}
}

func prompts(t *testing.T, w *comfy.Workflow, sets ...[]comfy.Applied) []*comfy.Workflow {
	var out []*comfy.Workflow
	for _, s := range sets {
		p, err := w.Apply(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func seed(n float64) []comfy.Applied {
	return []comfy.Applied{{Node: "KSampler", Input: "seed", Value: n}}
}

func TestRunner(t *testing.T) {
	srv := comfytest.New()
	defer srv.Close()
	client := comfy.New(srv.URL)
	ctx := context.Background()

	info, err := client.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "0.99.0-fake" || !info.WebsocketNode || len(info.Devices) != 1 || info.Devices[0].Name != "cuda:0 Fake GPU" {
		t.Errorf("info %+v", info)
	}
	ni, err := client.NodeInfo(ctx, "KSampler")
	if err != nil {
		t.Fatal(err)
	}
	specs := map[string]comfy.InputSpec{}
	for _, s := range ni.Inputs {
		specs[s.Name] = s
	}
	if !specs["seed"].Seed || specs["sampler_name"].Type != "COMBO" || len(specs["sampler_name"].Options) != 3 ||
		specs["cfg"].Type != "FLOAT" || *specs["cfg"].Step != 0.1 || ni.Inputs[0].Name != "model" {
		t.Errorf("KSampler specs %+v", ni.Inputs)
	}
	if ck, err := client.NodeInfo(ctx, "CheckpointLoaderSimple"); err != nil || len(ck.Inputs[0].Options) != 2 {
		t.Errorf("checkpoint options %+v %v", ck, err)
	}
	if _, err := client.NodeInfo(ctx, "Nope"); !errors.Is(err, comfy.ErrUnknownNode) {
		t.Errorf("unknown node: %v", err)
	}

	w := parse(t, comfytest.Workflow)
	batch, _ := w.Apply([]comfy.Applied{{Node: "Batches", Input: "value", Value: 2.0}, {Node: "KSampler", Input: "seed", Value: 7.0}})
	ps := append(prompts(t, w, seed(1), seed(2),
		[]comfy.Applied{{Node: "Checkpoint", Input: "ckpt_name", Value: "missing.safetensors"}}), batch)
	rec := newRecorder()
	r := &comfy.Runner{Client: client}
	if err := r.Run(ctx, ps, rec.events()); err != nil {
		t.Fatal(err)
	}
	if len(rec.done) != 4 || len(rec.images[0]) != 1 || len(rec.images[1]) != 1 || len(rec.images[3]) != 2 {
		t.Fatalf("done %v, images %v, errs %v", rec.done, rec.images, rec.errs)
	}
	if e := rec.errs[2]; e == nil || !strings.Contains(e.Error(), "Checkpoint: Value not in list") {
		t.Errorf("validation error: %v", e)
	}
	img := rec.images[0][0]
	if img.Format != "png" || img.Node != "29" || rec.images[3][1].Index != 1 {
		t.Errorf("output %s %s %d", img.Format, img.Node, rec.images[3][1].Index)
	}
	if cfg, err := png.DecodeConfig(bytes.NewReader(img.Data)); err != nil || cfg.Width != 64 || cfg.Height != 48 {
		t.Errorf("image %+v %v", cfg, err)
	}
	if bytes.Equal(rec.images[0][0].Data, rec.images[1][0].Data) {
		t.Error("different seeds made the same image")
	}
	// Labelled previews (10 steps → 2) are not taken for outputs.
	if rec.previews != 6 || rec.steps != 30 {
		t.Errorf("previews %d, steps %d", rec.previews, rec.steps)
	}
	got := srv.Received()
	if len(got) != 3 || got[0].ClientID == "" || got[0].ClientID != got[2].ClientID {
		t.Errorf("received %d prompts", len(got))
	}

	// Running the same prompt again: ComfyUI serves it from its cache and
	// sends nothing.
	rec = newRecorder()
	if err := r.Run(ctx, []*comfy.Workflow{batch}, rec.events()); err != nil {
		t.Fatal(err)
	}
	if e := rec.errs[0]; !errors.Is(e, comfy.ErrNoImages) || !strings.Contains(e.Error(), "cached") {
		t.Errorf("cached prompt: %v", e)
	}

	// Save Image outputs are downloaded.
	rec = newRecorder()
	sw := parse(t, comfytest.SaveImageWorkflow)
	if err := r.Run(ctx, prompts(t, sw, seed(11)), rec.events()); err != nil {
		t.Fatal(err)
	}
	if len(rec.images[0]) != 1 || rec.images[0][0].Format != "png" || rec.errs[0] != nil {
		t.Errorf("save image: %v %v", rec.images, rec.errs)
	}

	// A node failing at run time fails that prompt; three failures in a
	// row at the start end the run.
	srv.Fail = "VAEDecode"
	rec = newRecorder()
	err = r.Run(ctx, prompts(t, w, seed(21), seed(22), seed(23), seed(24)), rec.events())
	if err == nil || !strings.Contains(err.Error(), "3 failed prompts") || !strings.Contains(err.Error(), "out of memory") {
		t.Errorf("failing run: %v", err)
	}
	srv.Fail = ""
	time.Sleep(100 * time.Millisecond)
}

func TestRunnerCancel(t *testing.T) {
	srv := comfytest.New()
	defer srv.Close()
	srv.StepDelay = 20 * time.Millisecond
	w := parse(t, comfytest.Workflow)
	ctx, cancel := context.WithCancel(context.Background())
	rec := newRecorder()
	ev := rec.events()
	ev.Started = func(i int) {
		if i == 1 {
			cancel()
		}
	}
	r := &comfy.Runner{Client: comfy.New(srv.URL)}
	err := r.Run(ctx, prompts(t, w, seed(1), seed(2), seed(3), seed(4)), ev)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	// The running prompt is interrupted and the waiting one removed.
	deadline := time.Now().Add(3 * time.Second)
	for {
		q, err := r.Client.Queue(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(q.Running) == 0 && len(q.Pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue not cleared: %+v", q)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(srv.Received()); n != 3 {
		t.Errorf("%d prompts were queued (window 2 + 1)", n)
	}
	if len(rec.images[0]) != 1 || len(rec.images[1]) != 0 {
		t.Errorf("images %v", rec.images)
	}
}

func TestRunnerReconnect(t *testing.T) {
	srv := comfytest.New()
	defer srv.Close()
	srv.StepDelay = 30 * time.Millisecond
	w := parse(t, comfytest.Workflow)
	rec := newRecorder()
	ev := rec.events()
	dropped := false
	ev.Progress = func(i int, node string, v, m int) {
		if i == 0 && v == 3 && !dropped {
			dropped = true
			go srv.DropConnections()
		}
	}
	r := &comfy.Runner{Client: comfy.New(srv.URL), Poll: 600 * time.Millisecond}
	if err := r.Run(context.Background(), prompts(t, w, seed(1), seed(2), seed(3), seed(4)), ev); err != nil {
		t.Fatal(err)
	}
	// Prompts that finished while disconnected lost their images (and say
	// so); the run carries on after reconnecting.
	if len(rec.done) != 4 || len(rec.images[3]) != 1 {
		t.Errorf("after reconnecting: done %v, images %v, errs %v", rec.done, rec.images, rec.errs)
	}
	for i := range 4 {
		if len(rec.images[i]) == 0 && (rec.errs[i] == nil || !strings.Contains(rec.errs[i].Error(), "connection")) {
			t.Errorf("prompt %d: lost image not reported: %v", i, rec.errs[i])
		}
	}
}

func testImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	return img
}
