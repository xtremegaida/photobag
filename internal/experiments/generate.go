package experiments

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"photobag/internal/bag"
	"photobag/internal/comfy"
	"photobag/internal/imaging"
	"photobag/internal/library"
)

// resolved is what a request runs.
type resolved struct {
	workflow   *comfy.Workflow
	versionID  int64
	workflowID int64 // 0 when the template is gone
	name       string
}

func resolve(ctx context.Context, b *bag.Bag, req Request) (*resolved, error) {
	r := &resolved{}
	if req.WorkflowID != 0 {
		err := b.R.QueryRowContext(ctx, "SELECT id, name, version_id FROM workflows WHERE id = ?", req.WorkflowID).
			Scan(&r.workflowID, &r.name, &r.versionID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if errors.Is(err, sql.ErrNoRows) && req.VersionID == 0 {
			return nil, invalidf("that workflow has been deleted: choose another")
		}
	}
	if req.VersionID != 0 {
		r.versionID = req.VersionID
	}
	if r.versionID == 0 {
		return nil, invalidf("choose a workflow")
	}
	if r.name == "" {
		_ = b.R.QueryRowContext(ctx, "SELECT workflow_name FROM generations WHERE version_id = ? ORDER BY id DESC LIMIT 1", r.versionID).Scan(&r.name)
		if r.name == "" {
			r.name = "workflow"
		}
	}
	var err error
	if r.workflow, err = loadVersion(ctx, b, r.versionID); err != nil {
		return nil, err
	}
	return r, nil
}

// Plan checks a request and says what it would make. A request that
// cannot run is reported in the summary's Error.
func Plan(ctx context.Context, b *bag.Bag, req Request) (PlanSummary, error) {
	sum := PlanSummary{Dims: []comfy.Dim{}, SeedInputs: []string{}, Warnings: []string{}}
	r, err := resolve(ctx, b, req)
	if errors.Is(err, ErrNotFound) {
		sum.Error = "that workflow version no longer exists"
		return sum, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return sum, err
		}
		sum.Error = err.Error()
		return sum, nil
	}
	sum.VersionID = r.versionID
	p, err := comfy.Expand(r.workflow, req.Overrides, req.Count, req.Seed, nil)
	if err != nil {
		sum.Error = err.Error()
		return sum, nil
	}
	sum.Prompts, sum.Combos, sum.Count = len(p.Items), p.Combos, p.Count
	sum.Dims = p.Dims
	if p.SeedInputs != nil {
		sum.SeedInputs = p.SeedInputs
	}
	if p.Warnings != nil {
		sum.Warnings = p.Warnings
	}
	if kind, _ := r.workflow.Outputs(); kind == "" {
		sum.Error = comfy.ErrNoOutput.Error()
	}
	return sum, nil
}

// GenerateOptions configure a generation job.
type GenerateOptions struct {
	Client *comfy.Client
	JobID  string
	// OnImage is called after each image is stored.
	OnImage func()
	// OnPreview receives sampler previews.
	OnPreview func(data []byte, format string)
	Log       *slog.Logger
	// Rand makes seeds reproducible (tests).
	Rand *rand.Rand
}

// decodeError is an image ComfyUI sent that cannot be stored.
type decodeError struct{ msg string }

func (e decodeError) Error() string { return e.msg }

// Generate runs a request in an experiment, storing the images there as
// they arrive. It returns when all prompts are done or ctx is cancelled
// (the prompts still waiting are then removed from ComfyUI's queue).
func Generate(ctx context.Context, b *bag.Bag, experimentID int64, req Request, opts GenerateOptions, progress func(Progress)) (*Result, error) {
	if opts.Client == nil {
		return nil, invalidf("no ComfyUI address is set")
	}
	if _, err := GetExperiment(ctx, b, experimentID); err != nil {
		return nil, err
	}
	r, err := resolve(ctx, b, req)
	if err != nil {
		return nil, err
	}
	plan, err := comfy.Expand(r.workflow, req.Overrides, req.Count, req.Seed, opts.Rand)
	if err != nil {
		return nil, invalid{err}
	}
	prompts := make([]*comfy.Workflow, len(plan.Items))
	for i, it := range plan.Items {
		if prompts[i], err = r.workflow.Apply(it.Applied); err != nil {
			return nil, invalid{err}
		}
	}

	// Record the run and remember the settings for next time.
	pinned := req
	pinned.VersionID = r.versionID
	reqJS, _ := json.Marshal(pinned)
	dimsJS, _ := json.Marshal(plan.Dims)
	var runID int64
	now := bag.NowMillis()
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		var wid any
		if r.workflowID != 0 {
			wid = r.workflowID
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO generation_runs(experiment_id, version_id, workflow_id, workflow_name,
			request, prompts, dims, job_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			experimentID, r.versionID, wid, r.name, string(reqJS), len(prompts), string(dimsJS), opts.JobID, now)
		if err != nil {
			return err
		}
		runID, _ = res.LastInsertId()
		return saveRequest(ctx, tx, experimentID, req)
	})
	if err != nil {
		return nil, err
	}

	g := &generation{b: b, r: r, plan: plan, prompts: prompts, runID: runID, experimentID: experimentID,
		opts: opts, progress: progress, ids: map[int][]int64{}, storeErr: map[int]error{}, failures: []Failure{}}
	g.p = Progress{ExperimentID: experimentID, RunID: runID, Prompts: len(prompts)}
	g.report(true)
	runner := &comfy.Runner{Client: opts.Client, Log: opts.Log}
	runErr := runner.Run(ctx, prompts, comfy.Events{
		Started:  g.started,
		Progress: g.stepped,
		Preview:  g.preview,
		Image:    g.image,
		Finished: g.finished,
	})

	status, msg := RunDone, ""
	switch {
	case errors.Is(runErr, context.Canceled):
		status = RunCancelled
	case runErr != nil:
		status, msg = RunFailed, runErr.Error()
	}
	g.saveRun(status, msg)
	res := &Result{ExperimentID: experimentID, RunID: runID, Prompts: len(prompts), Images: g.p.Images, Failed: g.p.Failed}
	return res, runErr
}

