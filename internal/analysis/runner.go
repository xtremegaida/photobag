package analysis

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"photobag/internal/bag"
	"photobag/internal/imaging"
	"photobag/internal/library"
	"photobag/internal/llm"
	"photobag/internal/query"
)

// Modes choose which images a job sends to the model. Results a person
// edited are never replaced.
const (
	ModeMissing = "missing" // images without a result from the pipeline
	ModeChanged = "changed" // also results made with another model, prompt or options
	ModeAll     = "all"     // every image
)

// Options select the images and pipelines of an analysis job.
type Options struct {
	Query     query.ImageQuery `json:"query"`
	Pipelines []string         `json:"pipelines"`
	Mode      string           `json:"mode"`
}

// Validate checks the options.
func (o *Options) Validate() error {
	if len(o.Pipelines) == 0 {
		return fmt.Errorf("choose at least one pipeline")
	}
	seen := map[string]bool{}
	var ps []string
	for _, p := range o.Pipelines {
		if !library.ValidPipeline(p) {
			return fmt.Errorf("unknown pipeline %q", p)
		}
		if !seen[p] {
			seen[p] = true
			ps = append(ps, p)
		}
	}
	o.Pipelines = ps
	switch o.Mode {
	case "":
		o.Mode = ModeMissing
	case ModeMissing, ModeChanged, ModeAll:
	default:
		return fmt.Errorf("unknown mode %q", o.Mode)
	}
	return o.Query.Validate()
}

// Plan is the work an analysis job would do.
type Plan struct {
	// Images match the query; Todo of them need Requests pipeline runs.
	Images     int            `json:"images"`
	Todo       int            `json:"todo"`
	Requests   int            `json:"requests"`
	ByPipeline map[string]int `json:"byPipeline"`
	// Edited results are kept, so their images are left out.
	Edited int `json:"edited"`
	work   []work
}

type work struct {
	id        int64
	pipelines []string
}

