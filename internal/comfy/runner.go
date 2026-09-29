package comfy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// Output is an image a prompt produced.
type Output struct {
	Data   []byte
	Format string // "png", "jpeg" or "webp"
	Node   string // the output node's id
	// Index counts the prompt's images from 0 (a batch gives several).
	Index int
}

// Events receives what happens to a run's prompts, all from one
// goroutine. Any may be nil.
type Events struct {
	// Started: ComfyUI began the prompt.
	Started func(i int)
	// Progress reports a node's steps (a sampler's, say).
	Progress func(i int, node string, value, max int)
	// Preview carries a sampler preview image.
	Preview func(i int, data []byte, format string)
	// Image carries a finished image; an error ends the run.
	Image func(i int, out Output) error
	// Finished: the prompt is done; err says why it failed.
	Finished func(i int, millis int64, err error)
}

// Runner runs prompts on a ComfyUI server.
type Runner struct {
	Client *Client
	// Window is how many prompts are kept queued in ComfyUI, so it never
	// waits for the next one (default 2).
	Window int
	// Poll is how long without news before the queue is checked (default
	// 15 s): it catches prompts whose messages were lost.
	Poll time.Duration
	Log  *slog.Logger
}

// Run failures.
var (
	ErrNoImages    = errors.New("ComfyUI returned no images")
	ErrInterrupted = errors.New("interrupted in ComfyUI")
	ErrNoOutput    = errors.New("the workflow has no node that returns images (such as Send Image (WebSocket) or Save Image)")
)

type promptState struct {
	i       int
	id      string
	kind    string
	outputs []string
	queued  time.Time
	started time.Time
	images  int
	cached  bool
	missing int // polls that found it neither queued nor finished
}

type run struct {
	r        *Runner
	ctx      context.Context
	prompts  []*Workflow
	ev       Events
	clientID string

	active      map[string]*promptState
	next        int
	current     *promptState
	currentNode string
	successes   int
	failures    int // in a row
}

// Run queues the prompts and waits for their images. It returns when all
// are done, or with an error when ComfyUI cannot be reached or the first
// prompts all fail. Cancelling ctx removes the waiting prompts from
// ComfyUI's queue and interrupts the running one.
func (r *Runner) Run(ctx context.Context, prompts []*Workflow, ev Events) error {
	if len(prompts) == 0 {
		return nil
	}
	window := r.Window
	if window <= 0 {
		window = 2
	}
	poll := r.Poll
	if poll <= 0 {
		poll = 15 * time.Second
	}
	ru := &run{r: r, ctx: ctx, prompts: prompts, ev: ev, clientID: uuid.NewString(), active: map[string]*promptState{}}
	ws, err := r.Client.dial(ctx, ru.clientID)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("connecting to ComfyUI's websocket at %s: %w", r.Client.endpoint, err)
	}
	msgs := make(chan message, 64)
	rd := listen(ctx, ws, msgs)
	defer func() { rd.close() }()

	if err := ru.fill(window); err != nil {
		ru.abandon()
		return err
	}
	ticker := time.NewTicker(poll / 3)
	defer ticker.Stop()
	lastNews := time.Now()
	for len(ru.active) > 0 {
		select {
		case <-ctx.Done():
			ru.abandon()
			return ctx.Err()
		case err := <-rd.errc:
			if ctx.Err() != nil {
				ru.abandon()
				return ctx.Err()
			}
			r.logger().Warn("ComfyUI websocket closed; reconnecting", "err", err)
			rd.close()
			ws, err := ru.reconnect()
			if err != nil {
				ru.abandon()
				return fmt.Errorf("lost the connection to ComfyUI: %w", err)
			}
			rd = listen(ctx, ws, msgs)
			lastNews = time.Time{} // check the queue now
		case m := <-msgs:
			lastNews = time.Now()
			if err := ru.handle(m); err != nil {
				ru.abandon()
				return err
			}
		case <-ticker.C:
		}
		if time.Since(lastNews) > poll {
			if err := ru.poll(); err != nil {
				ru.abandon()
				return err
			}
			lastNews = time.Now()
		}
		if err := ru.fill(window); err != nil {
			ru.abandon()
			return err
		}
	}
	return nil
}

// listener reads one websocket connection.
type listener struct {
	ws   *websocket.Conn
	stop context.CancelFunc
	errc chan error
}

func listen(ctx context.Context, ws *websocket.Conn, out chan<- message) *listener {
	rctx, stop := context.WithCancel(ctx)
	l := &listener{ws: ws, stop: stop, errc: make(chan error, 1)}
	go readLoop(rctx, ws, out, l.errc)
	return l
}

