package server

import (
	"context"
	"net/http"
	"strconv"

	"photobag/internal/dedup"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/scoring"
)

// --- dedup ---

type scanSummary struct {
	*dedup.Scan
	Clusters int `json:"clusters"`
}

func (s *Server) startScan(w http.ResponseWriter, r *http.Request) error {
	var p dedup.Params
	if err := readJSON(r, &p); err != nil {
		return err
	}
	if p.Mode != dedup.ModeExact && p.Mode != dedup.ModeSimilar {
		return apiErr(http.StatusBadRequest, "mode must be exact or similar")
	}
	if err := p.Scope.Validate(); err != nil {
		return badRequest(err)
	}
	title := "Find exact duplicates"
	if p.Mode == dedup.ModeSimilar {
		title = "Find similar images"
	}
	job := s.jobs.Submit("dedup-scan", title, func(ctx context.Context, report func(any, string)) (any, error) {
		sc, err := dedup.NewScan(ctx, s.b, p, func(done, total int) {
			report(map[string]int{"done": done, "total": total}, "")
		})
		if err != nil {
			return nil, err
		}
		s.scans.Put(sc)
		s.events.Changed("dedup")
		return scanSummary{sc, len(sc.Clusters(dedup.DefaultThreshold))}, nil
	})
	return ok(w, job)
}

func (s *Server) listScans(w http.ResponseWriter, r *http.Request) error {
	out := []*dedup.Scan{}
	out = append(out, s.scans.All()...)
	return ok(w, out)
}

func (s *Server) getScan(w http.ResponseWriter, r *http.Request) error {
	sc := s.scans.Get(r.PathValue("id"))
	if sc == nil {
		return apiErr(http.StatusNotFound, "scan not found (scans are kept in memory; run a new one)")
	}
	threshold := dedup.DefaultThreshold
	if v, err := strconv.ParseFloat(r.URL.Query().Get("threshold"), 64); err == nil && v > 0 && v <= 1 {
		threshold = v
	}
	clusters := sc.Clusters(threshold)
	// Also return the metadata of every member so the UI can render in one go.
	var ids []int64
	for _, c := range clusters {
		ids = append(ids, c.Members...)
	}
	images, err := library.GetImages(r.Context(), s.b, ids)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"scan": sc, "threshold": threshold, "clusters": clusters, "images": images})
}

func (s *Server) resolveDups(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ScanID      string             `json:"scanId"`
		Resolutions []dedup.Resolution `json:"resolutions"`
		// Threshold with ApplyAll resolves every current proposal of the scan.
		ApplyAll  bool    `json:"applyAll"`
		Threshold float64 `json:"threshold"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	sc := s.scans.Get(req.ScanID)
	res := req.Resolutions
	if req.ApplyAll {
		if sc == nil {
			return apiErr(http.StatusNotFound, "scan not found")
		}
		t := req.Threshold
		if t <= 0 {
			t = dedup.DefaultThreshold
		}
		res = dedup.Proposals(sc.Clusters(t))
	}
	n, err := dedup.Resolve(r.Context(), s.b, res)
	if err != nil {
		return badRequest(err)
	}
	if sc != nil {
		sc.MarkResolved(res)
	}
	s.events.Changed("images", "trash", "tags", "metrics", "dedup")
	return ok(w, map[string]int{"trashed": n, "resolved": len(res)})
}

// --- metrics & runs ---

func (s *Server) listMetrics(w http.ResponseWriter, r *http.Request) error {
	m, err := s.scoring.Metrics(r.Context())
	if err != nil {
		return err
	}
	return ok(w, m)
}

type metricRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Server) createMetric(w http.ResponseWriter, r *http.Request) error {
	var req metricRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	m, err := s.scoring.EnsureMetric(r.Context(), req.Name, req.Description)
	if err != nil {
		return badRequest(err)
	}
	s.events.Changed("metrics")
	return ok(w, m)
}

func (s *Server) updateMetric(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req metricRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := s.scoring.UpdateMetric(r.Context(), id, req.Name, req.Description); err != nil {
		if err == scoring.ErrNotFound {
			return err
		}
		return badRequest(err)
	}
	s.events.Changed("metrics", "runs")
	m, err := s.scoring.GetMetric(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, m)
}

func (s *Server) deleteMetric(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.scoring.DeleteMetric(r.Context(), id); err != nil {
		return err
	}
	s.events.Changed("metrics", "runs")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) rankings(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ranks, m, err := s.scoring.Rankings(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"metric": m, "rankings": ranks})
}

func (s *Server) recalc(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	sum, err := s.scoring.Recompute(r.Context(), id)
	if err != nil {
		return err
	}
	s.events.Changed("metrics")
	return ok(w, sum)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) error {
	metric, _ := strconv.ParseInt(r.URL.Query().Get("metric"), 10, 64)
	runs, err := s.scoring.Runs(r.Context(), metric)
	if err != nil {
		return err
	}
	return ok(w, runs)
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		MetricID   int64            `json:"metricId"`
		MetricName string           `json:"metricName"`
		Query      query.ImageQuery `json:"query"`
		Seed       int64            `json:"seed"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	if req.MetricID == 0 {
		m, err := s.scoring.EnsureMetric(ctx, req.MetricName, "")
		if err != nil {
			return badRequest(err)
		}
		req.MetricID = m.ID
	}
	if err := req.Query.Validate(); err != nil {
		return badRequest(err)
	}
	run, err := s.scoring.CreateRun(ctx, req.MetricID, req.Query, req.Seed)
	if err != nil {
		if err == scoring.ErrNotFound {
			return err
		}
		return badRequest(err)
	}
	s.events.Changed("runs", "metrics")
	return ok(w, run)
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	run, err := s.scoring.GetRun(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, run)
}

func (s *Server) deleteRun(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.scoring.DeleteRun(r.Context(), id); err != nil {
		return err
	}
	s.events.Changed("runs", "metrics")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) nextPair(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := s.scoring.Next(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, p)
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Pos    int64 `json:"pos"`
		Winner int64 `json:"winner"` // 0 = skip
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	p, err := s.scoring.Answer(r.Context(), id, req.Pos, req.Winner)
	if err != nil {
		if err == scoring.ErrConflict || err == scoring.ErrNotFound {
			return err
		}
		return badRequest(err)
	}
	if p.Done {
		s.events.Changed("runs", "metrics")
	}
	return ok(w, p)
}

func (s *Server) undo(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := s.scoring.Undo(r.Context(), id)
	if err != nil {
		if err == scoring.ErrNotFound {
			return err
		}
		return badRequest(err)
	}
	return ok(w, p)
}

func (s *Server) runStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	run, err := s.scoring.SetStatus(r.Context(), id, req.Status)
	if err != nil {
		if err == scoring.ErrNotFound {
			return err
		}
		return badRequest(err)
	}
	s.events.Changed("runs")
	return ok(w, run)
}
