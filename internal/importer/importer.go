// Package importer walks a folder and adds its images to a bag: a pool of
// workers hashes, decodes and fingerprints files under a memory budget while
// a single writer batches the results into transactions.
package importer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"photobag/internal/bag"
	"photobag/internal/imaging"
	"photobag/internal/library"
	"photobag/internal/sysutil"
)

// MaxFileSize keeps originals below SQLite's default 1e9-byte blob limit.
const MaxFileSize = 900 << 20

// Options control an import.
type Options struct {
	Recursive bool     `json:"recursive"`
	Tags      []string `json:"tags,omitempty"`
	// TagFolders turns each directory component of a file's path (relative
	// to the import root) into a tag.
	TagFolders bool `json:"tagFolders,omitempty"`
	// SkipIdentical skips files whose bytes already belong to an active image.
	SkipIdentical bool `json:"skipIdentical,omitempty"`
	// IncludeRemoved imports files matching trashed/purged images, or
	// originals that re-encoding replaced, instead of skipping them.
	IncludeRemoved bool `json:"includeRemoved,omitempty"`

	Workers      int   `json:"-"`
	MemoryBudget int64 `json:"-"`
}

// Progress is reported while importing.
type Progress struct {
	Phase   string `json:"phase"` // scanning | importing | done
	Found   int    `json:"found"`
	Done    int    `json:"done"`
	Added   int    `json:"added"`
	Skipped int    `json:"skipped"`
	Failed  int    `json:"failed"`
	Current string `json:"current,omitempty"`
}

// FileReport explains a skipped or failed file.
type FileReport struct {
	Path   string `json:"path"`
	Status string `json:"status"` // skipped | failed
	Reason string `json:"reason"`
}

// Report summarises a finished import.
type Report struct {
	ImportID  int64        `json:"importId"`
	Source    string       `json:"source"`
	Found     int          `json:"found"`
	Added     int          `json:"added"`
	Skipped   int          `json:"skipped"`
	Failed    int          `json:"failed"`
	Cancelled bool         `json:"cancelled,omitempty"`
	Files     []FileReport `json:"files"`
	Truncated bool         `json:"truncated,omitempty"`
	Millis    int64        `json:"millis"`
}

const maxReportFiles = 5000

type file struct {
	path  string
	rel   string // slash-separated, relative to root
	size  int64
	mtime int64
}

// item is a processed file on its way to the writer.
type item struct {
	f      file
	sha    [32]byte
	data   []byte // nil when the blob already exists / is written by another item
	res    *imaging.Result
	format imaging.Format
	width  int
	height int
	meta   imaging.Meta
	mem    int64 // budget to release after commit
}

// claim coordinates workers that meet the same bytes in one run.
type claim struct {
	done chan struct{}
	err  error
}

type run struct {
	b        *bag.Bag
	opts     Options
	root     string
	sem      *weighted
	progress func(Progress)

	blobs   map[[32]byte]bool // blobs already in the bag
	active  map[[32]byte]bool // shas of active images (SkipIdentical)
	removed map[[32]byte]bool // shas only held by trashed/purged images
	// reencoded holds the shas of originals that re-encodes replaced.
	reencoded map[[32]byte]bool

	mu       sync.Mutex
	claims   map[[32]byte]*claim
	prog     Progress
	lastEmit time.Time
	report   Report
	importID int64
	tagIDs   []int64
	tagCache map[string]int64
	writeErr error
}

// Run imports root (a folder, or a single file) into b.
func Run(ctx context.Context, b *bag.Bag, root string, opts Options, progress func(Progress)) (*Report, error) {
	start := time.Now()
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if opts.Workers <= 0 {
		opts.Workers = max(1, runtime.NumCPU()-1)
	}
	if opts.MemoryBudget <= 0 {
		opts.MemoryBudget = 1536 << 20
	}
	if progress == nil {
		progress = func(Progress) {}
	}
	r := &run{
		b: b, opts: opts, root: abs, progress: progress,
		sem:      newWeighted(opts.MemoryBudget),
		claims:   map[[32]byte]*claim{},
		tagCache: map[string]int64{},
		report:   Report{Source: abs, Files: []FileReport{}},
	}

	// Scan.
	r.setPhase("scanning")
	var files []file
	if st.IsDir() {
		files, err = r.walk(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		files = []file{{path: abs, rel: filepath.Base(abs), size: st.Size(), mtime: st.ModTime().UnixMilli()}}
		r.root = filepath.Dir(abs)
	}
	r.mu.Lock()
	r.prog.Found = len(files)
	r.report.Found = len(files)
	r.mu.Unlock()

	if err := r.loadState(ctx); err != nil {
		return nil, err
	}
	if err := r.begin(ctx); err != nil {
		return nil, err
	}

	// Import.
	r.setPhase("importing")
	jobs := make(chan file)
	items := make(chan *item, opts.Workers*2)
	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				r.process(ctx, f, items)
			}
		}()
	}
	writerDone := make(chan struct{})
	go func() {
		r.writer(items)
		close(writerDone)
	}()
