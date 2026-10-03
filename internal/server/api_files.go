package server

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"photobag/internal/exporter"
	"photobag/internal/files"
	"photobag/internal/sysutil"
)

func (s *Server) fileRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/files", s.browseFiles)
	s.handle(mux, "GET /api/files/summary", s.fileSummary)
	s.handle(mux, "GET /api/files/folders", s.fileFolders)
	s.handle(mux, "POST /api/files/folders", s.makeFolder)
	s.handle(mux, "POST /api/files/check", s.checkFiles)
	s.handle(mux, "PUT /api/files/upload", s.uploadFile)
	s.handle(mux, "PATCH /api/files/{id}", s.renameFile)
	s.handle(mux, "POST /api/files/move", s.moveFiles)
	s.handle(mux, "POST /api/files/delete", s.deleteFiles)
	s.handle(mux, "GET /api/files/{id}/content", s.fileContent)
	s.handle(mux, "GET /api/files-raw/{path...}", s.fileByPath)
	s.handle(mux, "GET /api/files/zip", s.zipFiles)
	s.handle(mux, "POST /api/jobs/files-import", s.startFilesImport)
	s.handle(mux, "POST /api/jobs/files-export", s.startFilesExport)
}

func queryID(r *http.Request, name string) (int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id < 0 {
		return 0, apiErr(http.StatusBadRequest, "invalid %s", name)
	}
	return id, nil
}

// browseFiles describes the place at ?path= (or ?id=): a folder with its
// contents, or a file.
func (s *Server) browseFiles(w http.ResponseWriter, r *http.Request) error {
	if r.URL.Query().Has("id") {
		id, err := queryID(r, "id")
		if err != nil {
			return err
		}
		l, err := files.BrowseID(r.Context(), s.b, id)
		if err != nil {
			return err
		}
		return ok(w, l)
	}
	l, err := files.Browse(r.Context(), s.b, r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	return ok(w, l)
}

func (s *Server) fileSummary(w http.ResponseWriter, r *http.Request) error {
	sum, err := files.GetSummary(r.Context(), s.b)
	if err != nil {
		return err
	}
	return ok(w, sum)
}

func (s *Server) fileFolders(w http.ResponseWriter, r *http.Request) error {
	list, err := files.Folders(r.Context(), s.b)
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) makeFolder(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Parent int64  `json:"parent"`
		Name   string `json:"name"`
		// Path, instead of Name, makes the folders on a relative path,
		// keeping any that exist (for uploads of empty folders).
		Path string `json:"path"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	var n *files.Node
	var err error
	if req.Path != "" {
		var id int64
		if id, err = files.MakeDirs(r.Context(), s.b, req.Parent, req.Path); err == nil {
			n, err = files.Get(r.Context(), s.b, id)
		}
	} else {
		n, err = files.MakeDir(r.Context(), s.b, req.Parent, req.Name)
	}
	if err != nil {
		return err
	}
	s.events.Changed("files")
	return ok(w, n)
}

// checkFiles reports which of the relative paths already exist below a
// folder, so the UI can ask what to do before uploading.
func (s *Server) checkFiles(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Parent int64    `json:"parent"`
		Paths  []string `json:"paths"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	existing := []string{}
	for _, p := range req.Paths {
		there, err := files.Exists(r.Context(), s.b, req.Parent, p)
		if err != nil {
			return err
		}
		if there {
			existing = append(existing, p)
		}
	}
	return ok(w, map[string]any{"existing": existing})
}

// uploadFile stores the request body as a file:
// ?parent=<folder id>&path=<relative path>&modified=<unix ms>&conflict=rename|replace|skip.
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	parent, err := queryID(r, "parent")
	if err != nil {
		return err
	}
	modified, _ := strconv.ParseInt(q.Get("modified"), 10, 64)
	opts := files.PutOptions{Parent: parent, Path: q.Get("path"), Modified: modified, Conflict: q.Get("conflict")}
	ctx := r.Context()
	if opts.Conflict == files.ConflictSkip {
		if there, err := files.Exists(ctx, s.b, parent, opts.Path); err != nil {
			return err
		} else if there {
			return ok(w, files.PutResult{Outcome: files.Skipped})
		}
	}
	if r.ContentLength > 0 {
		if free, err := sysutil.DiskFree(filepath.Dir(s.b.Path)); err == nil && uint64(r.ContentLength)+64<<20 > free {
			return apiErr(http.StatusInsufficientStorage, "not enough disk space for this file (%s free)", humanSize(int64(free)))
		}
	}
	res, err := files.Store(ctx, s.b, r.Body, opts)
	if err != nil {
		return err
	}
	s.events.Changed("files")
	return ok(w, res)
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (s *Server) renameFile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	n, err := files.Rename(r.Context(), s.b, id, req.Name)
	if err != nil {
		return err
	}
	s.events.Changed("files")
	return ok(w, n)
}

