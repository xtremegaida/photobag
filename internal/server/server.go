// Package server exposes a bag over a local HTTP JSON API and serves the
// embedded web UI.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"photobag/internal/analysis"
	"photobag/internal/backup"
	"photobag/internal/bag"
	"photobag/internal/dedup"
	"photobag/internal/events"
	"photobag/internal/experiments"
	"photobag/internal/importer"
	"photobag/internal/jobs"
	"photobag/internal/scoring"
	"photobag/internal/tagger"
)

// Config configures the server.
type Config struct {
	// Addr is the listen address (default 127.0.0.1:7474).
	Addr string
	// Dev disables the embedded UI (use the Vite dev server) and trusts
	// its origin.
	Dev bool
	// AllowRemote accepts any Host header (LAN access). The token is then
	// the only protection, so keep it enabled.
	AllowRemote bool
	// NoToken disables the per-launch access token.
	NoToken bool
	// KeyStore is the file holding model API keys (default: in the user's
	// configuration directory).
	KeyStore string
	// TaggerDir is the local tagger installation (default
	// tagger.DefaultDir()).
	TaggerDir string
	Logger    *slog.Logger
}

// DefaultAddr is where serve listens by default.
const DefaultAddr = "127.0.0.1:7474"

// Server serves one bag.
type Server struct {
	b       *bag.Bag
	cfg     Config
	log     *slog.Logger
	token   string
	events  *events.Broker
	jobs    *jobs.Manager
	scoring *scoring.Service
	scans   *dedup.Store
	preview *previewCache
	simSort *orderCache
	keys    *analysis.KeyStore
	tagger  *tagger.Local

	// Image generation: node definitions from ComfyUI, the running job of
	// each experiment, and the latest sampler previews.
	nodeInfo nodeInfoCache
	genMu    sync.Mutex
	genJobs  map[int64]string
	previews samplerPreviews

	ctx    context.Context
	cancel context.CancelFunc
	ln     net.Listener
	port   int

	backupsMu sync.Mutex
	backups   map[string]*pendingBackup
}

type pendingBackup struct {
	path     string
	filename string
	expires  time.Time
}

// New creates a server for b.
func New(b *bag.Bag, cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.KeyStore == "" {
		cfg.KeyStore = analysis.DefaultKeyStorePath()
	}
	if cfg.TaggerDir == "" {
		cfg.TaggerDir = tagger.DefaultDir()
	}
	ctx, cancel := context.WithCancel(context.Background())
	ev := events.New()
	var tok [16]byte
	rand.Read(tok[:])
	return &Server{
		b: b, cfg: cfg, log: cfg.Logger,
		token:   hex.EncodeToString(tok[:]),
		events:  ev,
		jobs:    jobs.NewManager(ctx, ev, cfg.Logger),
		scoring: scoring.New(b),
		scans:   dedup.NewStore(),
		preview: newPreviewCache(256 << 20),
		simSort: newOrderCache(4),
		keys:    &analysis.KeyStore{Path: cfg.KeyStore},
		tagger: &tagger.Local{Dir: cfg.TaggerDir, IdleTimeout: 10 * time.Minute, Log: cfg.Logger,
			OnChange: func() { ev.Changed("tagger") }},
		ctx: ctx, cancel: cancel,
		backups: map[string]*pendingBackup{},
		genJobs: map[int64]string{},
	}
}

// Token returns the per-launch access token.
func (s *Server) Token() string { return s.token }

// Listen binds the address. When the default port is taken it tries the
// next few ports.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil && s.cfg.Addr == DefaultAddr {
		host, portStr, _ := net.SplitHostPort(DefaultAddr)
		p, _ := strconv.Atoi(portStr)
		for i := 1; i <= 10 && err != nil; i++ {
			ln, err = net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p+i)))
		}
	}
	if err != nil {
		return err
	}
	s.ln = ln
	s.port = ln.Addr().(*net.TCPAddr).Port
	return nil
}

// URL is the address to open in a browser (including the token).
func (s *Server) URL() string {
	host, _, _ := net.SplitHostPort(s.ln.Addr().String())
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
		host = "127.0.0.1"
	}
	port := s.port
	if s.cfg.Dev {
		port = 5173
		host = "localhost"
	}
	u := fmt.Sprintf("http://%s/", net.JoinHostPort(host, strconv.Itoa(port)))
	if !s.cfg.NoToken {
		u += "?token=" + s.token
	}
	return u
}

// Serve runs until ctx is cancelled, then shuts down gracefully: running
// jobs are cancelled and temporary backups removed. The caller closes the
// bag afterwards.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}
	backup.CleanStale(s.b.Path)
	if err := experiments.RecoverRuns(ctx, s.b); err != nil {
		s.log.Warn("could not tidy up interrupted generations", "err", err)
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: event streams and large downloads are long-lived.
	}
	go s.watchExternalChanges()
	go s.idleCheckpoint()
	s.refreshIfOutdated()
	go s.expireBackups()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(s.ln) }()
	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	s.cancel() // stop jobs and background loops, end event streams
	shutCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_ = srv.Shutdown(shutCtx)
	s.jobs.Wait()
	s.tagger.Close()
	s.cleanupBackups()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// watchExternalChanges notices commits by other processes (for example a
// CLI import into the same bag) and tells clients to refresh.
func (s *Server) watchExternalChanges() {
	if s.b.Journal != bag.JournalWAL {
		return // exclusive locking: no other process can write
	}
	conn, err := s.b.R.Conn(s.ctx)
	if err != nil {
		return
	}
	defer conn.Close()
	var last int64 = -1
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
		var v int64
		if err := conn.QueryRowContext(s.ctx, "PRAGMA data_version").Scan(&v); err != nil {
			continue
		}
		if last >= 0 && v != last && time.Since(s.b.LastLocalWrite()) > 3*time.Second && !s.jobs.Busy(jobs.MainLane) {
			s.events.Changed("all")
		}
		last = v
	}
}

// refreshIfOutdated queues a job regenerating thumbnails/thumbprints that
// are missing or were made by an older thumbprint version.
func (s *Server) refreshIfOutdated() {
	n, err := importer.Outdated(s.ctx, s.b)
	if err != nil || n == 0 {
		return
	}
	s.jobs.Submit("refresh", fmt.Sprintf("Update thumbnails and thumbprints (%d)", n), func(ctx context.Context, report func(any, string)) (any, error) {
		rep, err := importer.Refresh(ctx, s.b, func(done, total int) {
			report(map[string]int{"done": done, "total": total}, "")
		})
		s.events.Changed("images", "dedup")
		return rep, err
	})
}

// idleCheckpoint keeps the main bag file current: a few seconds after the
// last write it folds the WAL back in, so copying the .photobag file while
// the server runs still captures recent changes.
func (s *Server) idleCheckpoint() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	var done time.Time
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
		last := s.b.LastLocalWrite()
		if last.After(done) && time.Since(last) > 5*time.Second && !s.jobs.Busy(jobs.MainLane) {
			s.b.CheckpointTruncate(s.ctx)
			done = last
		}
	}
}

func (s *Server) expireBackups() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
		s.backupsMu.Lock()
		for tok, p := range s.backups {
			if time.Now().After(p.expires) {
				os.Remove(p.path)
				delete(s.backups, tok)
			}
		}
		s.backupsMu.Unlock()
	}
}

func (s *Server) cleanupBackups() {
	s.backupsMu.Lock()
	defer s.backupsMu.Unlock()
	for tok, p := range s.backups {
		os.Remove(p.path)
		delete(s.backups, tok)
	}
	backup.CleanStale(s.b.Path)
}