type generation struct {
	b            *bag.Bag
	r            *resolved
	plan         *comfy.Plan
	prompts      []*comfy.Workflow
	runID        int64
	experimentID int64
	opts         GenerateOptions
	progress     func(Progress)

	p        Progress
	last     time.Time
	ids      map[int][]int64
	storeErr map[int]error
	failures []Failure
}

func (g *generation) report(force bool) {
	if g.progress == nil || (!force && time.Since(g.last) < 250*time.Millisecond) {
		return
	}
	g.last = time.Now()
	g.progress(g.p)
}

// swept returns a prompt's swept values.
func (g *generation) swept(i int) []comfy.Applied {
	var out []comfy.Applied
	for _, a := range g.plan.Items[i].Applied {
		if a.Kind == comfy.KindSweep {
			out = append(out, a)
		}
	}
	if out == nil {
		out = []comfy.Applied{}
	}
	return out
}

func (g *generation) label(i int) string {
	it := g.plan.Items[i]
	var parts []string
	if d := comfy.Describe(g.swept(i)); d != "" {
		parts = append(parts, d)
	}
	if g.plan.Count > 1 {
		parts = append(parts, fmt.Sprintf("image %d of %d", it.Repeat+1, g.plan.Count))
	}
	return strings.Join(parts, " · ")
}

func (g *generation) started(i int) {
	g.p.Current, g.p.Node, g.p.Step, g.p.Steps = g.label(i), "", 0, 0
	g.report(true)
}

func (g *generation) stepped(i int, node string, v, m int) {
	g.p.Node, g.p.Step, g.p.Steps = node, v, m
	g.report(v == m)
}

func (g *generation) preview(i int, data []byte, format string) {
	if g.opts.OnPreview == nil {
		return
	}
	g.opts.OnPreview(data, format)
	g.p.Preview++
	g.report(false)
}

func (g *generation) image(i int, out comfy.Output) error {
	id, err := g.store(i, out)
	var de decodeError
	if errors.As(err, &de) {
		g.storeErr[i] = err
		return nil
	}
	if err != nil {
		return err
	}
	g.ids[i] = append(g.ids[i], id)
	g.p.Images++
	g.report(true)
	if g.opts.OnImage != nil {
		g.opts.OnImage()
	}
	return nil
}

// store saves an image in the experiment. PNGs get the prompt embedded,
// so ComfyUI can open them.
func (g *generation) store(i int, out comfy.Output) (int64, error) {
	ctx := context.Background() // keep what arrived even when cancelled
	data := out.Data
	f, reason := imaging.Sniff(data, "")
	if f == "" {
		return 0, decodeError{"ComfyUI sent something that is not a supported image (" + reason + ")"}
	}
	if f == imaging.PNG {
		data = comfy.EmbedPrompt(data, g.prompts[i])
	}
	res, err := imaging.Process(f, data)
	if err != nil {
		return 0, decodeError{"ComfyUI sent an image that cannot be read: " + err.Error()}
	}
	sum := sha256.Sum256(data)
	it := g.plan.Items[i]
	applied, _ := json.Marshal(it.Applied)
	var seed any
	if it.Seed != nil {
		seed = *it.Seed
	}
	var wid any
	if g.r.workflowID != 0 {
		wid = g.r.workflowID
	}
	var id int64
	err = g.b.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO blobs(sha256, size, data) VALUES (?, ?, ?) ON CONFLICT(sha256) DO NOTHING",
			sum[:], len(data), data); err != nil {
			return err
		}
		var blobID int64
		if err := tx.QueryRowContext(ctx, "SELECT id FROM blobs WHERE sha256 = ?", sum[:]).Scan(&blobID); err != nil {
			return err
		}
		if err := library.SaveThumb(ctx, tx, g.b, blobID, sum[:], res); err != nil {
			return err
		}
		fp := res.Fingerprint
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO fingerprints(blob_id, version, phash, color, aspect, ac_energy)
			VALUES (?, ?, ?, ?, ?, ?)`, blobID, fp.Version, int64(fp.PHash), fp.Color[:], fp.Aspect, fp.ACEnergy); err != nil {
			return err
		}
		r, err := tx.ExecContext(ctx, `INSERT INTO generations(experiment_id, run_id, version_id, workflow_id, workflow_name,
			applied, combo, repeat, batch_index, seed, sha256, blob_id, format, size, width, height, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			g.experimentID, g.runID, g.r.versionID, wid, g.r.name, string(applied), it.Combo, it.Repeat, out.Index, seed,
			sum[:], blobID, string(f), len(data), res.Width, res.Height, bag.NowMillis())
		if err != nil {
			return err
		}
		id, _ = r.LastInsertId()
		_, err = tx.ExecContext(ctx, "UPDATE generation_runs SET images = images + 1 WHERE id = ?", g.runID)
		return err
	})
	return id, err
}