func (s *Server) moveFiles(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		IDs    []int64 `json:"ids"`
		Parent int64   `json:"parent"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	res, err := files.Move(r.Context(), s.b, req.IDs, req.Parent)
	if err != nil {
		return err
	}
	s.events.Changed("files")
	return ok(w, res)
}

func (s *Server) deleteFiles(w http.ResponseWriter, r *http.Request) error {
	var req idsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	res, err := files.Delete(r.Context(), s.b, req.IDs)
	if err != nil {
		return err
	}
	s.events.Changed("files")
	return ok(w, res)
}

func (s *Server) fileContent(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	return s.sendFile(w, r, id, r.URL.Query().Get("download") != "")
}

// fileByPath serves a file by its path, so relative links in a Markdown
// note (images beside it) resolve.
func (s *Server) fileByPath(w http.ResponseWriter, r *http.Request) error {
	n, err := files.Resolve(r.Context(), s.b, r.PathValue("path"))
	if err != nil {
		return err
	}
	if n == nil || n.Dir {
		return files.ErrNotFound
	}
	return s.sendFile(w, r, n.ID, false)
}

// sendFile serves a stored file. Only kinds the browser shows harmlessly
// are served inline, and never as active content: text (HTML included)
// goes out as text/plain, and everything but PDF (whose viewer refuses
// sandboxing) is sandboxed, so a stored page or SVG cannot run scripts
// against the app.
func (s *Server) sendFile(w http.ResponseWriter, r *http.Request, id int64, download bool) error {
	rd, n, err := files.Open(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	typ, inline := n.Type, true
	switch n.Kind {
	case files.KindText, files.KindMarkdown:
		typ = "text/plain; charset=utf-8"
	case files.KindImage, files.KindPDF, files.KindAudio, files.KindVideo:
	default:
		inline = false
	}
	if typ == "" {
		typ = "application/octet-stream"
	}
	disp := "inline"
	if download || !inline {
		disp = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", typ)
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": n.Name}))
	h.Set("X-Content-Type-Options", "nosniff")
	if n.Kind != files.KindPDF {
		h.Set("Content-Security-Policy", "sandbox")
	}
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `"`+n.SHA256+`"`)
	http.ServeContent(w, r, "", time.UnixMilli(n.ModifiedAt), rd)
	return nil
}

func (s *Server) zipFiles(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var ids []int64
	for _, v := range r.URL.Query()["id"] {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return apiErr(http.StatusBadRequest, "invalid id")
		}
		ids = append(ids, id)
	}
	name, _ := s.b.Meta(ctx, "name")
	name = exporter.SafeName(name, "") + " files"
	switch len(ids) {
	case 0:
	case 1:
		n, err := files.Get(ctx, s.b, ids[0])
		if err != nil {
			return err
		}
		name = n.Name
	default:
		name = "files"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".zip"}))
	w.Header().Set("Cache-Control", "no-store")
	if err := files.WriteZip(ctx, s.b, ids, w); err != nil {
		// The status has gone out; all that is left is to cut the zip short.
		s.log.Warn("zip download failed", "err", err)
	}
	return nil
}

func (s *Server) startFilesImport(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Path     string `json:"path"`
		Parent   int64  `json:"parent"`
		Conflict string `json:"conflict"`
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
	where := "Files"
	if req.Parent != 0 {
		if where, err = files.PathOf(r.Context(), s.b, req.Parent); err != nil {
			return err
		}
	}
	job := s.jobs.Submit("files-import", "Import "+p+" into "+where, func(ctx context.Context, report func(any, string)) (any, error) {
		last := time.Now()
		rep, err := files.Import(ctx, s.b, p, req.Parent, req.Conflict, func(pr files.Progress) {
			report(pr, pr.Current)
			if time.Since(last) > 2*time.Second {
				s.events.Changed("files")
				last = time.Now()
			}
		})
		s.events.Changed("files")
		return rep, err
	})
	return ok(w, job)
}

func (s *Server) startFilesExport(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Path     string  `json:"path"`
		IDs      []int64 `json:"ids"`
		Conflict string  `json:"conflict"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	p, err := cleanPath(req.Path)
	if err != nil {
		return err
	}
	what := "all files"
	if len(req.IDs) == 1 {
		n, err := files.Get(r.Context(), s.b, req.IDs[0])
		if err != nil {
			return err
		}
		what = n.Name
	} else if len(req.IDs) > 1 {
		what = fmt.Sprintf("%d items", len(req.IDs))
	}
	job := s.jobs.Submit("files-export", "Export "+what+" to "+p, func(ctx context.Context, report func(any, string)) (any, error) {
		return files.Export(ctx, s.b, req.IDs, p, req.Conflict, func(pr files.Progress) { report(pr, pr.Current) })
	})
	return ok(w, job)
}
