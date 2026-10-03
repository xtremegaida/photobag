package server

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"photobag/internal/importer"
	"photobag/internal/jobs"
	"photobag/internal/sysutil"
)

// uploadIdle is how long an upload for import may sit unused before its
// files are thrown away.
const uploadIdle = time.Hour

// importUpload is a set of files being uploaded for an import (see
// importer.PlanUpload).
type importUpload struct {
	dir      string
	title    string
	accepted map[string]string // offered path -> path in dir
	received map[string]bool   // offered paths
	skip     []importer.FileReport
	used     time.Time
	sending  int    // uploads in progress
	started  bool   // the import job owns the folder
	job      string // its id
}

// planUpload answers an offer of files to upload for an import, and makes
// the folder where they wait.
func (s *Server) planUpload(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Files []importer.UploadOffer `json:"files"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if len(req.Files) == 0 {
		return apiErr(http.StatusBadRequest, "no files to upload")
	}
	accepted, skip, bytes := importer.PlanUpload(req.Files)
	// The files take their space twice: waiting, and in the bag.
	if free, err := sysutil.DiskFree(filepath.Dir(s.b.Path)); err == nil && uint64(2*bytes)+64<<20 > free {
		return apiErr(http.StatusInsufficientStorage, "not enough disk space: uploading and importing %s needs about %s (%s free)",
			humanSize(bytes), humanSize(2*bytes), humanSize(int64(free)))
	}
	var b [12]byte
	rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	u := &importUpload{
		dir:      importer.UploadDir(s.b.Path, id),
		title:    importer.DescribeUpload(req.Files) + " (uploaded)",
		accepted: accepted,
		received: map[string]bool{},
		skip:     skip,
		used:     time.Now(),
	}
	if err := os.Mkdir(u.dir, 0o700); err != nil {
		return err
	}
	s.uploadsMu.Lock()
	s.uploads[id] = u
	s.uploadsMu.Unlock()
	return ok(w, importer.UploadPlan{ID: id, Skip: skip})
}

// uploadFor finds an upload that can still take files.
func (s *Server) uploadFor(id string) (*importUpload, error) {
	u := s.uploads[id]
	if u == nil {
		return nil, apiErr(http.StatusNotFound, "this upload has ended; drop the files again")
	}
	if u.started {
		return nil, apiErr(http.StatusConflict, "the import of this upload has started")
	}
	return u, nil
}

// receiveUpload stores one file of an upload:
// ?path=<offered path>&modified=<unix ms>.
func (s *Server) receiveUpload(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	offered := q.Get("path")
	s.uploadsMu.Lock()
	u, err := s.uploadFor(r.PathValue("id"))
	rel := ""
	if err == nil {
		if rel = u.accepted[offered]; rel == "" {
			err = apiErr(http.StatusBadRequest, "%s was not offered for this upload", offered)
		}
	}
	if err != nil {
		s.uploadsMu.Unlock()
		return err
	}
	u.sending++
	u.used = time.Now()
	s.uploadsMu.Unlock()
	defer func() {
		s.uploadsMu.Lock()
		u.sending--
		u.used = time.Now()
		s.uploadsMu.Unlock()
	}()

	if err := s.roomFor(r); err != nil {
		return err
	}
	dst := filepath.Join(u.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, importer.MaxFileSize))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return err
	}
	if ms, _ := strconv.ParseInt(q.Get("modified"), 10, 64); ms > 0 {
		t := time.UnixMilli(ms)
		os.Chtimes(dst, t, t)
	}
	s.uploadsMu.Lock()
	u.received[offered] = true
	gone := s.uploads[r.PathValue("id")] != u
	s.uploadsMu.Unlock()
	if gone { // cancelled meanwhile
		os.RemoveAll(u.dir)
		return apiErr(http.StatusNotFound, "this upload was cancelled")
	}
	return ok(w, map[string]bool{"ok": true})
}

// importUpload imports the files received, with the import options, as a
// job; the files left out, or not received, are in its report.
func (s *Server) importUpload(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Options importer.Options `json:"options"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	id := r.PathValue("id")
	s.uploadsMu.Lock()
	u, err := s.uploadFor(id)
	if err == nil && u.sending > 0 {
		err = apiErr(http.StatusConflict, "files are still being uploaded")
	}
	if err != nil {
		s.uploadsMu.Unlock()
		return err
	}
	u.started = true
	leftOut := slices.Clone(u.skip)
	var missing []string
	for offered := range u.accepted {
		if !u.received[offered] {
			missing = append(missing, offered)
		}
	}
	s.uploadsMu.Unlock()
	slices.Sort(missing)
	for _, p := range missing {
		leftOut = append(leftOut, importer.FileReport{Path: p, Status: "failed", Reason: "not uploaded"})
	}

	opts := req.Options
	opts.Recursive = true // dropped folders came with the files the browser chose
	opts.Source = u.title
	opts.LeftOut = leftOut
	job := s.submitImport(u.title, u.dir, opts, func() { s.dropUpload(id) })
	s.uploadsMu.Lock()
	u.job = job.ID
	s.uploadsMu.Unlock()
	return ok(w, job)
}

// cancelUpload throws an upload's files away.
func (s *Server) cancelUpload(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	s.uploadsMu.Lock()
	u, err := s.uploadFor(id)
	if err == nil {
		delete(s.uploads, id)
	}
	s.uploadsMu.Unlock()
	if err != nil {
		return err
	}
	os.RemoveAll(u.dir)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) dropUpload(id string) {
	s.uploadsMu.Lock()
	u := s.uploads[id]
	delete(s.uploads, id)
	s.uploadsMu.Unlock()
	if u != nil {
		os.RemoveAll(u.dir)
	}
}

// expireUploads throws away uploads left unused (a browser closed while
// uploading) or whose import was cancelled before it began, or all of
// them when the server stops.
func (s *Server) expireUploads(all bool) {
	s.uploadsMu.Lock()
	var gone []string
	for id, u := range s.uploads {
		idle := !u.started && u.sending == 0 && time.Since(u.used) > uploadIdle
		if u.job != "" {
			j, found := s.jobs.Get(u.job)
			idle = !found || (j.Status != jobs.Queued && j.Status != jobs.Running)
		}
		if all || idle {
			gone = append(gone, id)
		}
	}
	s.uploadsMu.Unlock()
	for _, id := range gone {
		s.dropUpload(id)
	}
	if all {
		importer.CleanUploads(s.b.Path)
	}
}
