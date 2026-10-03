package files

import (
	"archive/zip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"photobag/internal/bag"
	"photobag/internal/sysutil"
)

// Progress is reported while importing or exporting.
type Progress struct {
	Phase   string `json:"phase"` // scanning | copying
	Found   int    `json:"found"`
	Done    int    `json:"done"`
	Bytes   int64  `json:"bytes"`
	Current string `json:"current,omitempty"`
}

// Problem is a file that could not be copied.
type Problem struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Report summarises an import or export.
type Report struct {
	Added    int       `json:"added"`
	Replaced int       `json:"replaced"`
	Renamed  int       `json:"renamed"`
	Skipped  int       `json:"skipped"`
	Failed   int       `json:"failed"`
	Folders  int       `json:"folders"`
	Bytes    int64     `json:"bytes"`
	Problems []Problem `json:"problems"`
	Millis   int64     `json:"millis"`
}

const maxProblems = 500

func (r *Report) problem(p, reason string) {
	r.Failed++
	if len(r.Problems) < maxProblems {
		r.Problems = append(r.Problems, Problem{Path: p, Reason: reason})
	}
}

func (r *Report) count(outcome string) {
	switch outcome {
	case Added:
		r.Added++
	case Replaced:
		r.Replaced++
	case Renamed:
		r.Renamed++
	case Skipped:
		r.Skipped++
	}
}

type diskFile struct {
	path, rel string // rel is slash-separated, below the import root
	dir       bool
	mtime     int64
}

// Import copies a file, or a folder with everything in it, from disk into
// the folder parent. Hidden files and system clutter are left out.
func Import(ctx context.Context, b *bag.Bag, src string, parent int64, conflict string, progress func(Progress)) (*Report, error) {
	start := time.Now()
	if progress == nil {
		progress = func(Progress) {}
	}
	if _, err := validConflict(conflict); err != nil {
		return nil, err
	}
	if err := folder(ctx, b.R, parent); err != nil {
		return nil, err
	}
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	rep := &Report{Problems: []Problem{}}
	prog := Progress{Phase: "scanning"}
	progress(prog)

	// A folder arrives as itself (docs/...); a drive root has no usable
	// name, so its contents go straight into parent.
	base := ""
	if st.IsDir() {
		if n, err := CleanName(filepath.Base(src)); err == nil {
			base = n
		}
	}
	var list []diskFile
	if !st.IsDir() {
		list = append(list, diskFile{path: src, rel: filepath.Base(src), mtime: st.ModTime().UnixMilli()})
	} else {
		err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if p == src {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			rel = filepath.ToSlash(rel)
			if err != nil {
				rep.problem(rel, err.Error())
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || sysutil.IsHidden(d) || sysutil.Junk(name, d.IsDir()) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			info, err := os.Stat(p) // follows symlinks
			if err != nil {
				rep.problem(rel, err.Error())
				return nil
			}
			if !d.IsDir() && !info.Mode().IsRegular() {
				return nil
			}
			list = append(list, diskFile{path: p, rel: rel, dir: d.IsDir(), mtime: info.ModTime().UnixMilli()})
			if !d.IsDir() {
				prog.Found++
				if prog.Found%200 == 0 {
					progress(prog)
				}
			}
			return nil
		})
		if err != nil {
			return rep, err
		}
	}

	if base != "" {
		id, err := makeDirsCounting(ctx, b, parent, base, &rep.Folders)
		if err != nil {
			return rep, err
		}
		parent = id
	}
	prog.Phase = "copying"
	prog.Found = 0
	for _, f := range list {
		if !f.dir {
			prog.Found++
		}
	}
	progress(prog)
	last := time.Now()
	for _, f := range list {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if f.dir {
			if _, err := makeDirsCounting(ctx, b, parent, f.rel, &rep.Folders); err != nil {
				rep.problem(f.rel, err.Error())
			}
			continue
		}
		res, n, err := importFile(ctx, b, f, parent, conflict, &rep.Folders)
		prog.Done++
		prog.Current = f.rel
		if err != nil {
			if ctx.Err() != nil {
				return rep, ctx.Err()
			}
			rep.problem(f.rel, err.Error())
		} else {
			rep.count(res.Outcome)
			if res.Outcome != Skipped {
				rep.Bytes += n
				prog.Bytes += n
			}
		}
		if time.Since(last) > 250*time.Millisecond {
			progress(prog)
			last = time.Now()
		}
	}
	progress(prog)
	rep.Millis = time.Since(start).Milliseconds()
	return rep, nil
}

// makeDirsCounting is MakeDirs, adding the folders it creates to created.
func makeDirsCounting(ctx context.Context, b *bag.Bag, parent int64, rel string, created *int) (int64, error) {
	names, err := SplitPath(rel)
	if err != nil {
		return 0, err
	}
	var id int64
	n := 0
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		n = 0
		var err error
		id, err = ensureDirs(ctx, tx, parent, names, &n)
		return err
	})
	if err == nil {
		*created += n
	}
	return id, err
}