feed:
	for _, f := range files {
		select {
		case jobs <- f:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	close(items)
	<-writerDone

	r.report.Cancelled = ctx.Err() != nil
	r.report.Millis = time.Since(start).Milliseconds()
	if err := r.finish(); err != nil && r.writeErr == nil {
		r.writeErr = err
	}
	r.setPhase("done")
	if r.writeErr != nil {
		return &r.report, r.writeErr
	}
	return &r.report, nil
}

func (r *run) walk(ctx context.Context) ([]file, error) {
	var files []file
	err := filepath.WalkDir(r.root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if path == r.root {
				return err
			}
			r.addFile(FileReport{Path: path, Status: "failed", Reason: err.Error()})
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if path != r.root {
			if strings.HasPrefix(name, ".") || sysutil.IsHidden(d) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			if path != r.root && (!r.opts.Recursive || sysutil.Junk(name, true)) {
				return fs.SkipDir
			}
			return nil
		}
		if sysutil.Junk(name, false) {
			return nil
		}
		info, err := os.Stat(path) // follows symlinks
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(r.root, path)
		files = append(files, file{path: path, rel: filepath.ToSlash(rel), size: info.Size(), mtime: info.ModTime().UnixMilli()})
		if len(files)%500 == 0 {
			r.mu.Lock()
			r.prog.Found = len(files)
			r.mu.Unlock()
			r.emit(false)
		}
		return nil
	})
	return files, err
}

func (r *run) loadState(ctx context.Context) error {
	load := func(q string) (map[[32]byte]bool, error) {
		rows, err := r.b.R.QueryContext(ctx, q)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		m := map[[32]byte]bool{}
		for rows.Next() {
			var sha []byte
			if err := rows.Scan(&sha); err != nil {
				return nil, err
			}
			if len(sha) == 32 {
				m[[32]byte(sha)] = true
			}
		}
		return m, rows.Err()
	}
	var err error
	if r.blobs, err = load("SELECT sha256 FROM blobs"); err != nil {
		return err
	}
	if r.opts.SkipIdentical {
		if r.active, err = load("SELECT DISTINCT sha256 FROM images WHERE deleted_at IS NULL AND purged_at IS NULL"); err != nil {
			return err
		}
	}
	if !r.opts.IncludeRemoved {
		if r.removed, err = load(`SELECT DISTINCT sha256 FROM images WHERE (deleted_at IS NOT NULL OR purged_at IS NOT NULL)
			AND sha256 NOT IN (SELECT sha256 FROM images WHERE deleted_at IS NULL AND purged_at IS NULL)`); err != nil {
			return err
		}
		if r.reencoded, err = load(`SELECT DISTINCT old_sha256 FROM image_reencodes
			WHERE old_sha256 NOT IN (SELECT sha256 FROM images WHERE deleted_at IS NULL AND purged_at IS NULL)`); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) begin(ctx context.Context) error {
	optJSON, _ := json.Marshal(r.opts)
	return r.b.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "INSERT INTO imports(source, recursive, options_json, started_at) VALUES (?, ?, ?, ?)",
			r.report.Source, r.opts.Recursive, string(optJSON), bag.NowMillis())
		if err != nil {
			return err
		}
		r.importID, _ = res.LastInsertId()
		r.report.ImportID = r.importID
		if len(r.opts.Tags) > 0 {
			r.tagIDs, err = library.EnsureTags(ctx, tx, r.opts.Tags)
		}
		return err
	})
}

func (r *run) finish() error {
	ctx := context.Background()
	return r.b.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE imports SET finished_at = ?, added = ?, skipped = ?, failed = ? WHERE id = ?",
			bag.NowMillis(), r.report.Added, r.report.Skipped, r.report.Failed, r.importID)
		return err
	})
}

func (r *run) setPhase(p string) {
	r.mu.Lock()
	r.prog.Phase = p
	r.mu.Unlock()
	r.emit(true)
}

func (r *run) emit(force bool) {
	r.mu.Lock()
	if !force && time.Since(r.lastEmit) < 150*time.Millisecond {
		r.mu.Unlock()
		return
	}
	r.lastEmit = time.Now()
	p := r.prog
	r.mu.Unlock()
	r.progress(p)
}

