package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"photobag/internal/backup"
	"photobag/internal/exporter"
	"photobag/internal/importer"
	"photobag/internal/library"
	"photobag/internal/sysutil"
)

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.jobs.List())
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) error {
	j, found := s.jobs.Get(r.PathValue("id"))
	if !found {
		return apiErr(http.StatusNotFound, "no such job")
	}
	return ok(w, j)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) error {
	if !s.jobs.Cancel(r.PathValue("id")) {
		return apiErr(http.StatusNotFound, "no such job")
	}
	return ok(w, map[string]bool{"ok": true})
}

// cleanPath validates a server-side path entered in the UI.
func cleanPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", apiErr(http.StatusBadRequest, "a folder path is required")
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", badRequest(err)
	}
	return abs, nil
}

func (s *Server) startImport(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Path    string           `json:"path"`
		Options importer.Options `json:"options"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	p, err := cleanPath(req.Path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err != nil {
		return badRequest(err)
	}
	opts := req.Options
	job := s.jobs.Submit("import", "Import "+p, func(ctx context.Context, report func(any, string)) (any, error) {
		rep, err := importer.Run(ctx, s.b, p, opts, func(pr importer.Progress) {
			report(pr, pr.Current)
			if pr.Phase == "importing" && pr.Added > 0 && pr.Added%100 == 0 {
				s.events.Changed("images", "tags")
			}
		})
		s.events.Changed("images", "tags")
		return rep, err
	})
	return ok(w, job)
}

func (s *Server) startExport(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Path    string           `json:"path"`
		Options exporter.Options `json:"options"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	p, err := cleanPath(req.Path)
	if err != nil {
		return err
	}
	if err := req.Options.Query.Validate(); err != nil {
		return badRequest(err)
	}
	opts := req.Options
	job := s.jobs.Submit("export", "Export "+opts.Query.Describe()+" to "+p, func(ctx context.Context, report func(any, string)) (any, error) {
		return exporter.Run(ctx, s.b, p, opts, func(pr exporter.Progress) { report(pr, pr.Current) })
	})
	return ok(w, job)
}