// MakePlan works out which images need which pipelines.
func MakePlan(ctx context.Context, b *bag.Bag, s Settings, opts Options) (*Plan, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	ids, err := library.ListIDs(ctx, b, opts.Query, query.Sort{})
	if err != nil {
		return nil, err
	}
	p := &Plan{Images: len(ids), ByPipeline: map[string]int{}}
	type state struct {
		config string
		edited bool
	}
	have := map[string]map[int64]state{}
	js, _ := json.Marshal(ids)
	for _, pl := range opts.Pipelines {
		have[pl] = map[int64]state{}
		rows, err := b.R.QueryContext(ctx, `SELECT image_id, config, edited FROM analyses
			WHERE pipeline = ? AND image_id IN (SELECT value FROM json_each(?))`, pl, string(js))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var st state
			if err := rows.Scan(&id, &st.config, &st.edited); err != nil {
				rows.Close()
				return nil, err
			}
			have[pl][id] = st
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	configs := map[string]string{}
	for _, pl := range opts.Pipelines {
		configs[pl] = s.ConfigHash(pl)
	}
	for _, id := range ids {
		var need []string
		for _, pl := range opts.Pipelines {
			st, ok := have[pl][id]
			switch {
			case ok && st.edited:
				p.Edited++
				continue
			case !ok:
			case opts.Mode == ModeAll:
			case opts.Mode == ModeChanged && st.config != configs[pl]:
			default:
				continue
			}
			need = append(need, pl)
			p.ByPipeline[pl]++
		}
		if len(need) > 0 {
			p.Requests += len(need)
			p.work = append(p.work, work{id: id, pipelines: need})
		}
	}
	p.Todo = len(p.work)
	return p, nil
}

// Progress of an analysis job.
type Progress struct {
	Images           int    `json:"images"`
	Total            int    `json:"total"` // pipeline runs planned
	Done             int    `json:"done"`  // stored or failed
	Stored           int    `json:"stored"`
	Failed           int    `json:"failed"`
	Current          string `json:"current,omitempty"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	LastError        string `json:"lastError,omitempty"`
}

// Failure is one pipeline run that failed.
type Failure struct {
	ImageID  int64  `json:"imageId"`
	Name     string `json:"name"`
	Pipeline string `json:"pipeline"`
	Error    string `json:"error"`
}

// Report summarises an analysis job.
type Report struct {
	Images   int `json:"images"`
	Requests int `json:"requests"`
	Stored   int `json:"stored"`
	Failed   int `json:"failed"`
	// Skipped results were edited by a person while the job ran, or their
	// image left the library.
	Skipped          int            `json:"skipped"`
	ByPipeline       map[string]int `json:"byPipeline"`
	PromptTokens     int64          `json:"promptTokens"`
	CompletionTokens int64          `json:"completionTokens"`
	Model            string         `json:"model"`
	Millis           int64          `json:"millis"`
	Cancelled        bool           `json:"cancelled,omitempty"`
	// Stopped says why the job gave up early.
	Stopped  string    `json:"stopped,omitempty"`
	Failures []Failure `json:"failures"`
}

const maxFailures = 200

// Outcome is the result of one pipeline run on one image.
type Outcome struct {
	Pipeline string `json:"pipeline"`
	Text     string `json:"text"`
	// Tags are Danbooru tags; Main and Sub the category.
	Tags []string `json:"tags,omitempty"`
	Main string   `json:"main,omitempty"`
	Sub  string   `json:"sub,omitempty"`
	// TagNames are the PhotoBag tags attached for this result.
	TagNames         []string `json:"tagNames,omitempty"`
	Reply            string   `json:"reply"`
	Reasoning        string   `json:"reasoning,omitempty"`
	Model            string   `json:"model"`
	Millis           int64    `json:"millis"`
	PromptTokens     int      `json:"promptTokens"`
	CompletionTokens int      `json:"completionTokens"`
	Error            string   `json:"error,omitempty"`
	Stored           bool     `json:"stored"`
	attach           bool
}

// analyser runs pipelines with one model client.
type analyser struct {
	s      Settings
	client *llm.Client
	cats   []Category
	mu     sync.Mutex
	inUse  map[string]int // free-form categories already assigned
}

func newAnalyser(ctx context.Context, b *bag.Bag, s Settings, client *llm.Client, pipelines []string) (*analyser, error) {
	a := &analyser{s: s, client: client, cats: ParseCategories(s.Category.Categories), inUse: map[string]int{}}
	for _, p := range pipelines {
		if p == library.PipelineCategory && len(a.cats) == 0 {
			existing, err := library.ExistingCategories(ctx, b, 200)
			if err != nil {
				return nil, err
			}
			for i, e := range existing {
				a.inUse[e] = len(existing) - i
			}
		}
	}
	return a, nil
}

// existing lists the most used free-form categories for the prompt.
func (a *analyser) existing() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	counts := map[string]int{}
	for e, n := range a.inUse {
		if a.s.Category.Levels == 1 {
			e, _, _ = strings.Cut(e, " / ")
		}
		counts[e] += n
	}
	out := make([]string, 0, len(counts))
	for e := range counts {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if counts[out[i]] != counts[out[j]] {
			return counts[out[i]] > counts[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// parse interprets a reply into o.
func (a *analyser) parse(pipeline, reply string, o *Outcome) error {
	switch pipeline {
	case library.PipelineOCR:
		o.Text = parseOCR(reply)
	case library.PipelineCaption:
		t, err := parseCaption(reply)
		if err != nil {
			return err
		}
		o.Text = t
	case library.PipelineDanbooru:
		tags, err := parseDanbooru(reply, a.s.Danbooru.MaxTags)
		if err != nil {
			return err
		}
		o.Tags, o.Text = tags, strings.Join(tags, ", ")
		if a.s.Danbooru.AddTags {
			o.TagNames, o.attach = a.s.danbooruTagNames(tags), true
		}
	case library.PipelineCategory:
		main, sub, err := parseCategory(reply, a.s.Category.Levels, a.cats, a.existing())
		if err != nil {
			return err
		}
		o.Main, o.Sub, o.Text = main, sub, categoryText(main, sub)
		o.TagNames, o.attach = a.s.categoryTagNames(main, sub), true
	}
	return nil
}

// run sends one image through one pipeline. A reply that does not parse
// is answered with a hint once.
func (a *analyser) run(ctx context.Context, pipeline string, img []byte) (*Outcome, error) {
	o := &Outcome{Pipeline: pipeline, Model: a.s.Model}
	msgs := []llm.Message{
		{Role: "system", Text: a.s.system()},
		{Role: "user", Text: a.s.Prompt(pipeline, a.existing()), Images: [][]byte{img}},
	}
	start := time.Now()
	for attempt := 0; ; attempt++ {
		r, err := a.client.Chat(ctx, msgs)
		o.Millis = time.Since(start).Milliseconds()
		if err != nil {
			o.Error = err.Error()
			return o, err
		}
		o.Reply, o.Reasoning = r.Text, r.Reasoning
		o.PromptTokens += r.PromptTokens
		o.CompletionTokens += r.CompletionTokens
		if r.Model != "" {
			o.Model = r.Model
		}
		err = a.parse(pipeline, r.Text, o)
		var pe *ParseError
		if err != nil && errors.As(err, &pe) && pe.Hint != "" && attempt == 0 {
			msgs = append(msgs, llm.Message{Role: "assistant", Text: r.Text}, llm.Message{Role: "user", Text: pe.Hint})
			continue
		}
		if err != nil {
			o.Error = fmt.Sprintf("%v (reply: %q)", err, truncate(r.Text, 200))
			return o, errors.New(o.Error)
		}
		if pipeline == library.PipelineCategory && len(a.cats) == 0 {
			a.mu.Lock()
			a.inUse[o.Text]++
			a.mu.Unlock()
		}
		return o, nil
	}
}

func (a *analyser) store(ctx context.Context, b *bag.Bag, id int64, o *Outcome, force bool) (bool, error) {
	w := library.AnalysisWrite{
		ImageID: id, Pipeline: o.Pipeline, Text: o.Text, Model: o.Model,
		Config: a.s.ConfigHash(o.Pipeline), Force: force,
	}
	if len(o.Tags) > 0 || o.Main != "" {
		w.Data = &library.AnalysisData{Tags: o.Tags, Main: o.Main, Sub: o.Sub}
	}
	if o.attach {
		w.Tags = o.TagNames
		if w.Tags == nil {
			w.Tags = []string{}
		}
	}
	return library.WriteAnalysis(ctx, b, w)
}

// PrepareImage renders the JPEG sent to the model: the oriented image
// scaled to fit size × size.
func PrepareImage(ctx context.Context, b *bag.Bag, id int64, size int) ([]byte, error) {
	blobID, format, err := library.BlobInfo(ctx, b, id)
	if err != nil {
		return nil, fmt.Errorf("image %d: %w", id, err)
	}
	raw, err := library.BlobData(ctx, b, blobID)
	if err != nil {
		return nil, err
	}
	return imaging.Preview(imaging.Format(format), raw, size)
}

// Run executes a plan (see MakePlan) with the given client.
func Run(ctx context.Context, b *bag.Bag, s Settings, client *llm.Client, plan *Plan, opts Options, progress func(Progress)) (*Report, error) {
	if progress == nil {
		progress = func(Progress) {}
	}
	start := time.Now()
	rep := &Report{Images: len(plan.work), Requests: plan.Requests, ByPipeline: map[string]int{}, Model: s.Model, Failures: []Failure{}}
	a, err := newAnalyser(ctx, b, s, client, opts.Pipelines)
	if err != nil {
		return rep, err
	}
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)

	var mu sync.Mutex
	pr := Progress{Images: len(plan.work), Total: plan.Requests}
	successes, streak := 0, 0
	progress(pr)
	fail := func(id int64, name, pipeline string, err error) {
		if ctx.Err() != nil {
			return // cancelled: not the image's fault
		}
		pr.Done++
		pr.Failed++
		rep.Failed++
		pr.LastError = err.Error()
		if len(rep.Failures) < maxFailures {
			rep.Failures = append(rep.Failures, Failure{ImageID: id, Name: name, Pipeline: pipeline, Error: err.Error()})
		}
		streak++
		switch {
		case llm.IsFatal(err):
			stop(err)
		case successes == 0 && streak >= 5:
			stop(fmt.Errorf("the first %d requests failed; last error: %v", streak, err))
		case streak >= 25:
			stop(fmt.Errorf("%d requests in a row failed; last error: %v", streak, err))
		}
	}

	names := map[int64]string{}
	jobs := make(chan work)
	var wg sync.WaitGroup
	for w := 0; w < min(s.Concurrency, max(1, len(plan.work))); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for wk := range jobs {
				mu.Lock()
				name := names[wk.id]
				mu.Unlock()
				img, err := PrepareImage(ctx, b, wk.id, s.ImageSize)
				if err != nil {
					mu.Lock()
					for _, p := range wk.pipelines {
						if errors.Is(err, library.ErrNotFound) {
							pr.Done++
							rep.Skipped++
						} else {
							fail(wk.id, name, p, err)
						}
					}
					progress(pr)
					mu.Unlock()
					continue
				}
				for _, p := range wk.pipelines {
					if ctx.Err() != nil {
						break
					}
					mu.Lock()
					pr.Current = name + " · " + p
					progress(pr)
					mu.Unlock()
					o, err := a.run(ctx, p, img)
					stored := false
					if err == nil {
						stored, err = a.store(ctx, b, wk.id, o, false)
					}
					mu.Lock()
					pr.PromptTokens += int64(o.PromptTokens)
					pr.CompletionTokens += int64(o.CompletionTokens)
					switch {
					case err != nil:
						fail(wk.id, name, p, err)
					case stored:
						pr.Done++
						pr.Stored++
						rep.Stored++
						rep.ByPipeline[p]++
						successes++
						streak = 0
					default:
						pr.Done++
						rep.Skipped++
					}
					progress(pr)
					mu.Unlock()
				}
			}
		}()
	}

	// Names for progress and the failure list, fetched in batches.
	go func() {
		defer close(jobs)
		for i := 0; i < len(plan.work); i += 500 {
			batch := plan.work[i:min(i+500, len(plan.work))]
			ids := make([]int64, len(batch))
			for j, w := range batch {
				ids[j] = w.id
			}
			if ims, err := library.GetImages(ctx, b, ids); err == nil {
				mu.Lock()
				for _, im := range ims {
					names[im.ID] = im.Name
				}
				mu.Unlock()
			}
			for _, w := range batch {
				select {
				case jobs <- w:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	wg.Wait()

	rep.PromptTokens, rep.CompletionTokens = pr.PromptTokens, pr.CompletionTokens
	rep.Millis = time.Since(start).Milliseconds()
	pr.Current = ""
	progress(pr)
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		rep.Stopped = cause.Error()
		return rep, cause
	}
	if ctx.Err() != nil {
		rep.Cancelled = true
		return rep, ctx.Err()
	}
	return rep, nil
}

// RunOne sends one image through pipelines now. With save the results are
// stored and their tags attached; as this is an explicit request, results a
// person edited are replaced too.
func RunOne(ctx context.Context, b *bag.Bag, s Settings, client *llm.Client, id int64, pipelines []string, save bool) ([]Outcome, error) {
	opts := Options{Pipelines: pipelines}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	a, err := newAnalyser(ctx, b, s, client, opts.Pipelines)
	if err != nil {
		return nil, err
	}
	img, err := PrepareImage(ctx, b, id, s.ImageSize)
	if err != nil {
		return nil, err
	}
	var out []Outcome
	for _, p := range opts.Pipelines {
		o, err := a.run(ctx, p, img)
		if err == nil && save {
			o.Stored, err = a.store(ctx, b, id, o, true)
			if err == nil && !o.Stored {
				o.Error = "not stored: the image has been removed"
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			o.Error = err.Error()
			if llm.IsFatal(err) {
				return append(out, *o), err
			}
		}
		out = append(out, *o)
	}
	return out, nil
}

// Retag re-applies the tags a pipeline attaches (Danbooru or category)
// from its stored results under the current options, for example after
// turning on "add as tags" or changing a prefix, without asking the model
// again. With Danbooru tags turned off, their tags are removed. It returns
// how many images were updated.
func Retag(ctx context.Context, b *bag.Bag, s Settings, pipeline string, progress func(done, total int)) (int, error) {
	if pipeline != library.PipelineDanbooru && pipeline != library.PipelineCategory {
		return 0, fmt.Errorf("%s results do not add tags", pipeline)
	}
	if progress == nil {
		progress = func(int, int) {}
	}
	if pipeline == library.PipelineDanbooru && !s.Danbooru.AddTags {
		_, err := library.RemoveSourceTags(ctx, b, pipeline)
		return 0, err
	}
	rows, err := b.R.QueryContext(ctx, `SELECT a.image_id, COALESCE(a.data, '') FROM analyses a
		JOIN images i ON i.id = a.image_id WHERE a.pipeline = ? AND i.purged_at IS NULL ORDER BY a.image_id`, pipeline)
	if err != nil {
		return 0, err
	}
	type item struct {
		id   int64
		tags []string
	}
	var items []item
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		var d library.AnalysisData
		if raw != "" {
			json.Unmarshal([]byte(raw), &d)
		}
		var names []string
		if pipeline == library.PipelineDanbooru {
			names = s.danbooruTagNames(d.Tags)
		} else if d.Main != "" {
			names = s.categoryTagNames(d.Main, d.Sub)
		}
		items = append(items, item{id, names})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	progress(0, len(items))
	for i := 0; i < len(items); i += 200 {
		batch := items[i:min(i+200, len(items))]
		err := b.Tx(ctx, func(tx *sql.Tx) error {
			for _, it := range batch {
				if err := library.SetSourceTags(ctx, tx, it.id, pipeline, it.tags); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return i, err
		}
		progress(i+len(batch), len(items))
	}
	return len(items), nil
}