func importFile(ctx context.Context, b *bag.Bag, f diskFile, parent int64, conflict string, folders *int) (*PutResult, int64, error) {
	if conflict == ConflictSkip {
		if there, err := Exists(ctx, b, parent, f.rel); err != nil || there {
			return &PutResult{Outcome: Skipped}, 0, err
		}
	}
	fh, err := os.Open(f.path)
	if err != nil {
		return nil, 0, err
	}
	defer fh.Close()
	cr := &countingReader{r: fh}
	res, err := Store(ctx, b, cr, PutOptions{Parent: parent, Path: f.rel, Modified: f.mtime, Conflict: conflict, Folders: folders})
	return res, cr.n, err
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// walk calls fn for n and everything below it, folders before their
// contents; rel is n's path relative to where the walk started (its own
// name for the first call). fn returns fs.SkipDir to pass over a folder.
func walk(ctx context.Context, b *bag.Bag, n Node, rel string, fn func(rel string, n Node) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fn(rel, n); err != nil {
		if errors.Is(err, fs.SkipDir) {
			return nil
		}
		return err
	}
	if !n.Dir {
		return nil
	}
	kids, err := children(ctx, b.R, n.ID)
	if err != nil {
		return err
	}
	for _, k := range kids {
		if err := walk(ctx, b, k, rel+"/"+k.Name, fn); err != nil {
			return err
		}
	}
	return nil
}

// roots returns the nodes to export: ids, or everything at the top level.
func roots(ctx context.Context, b *bag.Bag, ids []int64) ([]Node, error) {
	if len(ids) == 0 {
		return children(ctx, b.R, 0)
	}
	out := make([]Node, 0, len(ids))
	for _, id := range ids {
		n, err := get(ctx, b.R, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, nil
}

// Export writes files and folders (everything, when ids is empty) into the
// folder dest on disk, keeping their structure and modification times. An
// existing file is renamed around, replaced or skipped as conflict says.
func Export(ctx context.Context, b *bag.Bag, ids []int64, dest string, conflict string, progress func(Progress)) (*Report, error) {
	start := time.Now()
	if progress == nil {
		progress = func(Progress) {}
	}
	conflict, err := validConflict(conflict)
	if err != nil {
		return nil, err
	}
	top, err := roots(ctx, b, ids)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	rep := &Report{Problems: []Problem{}}
	prog := Progress{Phase: "copying"}
	for _, n := range top {
		_ = walk(ctx, b, n, n.Name, func(rel string, n Node) error {
			if !n.Dir {
				prog.Found++
			}
			return nil
		})
	}
	progress(prog)
	last := time.Now()
	for _, root := range top {
		err := walk(ctx, b, root, root.Name, func(rel string, n Node) error {
			target := filepath.Join(dest, filepath.FromSlash(rel))
			if n.Dir {
				if st, err := os.Stat(target); err == nil && !st.IsDir() {
					rep.problem(rel, "a file of that name is in the way")
					return fs.SkipDir
				}
				if err := os.MkdirAll(target, 0o755); err != nil {
					rep.problem(rel, err.Error())
					return fs.SkipDir
				}
				rep.Folders++
				return nil
			}
			prog.Done++
			prog.Current = rel
			outcome, err := exportFile(ctx, b, n, target, conflict)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				rep.problem(rel, err.Error())
			} else {
				rep.count(outcome)
				if outcome != Skipped {
					rep.Bytes += n.Size
					prog.Bytes += n.Size
				}
			}
			if time.Since(last) > 250*time.Millisecond {
				progress(prog)
				last = time.Now()
			}
			return nil
		})
		if err != nil {
			return rep, err
		}
	}
	progress(prog)
	rep.Millis = time.Since(start).Milliseconds()
	return rep, nil
}

func exportFile(ctx context.Context, b *bag.Bag, n Node, target, conflict string) (string, error) {
	outcome := Added
	if st, err := os.Stat(target); err == nil {
		switch {
		case conflict == ConflictSkip:
			return Skipped, nil
		case conflict == ConflictReplace && !st.IsDir():
			outcome = Replaced
		default:
			dir, name := filepath.Split(target)
			for i := 2; ; i++ {
				cand := filepath.Join(dir, numbered(name, i, false))
				if _, err := os.Stat(cand); errors.Is(err, fs.ErrNotExist) {
					target = cand
					break
				}
			}
			outcome = Renamed
		}
	}
	r, _, err := Open(ctx, b, n.ID)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".photobag-*.tmp")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		t := time.UnixMilli(n.ModifiedAt)
		err = os.Chtimes(tmp.Name(), t, t)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), target)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return outcome, nil
}

// storedTypes are already compressed, so zips store them as they are.
var storedTypes = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".avif": true, ".zip": true, ".gz": true, ".tgz": true, ".7z": true, ".rar": true, ".xz": true, ".zst": true,
	".mp3": true, ".m4a": true, ".ogg": true, ".opus": true, ".flac": true, ".aac": true, ".mp4": true,
	".m4v": true, ".webm": true, ".mov": true, ".mkv": true, ".docx": true, ".xlsx": true, ".pptx": true,
	".odt": true, ".epub": true, ".woff2": true, ".pdf": true, ".photobag": true}

// WriteZip writes files and folders (everything, when ids is empty) to w
// as a zip archive.
func WriteZip(ctx context.Context, b *bag.Bag, ids []int64, w io.Writer) error {
	top, err := roots(ctx, b, ids)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	for _, root := range top {
		err := walk(ctx, b, root, root.Name, func(rel string, n Node) error {
			h := &zip.FileHeader{Name: rel, Modified: time.UnixMilli(n.ModifiedAt)}
			if n.Dir {
				h.Name += "/"
				_, err := zw.CreateHeader(h)
				return err
			}
			h.Method = zip.Deflate
			if storedTypes[strings.ToLower(path.Ext(n.Name))] {
				h.Method = zip.Store
			}
			h.UncompressedSize64 = uint64(n.Size)
			fw, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			r, _, err := Open(ctx, b, n.ID)
			if err != nil {
				return err
			}
			_, err = io.Copy(fw, r)
			return err
		})
		if err != nil {
			return fmt.Errorf("writing the zip: %w", err)
		}
	}
	return zw.Close()
}
