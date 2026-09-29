package tagger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Local runs the tagger installed in Dir: it starts the server on a free
// local port when first needed and stops it after IdleTimeout without
// requests, freeing the GPU memory the model holds.
type Local struct {
	Dir string
	// IdleTimeout stops the server after this long unused (0: never).
	IdleTimeout time.Duration
	// StartTimeout bounds loading the model (default 5 minutes).
	StartTimeout time.Duration
	// OnChange is called when the server starts or stops.
	OnChange func()
	Log      *slog.Logger

	mu       sync.Mutex
	proc     *process // running and healthy
	pending  *process // starting
	starting chan struct{}
	startErr error
	failedAt time.Time
	inflight int
	lastUse  time.Time
	lastErr  string
	closed   bool
}

type process struct {
	cmd      *exec.Cmd
	port     int
	client   *Client
	logs     *lineLog
	done     chan struct{}
	exitErr  error
	health   *Health
	started  time.Time
	stopping bool
	sys      procHandle
}

// Status reports the installation and the server.
func (l *Local) Status() LocalStatus {
	st := LocalStatus{Installation: Detect(l.Dir), State: "stopped", Providers: []string{}, IdleMinutes: int(l.IdleTimeout / time.Minute)}
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.proc != nil:
		st.State, st.Port, st.StartedAt = "running", l.proc.port, l.proc.started.UnixMilli()
		if h := l.proc.health; h != nil {
			st.OnGPU, st.Providers = h.OnGPU(), h.Providers
		}
	case l.starting != nil:
		st.State = "starting"
	}
	st.LastError = l.lastErr
	return st
}

// Name is the installed model's name.
func (l *Local) Name() string { return Detect(l.Dir).ModelName() }

// Tag starts the server if needed and tags an image.
func (l *Local) Tag(ctx context.Context, img []byte, o Options) (*Result, error) {
	c, release, err := l.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return c.Tag(ctx, img, o)
}

// Health starts the server if needed and reports on it.
func (l *Local) Health(ctx context.Context) (*Health, error) {
	c, release, err := l.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return c.Health(ctx)
}

// Stop stops the server (it starts again when needed).
func (l *Local) Stop() {
	l.mu.Lock()
	ps := []*process{l.proc, l.pending}
	l.proc = nil
	for _, p := range ps {
		if p != nil {
			p.stopping = true
		}
	}
	l.mu.Unlock()
	stopped := false
	for _, p := range ps {
		if p != nil {
			p.terminate()
			stopped = true
		}
	}
	if stopped {
		l.changed()
	}
}

// Close stops the server for good.
func (l *Local) Close() {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	l.Stop()
}

func (l *Local) changed() {
	if l.OnChange != nil {
		l.OnChange()
	}
}

func (l *Local) logger() *slog.Logger {
	if l.Log != nil {
		return l.Log
	}
	return slog.Default()
}

var errStopped = errors.New("the local tagger was stopped while starting")

// acquire returns a client for the running server, starting it first if
// needed. release must be called when the request is done.
func (l *Local) acquire(ctx context.Context) (*Client, func(), error) {
	for {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return nil, nil, errors.New("the local tagger has been shut down")
		}
		if p := l.proc; p != nil {
			l.inflight++
			l.lastUse = time.Now()
			l.mu.Unlock()
			return p.client, func() {
				l.mu.Lock()
				l.inflight--
				l.lastUse = time.Now()
				l.mu.Unlock()
			}, nil
		}
		if l.starting == nil {
			// A start that just failed fails again: report it rather than
			// retrying for every image.
			if l.startErr != nil && time.Since(l.failedAt) < 15*time.Second {
				err := l.startErr
				l.mu.Unlock()
				return nil, nil, err
			}
			l.starting = make(chan struct{})
			go l.start(l.starting)
		}
		ch := l.starting
		l.mu.Unlock()
		select {
		case <-ch:
			l.mu.Lock()
			err := l.startErr
			ok := l.proc != nil
			l.mu.Unlock()
			if !ok && err != nil {
				return nil, nil, err
			}
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
}

// start launches the server and waits until it answers.
func (l *Local) start(ch chan struct{}) {
	l.changed() // now "starting"
	p, err := l.launch()
	l.mu.Lock()
	l.pending = nil
	l.starting = nil
	if err == nil && l.closed {
		err = errors.New("the local tagger has been shut down")
		go p.terminate()
	}
	if errors.Is(err, errStopped) {
		l.mu.Unlock()
		close(ch)
		return
	}
	if err != nil {
		l.startErr, l.failedAt, l.lastErr = err, time.Now(), err.Error()
		l.mu.Unlock()
		close(ch)
		l.logger().Warn("local tagger did not start", "err", err)
		l.changed()
		return
	}
	l.proc, l.startErr, l.lastErr, l.lastUse = p, nil, "", time.Now()
	l.mu.Unlock()
	close(ch)
	l.logger().Info("local tagger started", "port", p.port, "gpu", p.health.OnGPU())
	l.changed()
	go l.watch(p)
}

func (l *Local) launch() (*process, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	cmd, err := Command(l.Dir, "127.0.0.1", port)
	if err != nil {
		return nil, err
	}
	logs := &lineLog{max: 40}
	if f, err := os.Create(filepath.Join(l.Dir, logFile)); err == nil {
		logs.file = f
	}
	cmd.Stdout, cmd.Stderr = logs, logs
	prepareCmd(cmd)
	p := &process{cmd: cmd, port: port, logs: logs, done: make(chan struct{})}
	p.client = New("http://127.0.0.1:"+strconv.Itoa(port), 5*time.Minute)
	if err := cmd.Start(); err != nil {
		logs.close()
		return nil, fmt.Errorf("starting the tagger: %w", err)
	}
	p.sys = afterStart(cmd)
	go func() {
		p.exitErr = cmd.Wait()
		p.sys.release()
		logs.close()
		close(p.done)
		l.exited(p)
	}()
	l.mu.Lock()
	l.pending = p
	l.mu.Unlock()

	timeout := l.StartTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-p.done:
			l.mu.Lock()
			stopped := p.stopping
			l.mu.Unlock()
			if stopped {
				return nil, errStopped
			}
			return nil, fmt.Errorf("the local tagger stopped while starting (%v). Its output:\n%s", p.exitErr, logs.tail(15))
		case <-time.After(400 * time.Millisecond):
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		h, err := p.client.healthOnce(ctx)
		cancel()
		if err == nil {
			p.health, p.started = h, time.Now()
			return p, nil
		}
		if time.Now().After(deadline) {
			p.terminate()
			return nil, fmt.Errorf("the local tagger did not answer within %s. Its output:\n%s", timeout, logs.tail(15))
		}
	}
}

