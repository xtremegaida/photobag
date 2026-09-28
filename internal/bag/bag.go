// Package bag owns the PhotoBag SQLite container: creating, opening,
// migrating and closing a bag file, plus the connection pools and
// transaction helpers every other package uses.
package bag

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"photobag/internal/sysutil"
)

// ApplicationID marks a SQLite file as a PhotoBag ("PHBG").
const ApplicationID = 0x50484247

// PageSize is applied when a bag is created. Larger pages suit blob-heavy
// files without making small commits (one comparison) expensive.
const PageSize = 16384

// Journal modes.
const (
	JournalAuto   = "auto"
	JournalWAL    = "wal"
	JournalDelete = "delete"
)

// ErrNotABag is returned when the file is a SQLite database that belongs to
// some other application.
var ErrNotABag = errors.New("file is a SQLite database but not a PhotoBag")

// Options control how a bag is opened.
type Options struct {
	// Journal is "auto" (WAL, or DELETE on network filesystems), "wal" or "delete".
	Journal string
	// NoMigrationBackup skips the VACUUM INTO safety copy taken before
	// migrating an existing bag to a newer schema.
	NoMigrationBackup bool
	Logger            *slog.Logger
}

// Bag is an open PhotoBag file.
type Bag struct {
	Path    string
	Journal string // effective journal mode: "wal" or "delete"

	// W is the writer pool (a single connection, BEGIN IMMEDIATE).
	W *sql.DB
	// R is the reader pool. In DELETE/exclusive mode it is the same pool as W,
	// so code must never hold rows open on R while writing.
	R *sql.DB

	log       *slog.Logger
	lastWrite atomic.Int64
	created   bool
}

// Open opens (or creates) the bag at path.
func Open(path string, opts Options) (*Bag, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	if st, err := os.Stat(abs); err == nil && st.IsDir() {
		return nil, fmt.Errorf("%s is a directory", abs)
	}
	if dir := filepath.Dir(abs); dir != "" {
		if _, err := os.Stat(dir); err != nil {
			return nil, fmt.Errorf("bag folder: %w", err)
		}
	}

	journal := strings.ToLower(opts.Journal)
	switch journal {
	case "", JournalAuto:
		journal = JournalWAL
		if sysutil.IsNetworkPath(abs) {
			log.Warn("bag is on a network filesystem; using DELETE journal with exclusive locking", "path", abs)
			journal = JournalDelete
		}
	case JournalWAL, JournalDelete:
	default:
		return nil, fmt.Errorf("unknown journal mode %q (want auto, wal or delete)", opts.Journal)
	}

	b := &Bag{Path: abs, Journal: journal, log: log}
	b.W, err = sql.Open("sqlite", b.dsn(false))
	if err != nil {
		return nil, err
	}
	b.W.SetMaxOpenConns(1)
	b.W.SetMaxIdleConns(1)
	b.W.SetConnMaxLifetime(0)
	b.W.SetConnMaxIdleTime(0)

	if err := b.init(opts); err != nil {
		b.W.Close()
		return nil, err
	}

	if journal == JournalDelete {
		b.R = b.W
	} else {
		b.R, err = sql.Open("sqlite", b.dsn(true))
		if err != nil {
			b.W.Close()
			return nil, err
		}
		b.R.SetMaxOpenConns(8)
		b.R.SetMaxIdleConns(8)
		if err := b.R.Ping(); err != nil {
			b.Close()
			return nil, err
		}
	}
	return b, nil
}

func (b *Bag) dsn(reader bool) string {
	q := []string{
		"_pragma=busy_timeout(10000)",
		"_pragma=foreign_keys(1)",
		"_pragma=journal_size_limit(67108864)",
	}
	if b.Journal == JournalWAL {
		q = append(q, "_pragma=synchronous(NORMAL)")
	} else {
		q = append(q, "_pragma=synchronous(FULL)")
	}
	if reader {
		q = append(q, "_pragma=query_only(1)")
	} else {
		q = append(q, "_txlock=immediate")
	}
	return b.Path + "?" + strings.Join(q, "&")
}

