package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"photobag/internal/jobs"
	"photobag/internal/query"
	"photobag/internal/reencode"
)

func (s *Server) reencodeRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/reencode", s.listReencodes)
	s.handle(mux, "POST /api/reencode", s.createReencode)
	s.handle(mux, "POST /api/reencode/try", s.tryReencode)
	s.handle(mux, "GET /api/reencode/{id}", s.getReencode)
	s.handle(mux, "POST /api/reencode/{id}/decide", s.decideReencode)
	s.handle(mux, "POST /api/reencode/{id}/resume", s.resumeReencode)
	s.handle(mux, "POST /api/reencode/{id}/stop", s.stopReencode)
	s.handle(mux, "DELETE /api/reencode/{id}", s.deleteReencode)
	s.handle(mux, "GET /api/reencode/{id}/result/{image}", s.reencodeResult)
}

// reencodeImages are the images of a re-encode: ids, or a query (in the
// gallery's order).
type reencodeImages struct {
	IDs   []int64           `json:"ids"`
	Query *query.ImageQuery `json:"query"`
	Sort  query.Sort        `json:"sort"`
}

func (s *Server) resolveImages(ctx context.Context, req reencodeImages) ([]int64, string, error) {
	if req.Query == nil {
		return req.IDs, fmt.Sprintf("%d selected image%s", len(req.IDs), plural(len(req.IDs))), nil
	}
	ids, err := s.orderedIDs(ctx, *req.Query, req.Sort)
	if err != nil {
		return nil, "", err
	}
	desc := fmt.Sprintf("%d image%s", len(ids), plural(len(ids)))
	if !req.Query.IsEmpty() {
		desc += ": " + req.Query.Describe()
	}
	return ids, desc, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (s *Server) listReencodes(w http.ResponseWriter, r *http.Request) error {
	list, err := reencode.List(r.Context(), s.b)
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) createReencode(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		reencodeImages
		Settings reencode.Settings `json:"settings"`
		Mode     string            `json:"mode"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ids, desc, err := s.resolveImages(r.Context(), req.reencodeImages)
	if err != nil {
		return err
	}
	bt, err := reencode.Create(r.Context(), s.b, ids, req.Settings, req.Mode, desc)
	if err != nil {
		return err
	}
	if err := s.submitReencode(r.Context(), bt); err != nil {
		return err
	}
	s.events.Changed("reencode")
	bt, err = reencode.Get(r.Context(), s.b, bt.ID)
	if err != nil {
		return err
	}
	return ok(w, bt)
}

// submitReencode queues the job working through a batch's pending images.
func (s *Server) submitReencode(ctx context.Context, bt *reencode.Batch) error {
	if err := reencode.SetState(ctx, s.b, bt.ID, reencode.StateQueued, "", ""); err != nil {
		return err
	}
	id := bt.ID
	title := fmt.Sprintf("Re-encode %s as %s", bt.Description, bt.Settings.Describe())
	job := s.jobs.Submit("reencode", title, func(ctx context.Context, report func(any, string)) (any, error) {
		last := time.Now()
		replaced := 0
		prog, err := reencode.Run(ctx, s.b, id, func(p reencode.Progress) {
			report(p, p.Current)
			if time.Since(last) > 2*time.Second {
				topics := []string{"reencode"}
				if p.Replaced != replaced {
					topics = append(topics, "images")
					replaced = p.Replaced
				}
				s.events.Changed(topics...)
				last = time.Now()
			}
		})
		s.simSort.clear()
		s.events.Changed("reencode", "images", "stats")
		return map[string]any{"batchId": id, "progress": prog}, err
	})
	return reencode.SetJob(ctx, s.b, id, job.ID)
}

func (s *Server) tryReencode(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		reencodeImages
		Settings reencode.Settings `json:"settings"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ids, _, err := s.resolveImages(r.Context(), req.reencodeImages)
	if err != nil {
		return err
	}
	// A few images from across the set.
	var sample []int64
	if n := len(ids); n <= 3 {
		sample = ids
	} else {
		sample = []int64{ids[0], ids[n/2], ids[n-1]}
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	est, err := reencode.Try(ctx, s.b, sample, req.Settings)
	if err != nil {
		return err
	}
	return ok(w, est)
}

func (s *Server) getReencode(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := reencode.GetDetail(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	return ok(w, d)
}

func (s *Server) decideReencode(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		IDs     []int64 `json:"ids"`
		Replace bool    `json:"replace"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	d, err := reencode.Decide(r.Context(), s.b, id, req.IDs, req.Replace)
	if err != nil {
		return err
	}
	if d.Replaced > 0 {
		s.simSort.clear()
	}
	s.events.Changed("reencode", "images", "stats")
	return ok(w, d)
}

func (s *Server) resumeReencode(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	bt, err := reencode.Get(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	if bt.State == reencode.StateRunning || bt.State == reencode.StateQueued {
		return apiErr(http.StatusConflict, "this re-encode is already running")
	}
	if bt.Counts.Pending == 0 {
		return apiErr(http.StatusBadRequest, "no images are left to re-encode")
	}
	if err := s.submitReencode(r.Context(), bt); err != nil {
		return err
	}
	s.events.Changed("reencode")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) stopReencode(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	bt, err := reencode.Get(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	if bt.JobID != "" {
		s.jobs.Cancel(bt.JobID)
	}
	// A job cancelled before it started never runs to record that.
	if j, found := s.jobs.Get(bt.JobID); !found || j.Status == jobs.Cancelled && j.StartedAt == 0 {
		if err := reencode.SetState(r.Context(), s.b, id, reencode.StateStopped, "", ""); err != nil {
			return err
		}
	}
	s.events.Changed("reencode")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) deleteReencode(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := reencode.Delete(r.Context(), s.b, id); err != nil {
		return err
	}
	s.events.Changed("reencode", "stats")
	return ok(w, map[string]bool{"ok": true})
}

// reencodeResult serves a result awaiting review.
func (s *Server) reencodeResult(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	image, err := pathID(r, "image")
	if err != nil {
		return err
	}
	data, f, err := reencode.Result(r.Context(), s.b, id, image)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", f.MIME())
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
	return nil
}