// Command runs the installed server in dir on host:port.
func Command(dir, host string, port int) (*exec.Cmd, error) {
	in := Detect(dir)
	if !in.Ready {
		return nil, errors.New(in.Problem)
	}
	if err := updateScript(dir); err != nil {
		return nil, err
	}
	info, _ := readInfo(dir)
	device := "auto" // a GPU that fails falls back to the CPU, and /health says so
	if info.Device == "cpu" {
		device = "cpu"
	}
	cmd := exec.Command(VenvPython(dir), "-u", ScriptFile, "--host", host, "--port", strconv.Itoa(port), "--device", device)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"MODEL_PATH="+filepath.Join(dir, ModelFile),
		"TAGS_PATH="+filepath.Join(dir, TagsFile),
		"MODEL_NAME="+in.ModelName(),
		"PYTHONUNBUFFERED=1",
		"PYTHONIOENCODING=utf-8",
	)
	if info.PipCUDA {
		cmd.Env = append(cmd.Env, "ORT_PRELOAD_DLLS=1")
	}
	return cmd, nil
}

// exited notes that a server process ended.
func (l *Local) exited(p *process) {
	l.mu.Lock()
	if l.proc != p {
		l.mu.Unlock()
		return
	}
	l.proc = nil
	if !p.stopping {
		l.lastErr = fmt.Sprintf("the local tagger stopped unexpectedly (%v). Its output:\n%s", p.exitErr, p.logs.tail(15))
		l.logger().Warn("local tagger stopped", "err", p.exitErr)
	}
	l.mu.Unlock()
	l.changed()
}

// watch stops an idle server.
func (l *Local) watch(p *process) {
	if l.IdleTimeout <= 0 {
		return
	}
	t := time.NewTicker(min(l.IdleTimeout/4, 15*time.Second))
	defer t.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-t.C:
		}
		l.mu.Lock()
		idle := l.proc == p && l.inflight == 0 && time.Since(l.lastUse) > l.IdleTimeout
		if idle {
			l.proc = nil
			p.stopping = true
		}
		l.mu.Unlock()
		if idle {
			l.logger().Info("stopping the idle local tagger")
			p.terminate()
			l.changed()
			return
		}
	}
}

// terminate stops the process and waits for it (briefly).
func (p *process) terminate() {
	select {
	case <-p.done:
		return
	default:
	}
	stopProcess(p.cmd, p.sys, p.done)
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
	}
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// lineLog keeps the last lines a process printed, and copies everything
// to a file.
type lineLog struct {
	mu      sync.Mutex
	max     int
	lines   []string
	partial []byte
	file    io.WriteCloser
}

func (w *lineLog) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		w.file.Write(b)
	}
	w.partial = append(w.partial, b...)
	for {
		i := strings.IndexAny(string(w.partial), "\r\n")
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(string(w.partial[:i])); line != "" {
			w.lines = append(w.lines, line)
			if len(w.lines) > w.max {
				w.lines = w.lines[len(w.lines)-w.max:]
			}
		}
		w.partial = w.partial[i+1:]
	}
	return len(b), nil
}

func (w *lineLog) tail(n int) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	lines := w.lines
	if p := strings.TrimSpace(string(w.partial)); p != "" {
		lines = append(lines[:len(lines):len(lines)], p)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 0 {
		return "(nothing)"
	}
	return "  " + strings.Join(lines, "\n  ")
}

func (w *lineLog) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
}