func (l *listener) close() {
	l.stop()
	l.ws.CloseNow()
}

func (r *Runner) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (ru *run) reconnect() (*websocket.Conn, error) {
	var err error
	for i, wait := range []time.Duration{250 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second} {
		select {
		case <-time.After(wait):
		case <-ru.ctx.Done():
			return nil, ru.ctx.Err()
		}
		var ws *websocket.Conn
		if ws, err = ru.r.Client.dial(ru.ctx, ru.clientID); err == nil {
			ru.r.logger().Info("reconnected to ComfyUI", "attempt", i+1)
			return ws, nil
		}
	}
	return nil, err
}

// fill queues prompts until window are waiting or running.
func (ru *run) fill(window int) error {
	for len(ru.active) < window && ru.next < len(ru.prompts) {
		i := ru.next
		ru.next++
		w := ru.prompts[i]
		kind, outs := w.Outputs()
		if kind == "" {
			if err := ru.fail(i, 0, ErrNoOutput); err != nil {
				return err
			}
			continue
		}
		id := uuid.NewString()
		err := ru.r.Client.Submit(ru.ctx, w, ru.clientID, id)
		var pe *PromptError
		if errors.As(err, &pe) {
			if err := ru.fail(i, 0, err); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		ru.active[id] = &promptState{i: i, id: id, kind: kind, outputs: outs, queued: time.Now()}
	}
	return nil
}

// fail reports a failed prompt and ends the run when failures pile up
// (the first three, or ten in a row later).
func (ru *run) fail(i int, millis int64, err error) error {
	if ru.ev.Finished != nil {
		ru.ev.Finished(i, millis, err)
	}
	ru.failures++
	if (ru.successes == 0 && ru.failures >= 3) || ru.failures >= 10 {
		return fmt.Errorf("stopped after %d failed prompts in a row; the last: %w", ru.failures, err)
	}
	return nil
}

func (ru *run) finish(st *promptState, err error) error {
	delete(ru.active, st.id)
	if ru.current == st {
		ru.current, ru.currentNode = nil, ""
	}
	start := st.started
	if start.IsZero() {
		start = st.queued
	}
	millis := time.Since(start).Milliseconds()
	if err == nil && st.images == 0 {
		err = ErrNoImages
		if st.cached {
			err = fmt.Errorf("%w: its output was cached, as ComfyUI skips work it has done before (change the seed)", ErrNoImages)
		}
	}
	if err != nil {
		return ru.fail(st.i, millis, err)
	}
	ru.successes++
	ru.failures = 0
	if ru.ev.Finished != nil {
		ru.ev.Finished(st.i, millis, nil)
	}
	return nil
}

func (ru *run) begin(st *promptState) {
	if ru.current == st {
		return
	}
	ru.current = st
	if st.started.IsZero() {
		st.started = time.Now()
		if ru.ev.Started != nil {
			ru.ev.Started(st.i)
		}
	}
}

type promptMsg struct {
	PromptID string `json:"prompt_id"`
}

func (ru *run) handle(m message) error {
	switch m.typ {
	case "image":
		st := ru.current
		if st == nil {
			return nil
		}
		if st.kind == OutputWebsocket && slices.Contains(st.outputs, ru.currentNode) {
			return ru.image(st, Output{Data: m.img, Format: m.format, Node: ru.currentNode})
		}
		if ru.ev.Preview != nil {
			ru.ev.Preview(st.i, m.img, m.format)
		}
		return nil
	case "preview":
		st := ru.active[m.promptID]
		if st == nil {
			st = ru.current
		}
		if st != nil && ru.ev.Preview != nil {
			ru.ev.Preview(st.i, m.img, m.format)
		}
		return nil
	}
	var pm promptMsg
	_ = json.Unmarshal(m.data, &pm)
	st := ru.active[pm.PromptID]
	if st == nil {
		return nil // another client's prompt, or one already finished
	}
	switch m.typ {
	case "execution_start":
		ru.begin(st)
	case "execution_cached":
		var d struct {
			Nodes []string `json:"nodes"`
		}
		_ = json.Unmarshal(m.data, &d)
		for _, n := range d.Nodes {
			if slices.Contains(st.outputs, n) {
				st.cached = true
			}
		}
	case "executing":
		var d struct {
			Node *string `json:"node"`
		}
		_ = json.Unmarshal(m.data, &d)
		if d.Node == nil {
			return ru.finish(st, nil) // older servers end with this
		}
		ru.begin(st)
		ru.currentNode = *d.Node
	case "progress":
		var d struct {
			Value int    `json:"value"`
			Max   int    `json:"max"`
			Node  string `json:"node"`
		}
		_ = json.Unmarshal(m.data, &d)
		ru.begin(st)
		if ru.ev.Progress != nil {
			ru.ev.Progress(st.i, ru.prompts[st.i].NodeLabel(d.Node), d.Value, d.Max)
		}
	case "executed":
		if st.kind != OutputFile {
			return nil
		}
		var d struct {
			Node   string `json:"node"`
			Output struct {
				Images []FileRef `json:"images"`
			} `json:"output"`
		}
		_ = json.Unmarshal(m.data, &d)
		if slices.Contains(st.outputs, d.Node) {
			return ru.download(st, d.Node, d.Output.Images)
		}
	case "execution_success":
		return ru.finish(st, nil)
	case "execution_error":
		var d execError
		_ = json.Unmarshal(m.data, &d)
		return ru.finish(st, errors.New(d.text()))
	case "execution_interrupted":
		return ru.finish(st, ErrInterrupted)
	}
	return nil
}

func (ru *run) image(st *promptState, out Output) error {
	out.Index = st.images
	st.images++
	if ru.ev.Image != nil {
		return ru.ev.Image(st.i, out)
	}
	return nil
}

// download fetches images a Save Image node wrote.
func (ru *run) download(st *promptState, node string, files []FileRef) error {
	for _, f := range files {
		data, err := ru.r.Client.View(ru.ctx, f)
		if err != nil {
			return fmt.Errorf("downloading %s from ComfyUI: %w", f.Filename, err)
		}
		format := strings.TrimPrefix(strings.ToLower(path.Ext(f.Filename)), ".")
		if format == "jpg" {
			format = "jpeg"
		}
		if err := ru.image(st, Output{Data: data, Format: format, Node: node}); err != nil {
			return err
		}
	}
	return nil
}

// poll checks on prompts that went quiet: finished ones whose messages
// were lost are read from ComfyUI's history.
func (ru *run) poll() error {
	q, err := ru.r.Client.Queue(ru.ctx)
	if err != nil {
		if ru.ctx.Err() != nil {
			return ru.ctx.Err()
		}
		return fmt.Errorf("checking ComfyUI's queue: %w", err)
	}
	for _, st := range ru.byOrder() {
		if slices.Contains(q.Running, st.id) || slices.Contains(q.Pending, st.id) {
			st.missing = 0
			continue
		}
		h, err := ru.r.Client.History(ru.ctx, st.id)
		if err != nil {
			return fmt.Errorf("checking ComfyUI's history: %w", err)
		}
		if h == nil {
			if st.missing++; st.missing >= 2 {
				if err := ru.finish(st, errors.New("ComfyUI lost the prompt (was it restarted?)")); err != nil {
					return err
				}
			}
			continue
		}
		if !h.Success {
			if err := ru.finish(st, errors.New(h.Error)); err != nil {
				return err
			}
			continue
		}
		if st.kind == OutputFile && st.images == 0 {
			for _, n := range st.outputs {
				if err := ru.download(st, n, h.Files[n]); err != nil {
					return err
				}
			}
		}
		err = nil
		if st.images == 0 && st.kind == OutputWebsocket {
			err = errors.New("the connection to ComfyUI dropped while it ran, so its images were lost")
		}
		if err := ru.finish(st, err); err != nil {
			return err
		}
	}
	return nil
}

func (ru *run) byOrder() []*promptState {
	out := make([]*promptState, 0, len(ru.active))
	for _, st := range ru.active {
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b *promptState) int { return a.i - b.i })
	return out
}

// abandon removes this run's prompts from ComfyUI: waiting ones are
// dequeued, the running one interrupted.
func (ru *run) abandon() {
	if len(ru.active) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ids []string
	for id := range ru.active {
		ids = append(ids, id)
	}
	c := ru.r.Client
	if err := c.Dequeue(ctx, ids); err != nil {
		ru.r.logger().Warn("could not remove prompts from ComfyUI's queue", "err", err)
	}
	running := map[string]bool{}
	if ru.current != nil {
		running[ru.current.id] = true
	}
	if q, err := c.Queue(ctx); err == nil {
		for _, id := range q.Running {
			if ru.active[id] != nil {
				running[id] = true
			}
		}
	}
	for id := range running {
		if err := c.Interrupt(ctx, id); err != nil {
			ru.r.logger().Warn("could not interrupt the ComfyUI prompt", "err", err)
		}
	}
	ru.active = map[string]*promptState{}
}