// init inspects the file, creates or migrates the schema and applies the
// journal mode. It runs on the writer connection only.
func (b *Bag) init(opts Options) error {
	ctx := context.Background()
	var appID, version, objects int64
	if err := b.W.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appID); err != nil {
		if strings.Contains(err.Error(), "file is not a database") {
			return fmt.Errorf("%s: not a SQLite database", b.Path)
		}
		return fmt.Errorf("reading %s: %w", b.Path, err)
	}
	if err := b.W.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if err := b.W.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&objects); err != nil {
		return err
	}

	latest := LatestVersion()
	switch {
	case objects == 0:
		// Brand new (or empty) file: layout pragmas must precede the first table.
		b.created = true
		for _, p := range []string{
			fmt.Sprintf("PRAGMA page_size = %d", PageSize),
			"PRAGMA auto_vacuum = INCREMENTAL",
			"PRAGMA journal_mode = DELETE", // page_size cannot change under WAL
		} {
			if _, err := b.W.ExecContext(ctx, p); err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
		}
		if err := b.migrate(ctx, 0); err != nil {
			return err
		}
		if _, err := b.W.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id = %d", ApplicationID)); err != nil {
			return err
		}
		if err := b.initMeta(ctx); err != nil {
			return err
		}
	case appID != ApplicationID:
		return fmt.Errorf("%s: %w (application_id %#x)", b.Path, ErrNotABag, appID)
	case version > int64(latest):
		return fmt.Errorf("%s was written by a newer PhotoBag (schema v%d, this build supports v%d)", b.Path, version, latest)
	case version < int64(latest):
		if !opts.NoMigrationBackup {
			dest := fmt.Sprintf("%s.pre-v%d.bak", b.Path, latest)
			if _, err := os.Stat(dest); err == nil {
				dest = fmt.Sprintf("%s.pre-v%d-%d.bak", b.Path, latest, time.Now().Unix())
			}
			b.log.Info("backing up bag before schema migration", "to", dest)
			if _, err := b.W.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
				return fmt.Errorf("pre-migration backup: %w", err)
			}
		}
		if err := b.migrate(ctx, int(version)); err != nil {
			return err
		}
	}

	if b.Journal == JournalWAL {
		var mode string
		if err := b.W.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
			return err
		}
		if !strings.EqualFold(mode, "wal") {
			return fmt.Errorf("could not enable WAL (got %q); try --journal delete", mode)
		}
	} else {
		var mode string
		if err := b.W.QueryRowContext(ctx, "PRAGMA journal_mode = DELETE").Scan(&mode); err != nil {
			return err
		}
		if _, err := b.W.ExecContext(ctx, "PRAGMA locking_mode = EXCLUSIVE"); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bag) initMeta(ctx context.Context) error {
	name := strings.TrimSuffix(filepath.Base(b.Path), filepath.Ext(b.Path))
	_, err := b.W.ExecContext(ctx,
		`INSERT OR IGNORE INTO meta(key, value) VALUES ('name', ?), ('created_at', ?)`,
		name, fmt.Sprint(NowMillis()))
	return err
}

// Created reports whether Open created a brand new bag.
func (b *Bag) Created() bool { return b.created }

// Close checkpoints the WAL (so the bag is left as a single file) and closes
// all connections.
func (b *Bag) Close() error {
	var errs []error
	if b.R != nil && b.R != b.W {
		errs = append(errs, b.R.Close())
	}
	if b.W != nil {
		if b.Journal == JournalWAL {
			if _, err := b.W.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
				errs = append(errs, fmt.Errorf("checkpoint: %w", err))
			}
		}
		errs = append(errs, b.W.Close())
	}
	return errors.Join(errs...)
}

// Tx runs fn in an immediate write transaction on the writer connection.
// Everything inside fn must use tx, never b.R or b.W.
func (b *Bag) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := b.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	b.lastWrite.Store(time.Now().UnixMilli())
	return nil
}

// LastLocalWrite returns when this process last committed a write.
func (b *Bag) LastLocalWrite() time.Time {
	return time.UnixMilli(b.lastWrite.Load())
}

// Checkpoint runs a passive WAL checkpoint (no-op in DELETE mode).
func (b *Bag) Checkpoint(ctx context.Context) {
	if b.Journal != JournalWAL {
		return
	}
	if _, err := b.W.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
		b.log.Debug("checkpoint failed", "err", err)
	}
}

// CheckpointTruncate folds the WAL back into the main file and truncates
// it, so a copy of the bag file alone is up to date. It is best effort:
// with active readers the checkpoint may be partial.
func (b *Bag) CheckpointTruncate(ctx context.Context) {
	if b.Journal != JournalWAL {
		return
	}
	if _, err := b.W.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		b.log.Debug("checkpoint failed", "err", err)
	}
}

// OpenAux opens an extra standalone connection pool for long-running,
// read-mostly work such as VACUUM INTO, so it never blocks the writer.
func (b *Bag) OpenAux() (*sql.DB, error) {
	if b.Journal == JournalDelete {
		// Exclusive locking: a second connection could not read the file.
		return nil, errors.New("not available in DELETE/exclusive journal mode")
	}
	db, err := sql.Open("sqlite", b.Path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Meta returns a meta value ("" when unset).
func (b *Bag) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := b.R.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetMeta stores a meta value.
func (b *Bag) SetMeta(ctx context.Context, key, value string) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO meta(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			key, value)
		return err
	})
}

// FileStats describes the on-disk size of the bag.
type FileStats struct {
	PageSize      int64  `json:"pageSize"`
	PageCount     int64  `json:"pageCount"`
	FreePages     int64  `json:"freePages"`
	SizeBytes     int64  `json:"sizeBytes"`
	FreeBytes     int64  `json:"freeBytes"`
	JournalMode   string `json:"journalMode"`
	SchemaVersion int64  `json:"schemaVersion"`
}

// Stats reads page-level statistics.
func (b *Bag) Stats(ctx context.Context) (FileStats, error) {
	var s FileStats
	row := b.R.QueryRowContext(ctx, `SELECT
		(SELECT page_size FROM pragma_page_size),
		(SELECT page_count FROM pragma_page_count),
		(SELECT freelist_count FROM pragma_freelist_count),
		(SELECT user_version FROM pragma_user_version)`)
	if err := row.Scan(&s.PageSize, &s.PageCount, &s.FreePages, &s.SchemaVersion); err != nil {
		return s, err
	}
	s.SizeBytes = s.PageSize * s.PageCount
	s.FreeBytes = s.PageSize * s.FreePages
	s.JournalMode = b.Journal
	return s, nil
}

// NowMillis is the timestamp format used throughout the schema.
func NowMillis() int64 { return time.Now().UnixMilli() }