func (r *run) addFile(fr FileReport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if fr.Status == "failed" {
		r.report.Failed++
		r.prog.Failed++
	} else {
		r.report.Skipped++
		r.prog.Skipped++
	}
	if len(r.report.Files) < maxReportFiles {
		r.report.Files = append(r.report.Files, fr)
	} else {
		r.report.Truncated = true
	}
}

func (r *run) fileDone(f file, fr *FileReport) {
	if fr != nil {
		fr.Path = f.path
		r.addFile(*fr)
	}
	r.mu.Lock()
	r.prog.Done++
	r.prog.Current = f.rel
	r.mu.Unlock()
	r.emit(false)
}

func skipped(reason string) *FileReport { return &FileReport{Status: "skipped", Reason: reason} }
func failed(reason string) *FileReport  { return &FileReport{Status: "failed", Reason: reason} }

// process handles one file on a worker goroutine.
func (r *run) process(ctx context.Context, f file, out chan<- *item) {
	if ctx.Err() != nil {
		return
	}
	it, fr, owned := r.prepare(ctx, f)
	if it != nil {
		// The writer counts it as added. Sending before releasing waiters on
		// the claim guarantees the blob row is written before items that
		// reference it.
		out <- it
	} else if fr != nil || ctx.Err() == nil {
		r.fileDone(f, fr)
	}
	if owned != nil {
		if it == nil && owned.err == nil {
			owned.err = errors.New("import of identical file was interrupted")
		}
		close(owned.done)
	}
}

// prepare reads, hashes and (for new content) processes one file. owned is
// non-nil when this worker claimed the file's bytes and must close it.
func (r *run) prepare(ctx context.Context, f file) (it *item, fr *FileReport, owned *claim) {
	fh, err := os.Open(f.path)
	if err != nil {
		return nil, failed(err.Error()), nil
	}
	defer fh.Close()
	head := make([]byte, imaging.SniffLen)
	n, _ := io.ReadFull(fh, head)
	format, reason := imaging.Sniff(head[:n], f.path)
	if format == "" {
		return nil, skipped(reason), nil
	}
	if f.size > MaxFileSize {
		return nil, failed(fmt.Sprintf("file is too large (%d MB)", f.size>>20)), nil
	}
	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		return nil, failed(err.Error()), nil
	}
	w, h, err := imaging.DecodeConfig(format, fh)
	if err != nil {
		return nil, failed(err.Error()), nil
	}
	need := min(f.size+int64(w)*int64(h)*4, r.opts.MemoryBudget)
	if err := r.sem.Acquire(ctx, need); err != nil {
		return nil, nil, nil
	}
	release := need
	defer func() { r.sem.Release(release) }()

	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		return nil, failed(err.Error()), nil
	}
	data := make([]byte, f.size)
	if _, err := io.ReadFull(fh, data); err != nil {
		return nil, failed("reading file: " + err.Error()), nil
	}
	sha := sha256.Sum256(data)

	r.mu.Lock()
	if r.opts.SkipIdentical && r.active[sha] {
		r.mu.Unlock()
		return nil, skipped("identical to an image already in the bag"), nil
	}
	if r.removed[sha] {
		r.mu.Unlock()
		return nil, skipped("previously removed from the bag (use include-removed to import anyway)"), nil
	}
	if r.reencoded[sha] {
		r.mu.Unlock()
		return nil, skipped("the original of an image that was re-encoded (use include-removed to import anyway)"), nil
	}
	if r.opts.SkipIdentical {
		r.active[sha] = true // later identical files in this run are skipped
	}
	existing := r.blobs[sha]
	c, claimed := r.claims[sha]
	if !existing && !claimed {
		c = &claim{done: make(chan struct{})}
		r.claims[sha] = c
		owned = c
	}
	r.mu.Unlock()

	it = &item{f: f, sha: sha, format: format}
	if owned == nil {
		if claimed {
			select {
			case <-c.done:
			case <-ctx.Done():
				return nil, nil, nil
			}
			if c.err != nil {
				return nil, failed(c.err.Error()), nil
			}
		}
		// The blob (with thumbnail and fingerprint) already exists or is
		// written ahead of us: only per-file metadata is needed.
		it.meta = imaging.ReadMeta(format, data)
		it.width, it.height = imaging.OrientedSize(w, h, it.meta.Orientation)
		return it, nil, nil
	}

	res, err := imaging.Process(format, data)
	if err != nil {
		owned.err = err
		return nil, failed(err.Error()), owned
	}
	it.data = data
	it.res = res
	it.meta = res.Meta
	it.width, it.height = res.Width, res.Height
	// The file bytes stay reserved until the writer commits them.
	it.mem = min(f.size, need)
	release = need - it.mem
	return it, nil, owned
}