func (g *generation) finished(i int, millis int64, err error) {
	if err == nil && len(g.ids[i]) == 0 && g.storeErr[i] != nil {
		err = g.storeErr[i]
	}
	g.p.Done++
	ctx := context.Background()
	if err != nil {
		g.p.Failed++
		g.p.LastError = err.Error()
		it := g.plan.Items[i]
		g.failures = append(g.failures, Failure{Combo: it.Combo, Repeat: it.Repeat, Values: g.swept(i), Error: err.Error()})
	} else if ids := g.ids[i]; len(ids) > 0 {
		js, _ := json.Marshal(ids)
		if _, err := g.b.W.ExecContext(ctx, "UPDATE generations SET millis = ? WHERE id IN (SELECT value FROM json_each(?))",
			millis, string(js)); err != nil {
			g.logger().Warn("recording the generation time", "err", err)
		}
	}
	fj, _ := json.Marshal(g.failures)
	if _, err := g.b.W.ExecContext(ctx, "UPDATE generation_runs SET done = ?, failed = ?, failures = ? WHERE id = ?",
		g.p.Done, g.p.Failed, string(fj), g.runID); err != nil {
		g.logger().Warn("recording generation progress", "err", err)
	}
	g.report(true)
}

func (g *generation) saveRun(status, msg string) {
	fj, _ := json.Marshal(g.failures)
	if _, err := g.b.W.ExecContext(context.Background(), `UPDATE generation_runs SET status = ?, error = ?, done = ?, failed = ?,
		failures = ?, finished_at = ? WHERE id = ?`, status, msg, g.p.Done, g.p.Failed, string(fj), bag.NowMillis(), g.runID); err != nil {
		g.logger().Warn("recording the end of a generation", "err", err)
	}
}

func (g *generation) logger() *slog.Logger {
	if g.opts.Log != nil {
		return g.opts.Log
	}
	return slog.Default()
}

// ListRuns returns an experiment's runs, newest first.
func ListRuns(ctx context.Context, b *bag.Bag, experimentID int64) ([]Run, error) {
	rows, err := b.R.QueryContext(ctx, `SELECT id, experiment_id, version_id, COALESCE(workflow_id, 0), workflow_name, request,
		dims, prompts, done, failed, images, status, error, failures, job_id, created_at, COALESCE(finished_at, 0)
		FROM generation_runs WHERE experiment_id = ? ORDER BY id DESC`, experimentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var req, dims, failures string
		if err := rows.Scan(&r.ID, &r.ExperimentID, &r.VersionID, &r.WorkflowID, &r.WorkflowName, &req, &dims, &r.Prompts,
			&r.Done, &r.Failed, &r.Images, &r.Status, &r.Error, &failures, &r.JobID, &r.CreatedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(req), &r.Request)
		if r.Request.Overrides == nil {
			r.Request.Overrides = []comfy.Override{}
		}
		if json.Unmarshal([]byte(dims), &r.Dims) != nil || r.Dims == nil {
			r.Dims = []comfy.Dim{}
		}
		if json.Unmarshal([]byte(failures), &r.Failures) != nil || r.Failures == nil {
			r.Failures = []Failure{}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecoverRuns marks runs left "running" by a PhotoBag that stopped.
func RecoverRuns(ctx context.Context, b *bag.Bag) error {
	_, err := b.W.ExecContext(ctx, `UPDATE generation_runs SET status = ?, error = 'PhotoBag stopped while it ran',
		finished_at = COALESCE(finished_at, ?) WHERE status = ?`, RunCancelled, bag.NowMillis(), RunRunning)
	return err
}
