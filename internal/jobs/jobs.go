// Package jobs runs long operations (import, export, backup, dedup scans,
// compaction, model analysis) in the background, publishing progress. Jobs
// run one at a time per lane.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"photobag/internal/events"
)

// Statuses.
const (
	Queued    = "queued"
	Running   = "running"
	Done      = "done"
	Failed    = "failed"
	Cancelled = "cancelled"
)

// Job is a snapshot of a background job.
type Job struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Progress   any    `json:"progress,omitempty"`
	Message    string `json:"message,omitempty"`
	Result     any    `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	StartedAt  int64  `json:"startedAt,omitempty"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
}

// Func is the body of a job. report publishes progress (any JSON value)
// and an optional message.
type Func func(ctx context.Context, report func(progress any, message string)) (any, error)

type entry struct {
	job    Job
	lane   string
	fn     Func
	ctx    context.Context
	cancel context.CancelFunc
}

// MainLane runs jobs that touch much of the bag (imports, exports,
// backups); other lanes run beside it.
const MainLane = "main"

// Manager executes jobs sequentially within each lane.
type Manager struct {
	mu      sync.Mutex
	entries map[string]*entry
	order   []string
	lanes   map[string]chan *entry
	ctx     context.Context
	events  *events.Broker
	log     *slog.Logger
	wg      sync.WaitGroup
}

// maxHistory is how many finished jobs are remembered.
const maxHistory = 50

// NewManager starts a manager whose jobs are cancelled when ctx ends.
func NewManager(ctx context.Context, ev *events.Broker, log *slog.Logger) *Manager {
	m := &Manager{
		entries: map[string]*entry{},
		lanes:   map[string]chan *entry{},
		ctx:     ctx,
		events:  ev,
		log:     log,
	}
	m.mu.Lock()
	m.lane(MainLane)
	m.mu.Unlock()
	return m
}

// lane returns a lane's queue, starting its worker (mu held).
func (m *Manager) lane(name string) chan *entry {
	q, ok := m.lanes[name]
	if ok {
		return q
	}
	q = make(chan *entry, 256)
	m.lanes[name] = q
	m.wg.Add(1)
	go m.loop(q)
	return q
}

func (m *Manager) loop(q chan *entry) {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			m.mu.Lock()
			for _, e := range m.entries {
				e.cancel()
			}
			m.mu.Unlock()
			return
		case e := <-q:
			m.run(e)
		}
	}
}

// Wait blocks until the manager has stopped (after its context ended).
func (m *Manager) Wait() { m.wg.Wait() }

func (m *Manager) run(e *entry) {
	started := false
	m.update(e, func(j *Job) {
		if j.Status != Queued {
			return
		}
		j.Status = Running
		j.StartedAt = time.Now().UnixMilli()
		started = true
	})
	if !started {
		return
	}
	var result any
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("internal error: %v", r)
				m.log.Error("job panicked", "kind", e.job.Kind, "panic", r)
			}
		}()
		result, err = e.fn(e.ctx, func(p any, msg string) {
			m.update(e, func(j *Job) {
				j.Progress = p
				if msg != "" {
					j.Message = msg
				}
			})
		})
	}()
	m.update(e, func(j *Job) {
		j.FinishedAt = time.Now().UnixMilli()
		j.Result = result
		switch {
		case err == nil && e.ctx.Err() != nil:
			j.Status = Cancelled
		case err == nil:
			j.Status = Done
		case errors.Is(err, context.Canceled):
			j.Status = Cancelled
		default:
			j.Status = Failed
			j.Error = err.Error()
		}
	})
	e.cancel()
	if err != nil && !errors.Is(err, context.Canceled) {
		m.log.Warn("job failed", "kind", e.job.Kind, "err", err)
	}
}

func (m *Manager) update(e *entry, fn func(*Job)) {
	m.mu.Lock()
	fn(&e.job)
	snap := e.job
	m.mu.Unlock()
	m.events.Publish(events.Event{Type: "job", Data: snap})
}

// Submit queues a job on the main lane and returns its snapshot.
func (m *Manager) Submit(kind, title string, fn Func) Job {
	return m.SubmitTo(MainLane, kind, title, fn)
}

// SubmitTo queues a job on a lane; lanes run beside each other.
func (m *Manager) SubmitTo(lane, kind, title string, fn Func) Job {
	var b [6]byte
	rand.Read(b[:])
	ctx, cancel := context.WithCancel(context.Background())
	e := &entry{
		job:  Job{ID: hex.EncodeToString(b[:]), Kind: kind, Title: title, Status: Queued, CreatedAt: time.Now().UnixMilli()},
		lane: lane, fn: fn, ctx: ctx, cancel: cancel,
	}
	m.mu.Lock()
	m.entries[e.job.ID] = e
	m.order = append(m.order, e.job.ID)
	m.prune()
	snap := e.job
	q := m.lane(lane)
	m.mu.Unlock()
	m.events.Publish(events.Event{Type: "job", Data: snap})
	q <- e
	return snap
}

// prune drops the oldest finished jobs beyond maxHistory (mu held).
func (m *Manager) prune() {
	for len(m.order) > maxHistory {
		dropped := false
		for i, id := range m.order {
			st := m.entries[id].job.Status
			if st == Done || st == Failed || st == Cancelled {
				delete(m.entries, id)
				m.order = append(m.order[:i], m.order[i+1:]...)
				dropped = true
				break
			}
		}
		if !dropped {
			return
		}
	}
}

// Get returns a job snapshot.
func (m *Manager) Get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok {
		return Job{}, false
	}
	return e.job, true
}

// List returns job snapshots, newest first.
func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		out = append(out, m.entries[m.order[i]].job)
	}
	return out
}

// Cancel requests cancellation of a queued or running job.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	e, ok := m.entries[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	e.cancel()
	m.update(e, func(j *Job) {
		if j.Status == Queued {
			j.Status = Cancelled
			j.FinishedAt = time.Now().UnixMilli()
		}
	})
	return true
}

// Busy reports whether any job is queued or running (on the given lanes,
// or on any lane when none are given).
func (m *Manager) Busy(lanes ...string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		if e.job.Status != Queued && e.job.Status != Running {
			continue
		}
		if len(lanes) == 0 {
			return true
		}
		for _, l := range lanes {
			if e.lane == l {
				return true
			}
		}
	}
	return false
}