func (s *Server) startBackup(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Mode string `json:"mode"` // download | path
		Path string `json:"path"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	name, _ := s.b.Meta(r.Context(), "name")
	if name == "" {
		name = "photobag"
	}
	filename := fmt.Sprintf("%s-%s.photobag", exporter.SafeName(name, ""), time.Now().Format("20060102-150405"))
	switch req.Mode {
	case "path":
		p, err := cleanPath(req.Path)
		if err != nil {
			return err
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			p = filepath.Join(p, filename)
		}
		job := s.jobs.Submit("backup", "Backup to "+p, func(ctx context.Context, report func(any, string)) (any, error) {
			report(nil, "Writing "+p)
			return backup.To(ctx, s.b, p)
		})
		return ok(w, job)
	case "", "download":
		job := s.jobs.Submit("backup", "Backup for download", func(ctx context.Context, report func(any, string)) (any, error) {
			report(nil, "Copying the bag (VACUUM INTO)")
			tmp := backup.TempPath(s.b.Path)
			res, err := backup.To(ctx, s.b, tmp)
			if err != nil {
				return nil, err
			}
			var b [16]byte
			rand.Read(b[:])
			tok := hex.EncodeToString(b[:])
			s.backupsMu.Lock()
			s.backups[tok] = &pendingBackup{path: tmp, filename: filename, expires: time.Now().Add(time.Hour)}
			s.backupsMu.Unlock()
			return map[string]any{"token": tok, "filename": filename, "bytes": res.Bytes, "url": "/api/backups/" + tok}, nil
		})
		return ok(w, job)
	}
	return apiErr(http.StatusBadRequest, "unknown backup mode %q", req.Mode)
}

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) error {
	tok := r.PathValue("token")
	s.backupsMu.Lock()
	p := s.backups[tok]
	s.backupsMu.Unlock()
	if p == nil {
		return apiErr(http.StatusNotFound, "backup expired or already downloaded")
	}
	f, err := os.Open(p.path)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": p.filename}))
	http.ServeContent(w, r, p.filename, st.ModTime(), f)
	f.Close()
	// A complete, non-range download consumes the backup.
	if r.Context().Err() == nil && r.Header.Get("Range") == "" && r.Method == http.MethodGet {
		s.backupsMu.Lock()
		delete(s.backups, tok)
		s.backupsMu.Unlock()
		os.Remove(p.path)
	}
	return nil
}

func (s *Server) startCompact(w http.ResponseWriter, r *http.Request) error {
	job := s.jobs.Submit("compact", "Compact bag", func(ctx context.Context, report func(any, string)) (any, error) {
		report(nil, "Rebuilding the bag file (VACUUM)")
		before, after, err := backup.Compact(ctx, s.b)
		return map[string]int64{"before": before, "after": after}, err
	})
	return ok(w, job)
}

func (s *Server) getThumbSettings(w http.ResponseWriter, r *http.Request) error {
	t, err := library.GetThumbInfo(r.Context(), s.b)
	if err != nil {
		return err
	}
	return ok(w, t)
}

// putThumbSettings changes where the bag keeps its thumbnails, as a job:
// making them all, or dropping them and giving the space back, takes a
// while in a large bag.
func (s *Server) putThumbSettings(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Mode library.ThumbMode `json:"mode"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if !req.Mode.Valid() {
		return badRequest(fmt.Errorf("mode must be %q or %q", library.ThumbsStored, library.ThumbsOnDemand))
	}
	title := "Store thumbnails in the bag"
	if req.Mode == library.ThumbsOnDemand {
		title = "Make thumbnails on demand"
	}
	job := s.jobs.Submit("thumbnails", title, func(ctx context.Context, report func(any, string)) (any, error) {
		phase := "making"
		if req.Mode == library.ThumbsOnDemand {
			phase = "freeing"
			report(map[string]any{"phase": "dropping"}, "Removing the stored thumbnails")
		}
		rep, err := importer.SetThumbMode(ctx, s.b, req.Mode, func(done, total int) {
			report(map[string]any{"phase": phase, "done": done, "total": total}, "")
		})
		s.events.Changed("thumbnails")
		return rep, err
	})
	return ok(w, job)
}

func (s *Server) startEmptyTrash(w http.ResponseWriter, r *http.Request) error {
	var req idsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ids := req.IDs
	job := s.jobs.Submit("empty-trash", "Empty trash", func(ctx context.Context, report func(any, string)) (any, error) {
		report(nil, "Purging trashed images")
		res, err := library.EmptyTrash(ctx, s.b, ids)
		s.events.Changed("images", "trash", "tags", "metrics")
		return res, err
	})
	return ok(w, job)
}

// fsEntry is a folder in the server-side folder picker.
type fsEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".bmp": true, ".tif": true, ".tiff": true, ".jpe": true, ".jfif": true}

func (s *Server) fsList(w http.ResponseWriter, r *http.Request) error {
	p := r.URL.Query().Get("path")
	if p == "" {
		if home, err := os.UserHomeDir(); err == nil {
			p = home
		} else {
			p = filepath.Dir(s.b.Path)
		}
	}
	p, err := cleanPath(p)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return badRequest(err)
	}
	dirs := []fsEntry{}
	images := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, fsEntry{Name: name, Path: filepath.Join(p, name)})
		} else if imageExts[strings.ToLower(filepath.Ext(name))] {
			images++
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	home, _ := os.UserHomeDir()
	return ok(w, map[string]any{
		"path": p, "parent": parent, "dirs": dirs, "images": images,
		"roots": roots(), "home": home, "bagDir": filepath.Dir(s.b.Path), "separator": string(filepath.Separator),
	})
}

func roots() []fsEntry {
	var out []fsEntry
	for _, r := range sysutil.Roots() {
		out = append(out, fsEntry{Name: strings.TrimRight(r, `\`), Path: r})
	}
	return out
}
