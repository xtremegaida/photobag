package experiments_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/comfy"
	"photobag/internal/comfy/comfytest"
	"photobag/internal/experiments"
	"photobag/internal/library"
	"photobag/internal/query"
)

func openBag(t *testing.T) *bag.Bag {
	t.Helper()
	b, err := bag.Open(filepath.Join(t.TempDir(), "g.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func ptr[T any](v T) *T { return &v }

func count(t *testing.T, b *bag.Bag, sql string) int {
	t.Helper()
	var n int
	if err := b.R.QueryRow(sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWorkflows(t *testing.T) {
	b := openBag(t)
	ctx := context.Background()
	if _, err := experiments.SaveWorkflow(ctx, b, 0, experiments.WorkflowChange{Name: ptr("Bad"), JSON: ptr(`{"nodes": [], "links": []}`)}); err == nil ||
		!strings.Contains(err.Error(), "editor format") {
		t.Errorf("editor format accepted: %v", err)
	}
	d, err := experiments.SaveWorkflow(ctx, b, 0, experiments.WorkflowChange{Name: ptr("  SDXL   text to image "), JSON: ptr(comfytest.Workflow)})
	if err != nil {
		t.Fatal(err)
	}
	if d.Workflow.Name != "SDXL text to image" || d.Workflow.NodeCount != 10 || d.Workflow.Output != comfy.OutputWebsocket ||
		len(d.Version.Nodes) != 10 || !strings.HasPrefix(d.Version.JSON, "{\n  \"3\": {") {
		t.Errorf("saved %+v", d.Workflow)
	}
	if _, err := experiments.SaveWorkflow(ctx, b, 0, experiments.WorkflowChange{Name: ptr("sdxl TEXT to image"), JSON: ptr(comfytest.Workflow)}); !errors.Is(err, experiments.ErrConflict) {
		t.Errorf("duplicate name: %v", err)
	}
	// The same content twice is one version; a change makes a new one and
	// drops the unused old one.
	d2, err := experiments.SaveWorkflow(ctx, b, 0, experiments.WorkflowChange{Name: ptr("Copy"), JSON: ptr(comfytest.Workflow)})
	if err != nil || d2.Workflow.VersionID != d.Workflow.VersionID {
		t.Fatalf("copy: %v, versions %d %d", err, d2.Workflow.VersionID, d.Workflow.VersionID)
	}
	changed := strings.Replace(comfytest.Workflow, `"a cat"`, `"a dog"`, 1)
	d3, err := experiments.SaveWorkflow(ctx, b, d2.Workflow.ID, experiments.WorkflowChange{JSON: &changed, Notes: ptr("dogs")})
	if err != nil || d3.Workflow.VersionID == d.Workflow.VersionID || d3.Workflow.Notes != "dogs" {
		t.Fatalf("edit: %v %+v", err, d3)
	}
	if n := count(t, b, "SELECT count(*) FROM workflow_versions"); n != 2 {
		t.Errorf("%d versions", n)
	}
	if err := experiments.DeleteWorkflow(ctx, b, d3.Workflow.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, b, "SELECT count(*) FROM workflow_versions"); n != 1 {
		t.Errorf("%d versions after deleting", n)
	}
	ws, _ := experiments.ListWorkflows(ctx, b)
	if len(ws) != 1 {
		t.Errorf("%d workflows", len(ws))
	}
}

func TestGenerate(t *testing.T) {
	b := openBag(t)
	ctx := context.Background()
	srv := comfytest.New()
	defer srv.Close()
	wf, err := experiments.SaveWorkflow(ctx, b, 0, experiments.WorkflowChange{Name: ptr("T2I"), JSON: ptr(comfytest.Workflow)})
	if err != nil {
		t.Fatal(err)
	}
	exp, err := experiments.SaveExperiment(ctx, b, 0, experiments.ExperimentChange{Name: ptr("Cats / dogs?")})
	if err != nil {
		t.Fatal(err)
	}
	req := experiments.Request{
		WorkflowID: wf.Workflow.ID,
		Overrides: []comfy.Override{
			{Node: "Positive", Input: "text", Value: "a cat on a sofa"},
			{Node: "KSampler", Input: "steps", Sweep: &comfy.Sweep{Range: &comfy.Range{From: 4, To: 5, Step: 1}}},
			{Node: "Checkpoint", Input: "ckpt_name", Sweep: &comfy.Sweep{Values: []any{"sdxl_base.safetensors", "nope.safetensors"}}},
		},
		Count: 2,
	}
	sum, err := experiments.Plan(ctx, b, req)
	if err != nil || sum.Error != "" || sum.Prompts != 8 || sum.Combos != 4 || len(sum.Dims) != 2 || sum.SeedInputs[0] != "KSampler › seed" {
		t.Fatalf("plan %+v %v", sum, err)
	}
	if s, _ := experiments.Plan(ctx, b, experiments.Request{WorkflowID: 999}); s.Error == "" {
		t.Error("plan for a missing workflow")
	}

	var last experiments.Progress
	images := 0
	res, err := experiments.Generate(ctx, b, exp.ID, req, experiments.GenerateOptions{
		Client:  comfy.New(srv.URL),
		Rand:    rand.New(rand.NewPCG(3, 4)),
		OnImage: func() { images++ },
	}, func(p experiments.Progress) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	// The missing model fails its 4 prompts, but not before the good ones
	// ran (models vary slowest).
	if res.Images != 4 || res.Failed != 4 || images != 4 || last.Done != 8 || last.Images != 4 {
		t.Fatalf("result %+v, progress %+v", res, last)
	}
	runs, err := experiments.ListRuns(ctx, b, exp.ID)
	if err != nil || len(runs) != 1 {
		t.Fatal(err, runs)
	}
	run := runs[0]
	if run.Status != experiments.RunDone || run.Images != 4 || run.Failed != 4 || len(run.Failures) != 4 ||
		!strings.Contains(run.Failures[0].Error, "Value not in list") || run.Request.VersionID != wf.Workflow.VersionID || len(run.Dims) != 2 {
		t.Errorf("run %+v", run)
	}

	gens, err := experiments.ListGenerations(ctx, b, exp.ID, false)
	if err != nil || len(gens) != 4 {
		t.Fatal(err, len(gens))
	}
	g := gens[0]
	if g.Format != "png" || g.Width != 64 || g.Height != 48 || g.ThumbW == 0 || g.Seed == nil || g.WorkflowName != "T2I" ||
		g.ExperimentName != "Cats / dogs?" || g.ImageID != 0 {
		t.Errorf("generation %+v", g)
	}
	kinds := map[string]int{}
	for _, a := range g.Applied {
		kinds[a.Kind]++
	}
	if kinds[""] != 1 || kinds[comfy.KindSweep] != 2 || kinds[comfy.KindSeed] != 1 {
		t.Errorf("applied %+v", g.Applied)
	}
	// Same seed for repeat 0 of both step counts.
	if *gens[0].Seed != *gens[1].Seed || *gens[0].Seed == *gens[2].Seed {
		t.Errorf("seeds %d %d %d", *gens[0].Seed, *gens[1].Seed, *gens[2].Seed)
	}
	// Held images stay out of the library.
	if st, _ := library.GetStats(ctx, b); st.Images != 0 {
		t.Errorf("%d library images", st.Images)
	}
	e, _ := experiments.GetExperiment(ctx, b, exp.ID)
	if e.Held != 4 || e.Runs != 1 || len(e.Covers) != 4 || len(e.Last.Overrides) != 3 {
		t.Errorf("experiment %+v", e)
	}

	// The effective workflow reproduces the image, and the PNG carries it.
	eff, _, err := experiments.Effective(ctx, b, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := eff.Value("KSampler", "seed"); v == nil || !strings.Contains(string(eff.Compact()), "a cat on a sofa") {
		t.Errorf("effective %s", eff.Compact())
	}
	blobID, _, err := experiments.Blob(ctx, b, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := library.BlobData(ctx, b, blobID)
	if p := comfy.PromptFromPNG(data); !strings.Contains(string(p), "a cat on a sofa") {
		t.Errorf("PNG prompt %q", p)
	}
	saved, err := experiments.SaveEffective(ctx, b, g.ID, "Best cat")
	if err != nil || saved.Version.ID == wf.Workflow.VersionID {
		t.Fatalf("save effective: %v", err)
	}

	// Emptying the trash leaves held images alone.
	if _, err := library.EmptyTrash(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, b, "SELECT count(*) FROM blobs"); n != 4 {
		t.Errorf("%d blobs after emptying the trash", n)
	}

	// Move two with tags; discard one; delete the experiment.
	mv, err := experiments.Move(ctx, b, []int64{gens[0].ID, gens[1].ID}, []string{"comfy", "cats"})
	if err != nil || mv.Moved != 2 {
		t.Fatal(err, mv)
	}
	im, err := library.GetImage(ctx, b, mv.ImageIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(im.Name, "Cats dogs ") || !strings.HasSuffix(im.Name, ".png") || im.TakenAt == "" ||
		im.OriginalPath != "ComfyUI/Cats dogs/"+im.Name || strings.Join(im.Tags, ",") != "cats,comfy" {
		t.Errorf("moved image %+v", im)
	}
	if again, _ := experiments.Move(ctx, b, []int64{gens[0].ID}, nil); again.Moved != 0 {
		t.Error("moved twice")
	}
	fi, err := experiments.ForImage(ctx, b, mv.ImageIDs[0])
	if err != nil || fi == nil || fi.ID != gens[0].ID || fi.ImageID != mv.ImageIDs[0] {
		t.Errorf("for image %+v %v", fi, err)
	}
	if _, _, err := experiments.Blob(ctx, b, gens[0].ID); err != nil {
		t.Errorf("moved blob: %v", err)
	}
	ids, _ := library.ListIDs(ctx, b, query.ImageQuery{TagsAll: []string{"cats"}}, query.Sort{})
	if len(ids) != 2 {
		t.Errorf("%d tagged images", len(ids))
	}

	n, err := experiments.Discard(ctx, b, []int64{gens[2].ID, gens[0].ID})
	if err != nil || n != 1 {
		t.Fatalf("discard %d %v", n, err)
	}
	if n := count(t, b, "SELECT count(*) FROM blobs"); n != 3 {
		t.Errorf("%d blobs after discarding", n)
	}
	if err := experiments.DeleteExperiment(ctx, b, exp.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, b, "SELECT count(*) FROM blobs"); n != 2 {
		t.Errorf("%d blobs after deleting the experiment", n)
	}
	if fi, _ := experiments.ForImage(ctx, b, mv.ImageIDs[1]); fi == nil || fi.ExperimentID != 0 {
		t.Errorf("moved image lost its generation: %+v", fi)
	}
	if n := count(t, b, "SELECT count(*) FROM generation_runs"); n != 0 {
		t.Errorf("%d runs left", n)
	}
	// Its version is still there for the moved images.
	if _, err := experiments.GetVersion(ctx, b, fi.VersionID); err != nil {
		t.Error(err)
	}
}

func TestGenerateCancelled(t *testing.T) {
	b := openBag(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := comfytest.New()
	defer srv.Close()
	srv.StepDelay = 20_000_000 // 20 ms
	wf, _ := experiments.SaveWorkflow(ctx, b, 0, experiments.WorkflowChange{Name: ptr("T2I"), JSON: ptr(comfytest.Workflow)})
	exp, _ := experiments.SaveExperiment(ctx, b, 0, experiments.ExperimentChange{Name: ptr("E")})
	_, err := experiments.Generate(ctx, b, exp.ID, experiments.Request{WorkflowID: wf.Workflow.ID, Count: 5},
		experiments.GenerateOptions{Client: comfy.New(srv.URL), OnImage: cancel}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	runs, _ := experiments.ListRuns(context.Background(), b, exp.ID)
	if runs[0].Status != experiments.RunCancelled || runs[0].Images != 1 {
		t.Errorf("run %+v", runs[0])
	}
}
