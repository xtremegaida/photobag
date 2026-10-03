package server

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"photobag/internal/comfy"
	"photobag/internal/experiments"
	"photobag/internal/exporter"
	"photobag/internal/imaging"
	"photobag/internal/jobs"
	"photobag/internal/library"
)

// generateLane runs generation jobs beside everything else: they mostly
// wait for ComfyUI.
const generateLane = "generate"

func (s *Server) generateRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/comfy/settings", s.getComfySettings)
	s.handle(mux, "PUT /api/comfy/settings", s.putComfySettings)
	s.handle(mux, "POST /api/comfy/check", s.checkComfy)
	s.handle(mux, "GET /api/comfy/nodes", s.comfyNodes)

	s.handle(mux, "GET /api/workflows", s.listWorkflows)
	s.handle(mux, "POST /api/workflows", s.createWorkflow)
	s.handle(mux, "POST /api/workflows/parse", s.parseWorkflow)
	s.handle(mux, "GET /api/workflows/{id}", s.getWorkflow)
	s.handle(mux, "PATCH /api/workflows/{id}", s.patchWorkflow)
	s.handle(mux, "DELETE /api/workflows/{id}", s.deleteWorkflow)
	s.handle(mux, "GET /api/workflow-versions/{id}", s.getWorkflowVersion)

	s.handle(mux, "GET /api/experiments", s.listExperiments)
	s.handle(mux, "POST /api/experiments", s.createExperiment)
	s.handle(mux, "GET /api/experiments/{id}", s.getExperiment)
	s.handle(mux, "PATCH /api/experiments/{id}", s.patchExperiment)
	s.handle(mux, "DELETE /api/experiments/{id}", s.deleteExperiment)
	s.handle(mux, "GET /api/experiments/{id}/generations", s.listGenerations)
	s.handle(mux, "GET /api/experiments/{id}/runs", s.listGenerationRuns)
	s.handle(mux, "POST /api/experiments/{id}/plan", s.planGeneration)
	s.handle(mux, "POST /api/experiments/{id}/generate", s.startGeneration)

	s.handle(mux, "GET /api/generations/{id}", s.getGeneration)
	s.handle(mux, "GET /api/generations/{id}/preview", s.generationPreview)
	s.handle(mux, "GET /api/generations/{id}/original", s.generationOriginal)
	s.handle(mux, "GET /api/generations/{id}/workflow", s.generationWorkflow)
	s.handle(mux, "POST /api/generations/{id}/save-workflow", s.saveGenerationWorkflow)
	s.handle(mux, "POST /api/generations/move", s.moveGenerations)
	s.handle(mux, "POST /api/generations/discard", s.discardGenerations)
	s.handle(mux, "GET /api/generate/preview/{job}", s.samplerPreview)
}

// comfyClient returns a client for an address, or the stored one.
func (s *Server) comfyClient(ctx context.Context, endpoint string) (*comfy.Client, error) {
	if strings.TrimSpace(endpoint) == "" {
		set, err := experiments.LoadSettings(ctx, s.b)
		if err != nil {
			return nil, err
		}
		endpoint = set.Endpoint
	}
	if strings.TrimSpace(endpoint) == "" {
		return nil, apiErr(http.StatusBadRequest, "no ComfyUI address is set: add it under Generate → ComfyUI")
	}
	if err := comfy.ValidateEndpoint(endpoint); err != nil {
		return nil, badRequest(err)
	}
	return comfy.New(endpoint), nil
}

func (s *Server) getComfySettings(w http.ResponseWriter, r *http.Request) error {
	set, err := experiments.LoadSettings(r.Context(), s.b)
	if err != nil {
		return err
	}
	return ok(w, set)
}

func (s *Server) putComfySettings(w http.ResponseWriter, r *http.Request) error {
	var set experiments.Settings
	if err := readJSON(r, &set); err != nil {
		return err
	}
	set, err := experiments.SaveSettings(r.Context(), s.b, set)
	if err != nil {
		return badRequest(err)
	}
	s.nodeInfo.clear()
	s.events.Changed("comfy-settings")
	return ok(w, set)
}

type comfyCheck struct {
	OK      bool              `json:"ok"`
	Message string            `json:"message"`
	Info    *comfy.ServerInfo `json:"info,omitempty"`
}

// checkComfy reports on a ComfyUI server (the stored one, or an address
// being typed in).
func (s *Server) checkComfy(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	c, err := s.comfyClient(r.Context(), req.Endpoint)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	info, err := c.Info(ctx)
	if err != nil {
		return ok(w, comfyCheck{Message: err.Error()})
	}
	msg := "Connected to ComfyUI " + info.Version
	if len(info.Devices) > 0 {
		msg += " on " + info.Devices[0].Name
	}
	return ok(w, comfyCheck{OK: true, Message: msg, Info: info})
}

// nodeInfoCache remembers node definitions for a few minutes (ComfyUI
// takes a moment to list model files).
type nodeInfoCache struct {
	mu    sync.Mutex
	items map[string]nodeInfoEntry
}

type nodeInfoEntry struct {
	info *comfy.NodeInfo
	at   time.Time
}

func (c *nodeInfoCache) get(key string) (*comfy.NodeInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || time.Since(e.at) > 2*time.Minute {
		return nil, false
	}
	return e.info, true
}

func (c *nodeInfoCache) put(key string, ni *comfy.NodeInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]nodeInfoEntry{}
	}
	c.items[key] = nodeInfoEntry{info: ni, at: time.Now()}
}

func (c *nodeInfoCache) clear() {
	c.mu.Lock()
	c.items = nil
	c.mu.Unlock()
}

// comfyNodes returns the definitions of node classes (?class=A&class=B),
// which give input types, ranges and choices (models, samplers...).
func (s *Server) comfyNodes(w http.ResponseWriter, r *http.Request) error {
	type answer struct {
		Nodes   map[string]*comfy.NodeInfo `json:"nodes"`
		Missing []string                   `json:"missing"`
		Error   string                     `json:"error,omitempty"`
	}
	res := answer{Nodes: map[string]*comfy.NodeInfo{}, Missing: []string{}}
	c, err := s.comfyClient(r.Context(), "")
	if err != nil {
		res.Error = err.Error()
		return ok(w, res)
	}
	fresh := r.URL.Query().Get("fresh") != ""
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	for _, class := range r.URL.Query()["class"] {
		key := c.Endpoint() + "\x00" + class
		if ni, hit := s.nodeInfo.get(key); hit && !fresh {
			res.Nodes[class] = ni
			continue
		}
		ni, err := c.NodeInfo(ctx, class)
		if errors.Is(err, comfy.ErrUnknownNode) {
			res.Missing = append(res.Missing, class)
			continue
		}
		if err != nil {
			res.Error = err.Error()
			break
		}
		s.nodeInfo.put(key, ni)
		res.Nodes[class] = ni
	}
	return ok(w, res)
}

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) error {
	ws, err := experiments.ListWorkflows(r.Context(), s.b)
	if err != nil {
		return err
	}
	return ok(w, ws)
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) error {
	var c experiments.WorkflowChange
	if err := readJSON(r, &c); err != nil {
		return err
	}
	d, err := experiments.SaveWorkflow(r.Context(), s.b, 0, c)
	if err != nil {
		return err
	}
	s.events.Changed("workflows")
	return ok(w, d)
}

func (s *Server) parseWorkflow(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		JSON string `json:"json"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	v, err := experiments.ParseWorkflow(req.JSON)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := experiments.GetWorkflow(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	if r.URL.Query().Get("download") != "" {
		return sendJSONFile(w, d.Workflow.Name, d.Version.JSON)
	}
	return ok(w, d)
}

// sendJSONFile sends workflow JSON as a download.
func sendJSONFile(w http.ResponseWriter, name, js string) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment",
		map[string]string{"filename": exporter.SafeName(name, "") + ".json"}))
	w.Header().Set("Cache-Control", "no-store")
	_, err := w.Write([]byte(js))
	return err
}

func (s *Server) patchWorkflow(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var c experiments.WorkflowChange
	if err := readJSON(r, &c); err != nil {
		return err
	}
	d, err := experiments.SaveWorkflow(r.Context(), s.b, id, c)
	if err != nil {
		return err
	}
	s.events.Changed("workflows")
	return ok(w, d)
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := experiments.DeleteWorkflow(r.Context(), s.b, id); err != nil {
		return err
	}
	s.events.Changed("workflows")
	return ok(w, map[string]bool{"deleted": true})
}

func (s *Server) getWorkflowVersion(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	v, err := experiments.GetVersion(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	if r.URL.Query().Get("download") != "" {
		return sendJSONFile(w, "workflow version "+strconv.FormatInt(id, 10), v.JSON)
	}
	return ok(w, v)
}

// experimentView adds whether a generation is running.
type experimentView struct {
	*experiments.Experiment
	Job *jobs.Job `json:"job,omitempty"`
}

func (s *Server) runningGeneration(experimentID int64) *jobs.Job {
	s.genMu.Lock()
	id, ok := s.genJobs[experimentID]
	s.genMu.Unlock()
	if !ok {
		return nil
	}
	j, found := s.jobs.Get(id)
	if !found || (j.Status != jobs.Queued && j.Status != jobs.Running) {
		return nil
	}
	return &j
}

func (s *Server) listExperiments(w http.ResponseWriter, r *http.Request) error {
	es, err := experiments.ListExperiments(r.Context(), s.b)
	if err != nil {
		return err
	}
	out := make([]experimentView, len(es))
	for i := range es {
		out[i] = experimentView{Experiment: &es[i], Job: s.runningGeneration(es[i].ID)}
	}
	return ok(w, out)
}

func (s *Server) createExperiment(w http.ResponseWriter, r *http.Request) error {
	var c experiments.ExperimentChange
	if err := readJSON(r, &c); err != nil {
		return err
	}
	e, err := experiments.SaveExperiment(r.Context(), s.b, 0, c)
	if err != nil {
		return err
	}
	s.events.Changed("experiments")
	return ok(w, experimentView{Experiment: e})
}

func (s *Server) getExperiment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	e, err := experiments.GetExperiment(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	return ok(w, experimentView{Experiment: e, Job: s.runningGeneration(id)})
}

func (s *Server) patchExperiment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var c experiments.ExperimentChange
	if err := readJSON(r, &c); err != nil {
		return err
	}
	e, err := experiments.SaveExperiment(r.Context(), s.b, id, c)
	if err != nil {
		return err
	}
	if c.Name != nil || c.Notes != nil {
		s.events.Changed("experiments")
	}
	return ok(w, experimentView{Experiment: e, Job: s.runningGeneration(id)})
}

func (s *Server) deleteExperiment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if s.runningGeneration(id) != nil {
		return apiErr(http.StatusConflict, "images are being generated in this experiment: stop that first")
	}
	if err := experiments.DeleteExperiment(r.Context(), s.b, id); err != nil {
		return err
	}
	s.events.Changed("experiments", "generations", "images", "stats")
	return ok(w, map[string]bool{"deleted": true})
}

func (s *Server) listGenerations(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	gs, err := experiments.ListGenerations(r.Context(), s.b, id, r.URL.Query().Get("moved") != "")
	if err != nil {
		return err
	}
	return ok(w, gs)
}

func (s *Server) listGenerationRuns(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	runs, err := experiments.ListRuns(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	return ok(w, runs)
}

type generateRequest struct {
	Request experiments.Request `json:"request"`
}

func (s *Server) planGeneration(w http.ResponseWriter, r *http.Request) error {
	if _, err := pathID(r, "id"); err != nil {
		return err
	}
	var req generateRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	sum, err := experiments.Plan(r.Context(), s.b, req.Request)
	if err != nil {
		return err
	}
	return ok(w, sum)
}

func (s *Server) startGeneration(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req generateRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	e, err := experiments.GetExperiment(ctx, s.b, id)
	if err != nil {
		return err
	}
	if s.runningGeneration(id) != nil {
		return apiErr(http.StatusConflict, "this experiment is already generating: wait for it, or stop it")
	}
	sum, err := experiments.Plan(ctx, s.b, req.Request)
	if err != nil {
		return err
	}
	if sum.Error != "" {
		return apiErr(http.StatusBadRequest, "%s", sum.Error)
	}
	client, err := s.comfyClient(ctx, "")
	if err != nil {
		return err
	}
	title := fmt.Sprintf("Generate %d image%s in %s", sum.Prompts, map[bool]string{true: "", false: "s"}[sum.Prompts == 1], e.Name)
	idc := make(chan string, 1)
	job := s.jobs.SubmitTo(generateLane, "generate", title, func(ctx context.Context, report func(any, string)) (any, error) {
		jobID := <-idc
		defer s.previews.drop(jobID)
		var lastEmit time.Time
		res, err := experiments.Generate(ctx, s.b, id, req.Request, experiments.GenerateOptions{
			Client: client,
			JobID:  jobID,
			Log:    s.log,
			OnImage: func() {
				if time.Since(lastEmit) > time.Second {
					lastEmit = time.Now()
					s.events.Changed("generations")
				}
			},
			OnPreview: func(data []byte, format string) { s.previews.put(jobID, data, format) },
		}, func(p experiments.Progress) {
			msg := p.Current
			if p.Steps > 0 {
				msg = strings.TrimPrefix(fmt.Sprintf("%s · %s %d/%d", msg, p.Node, p.Step, p.Steps), " · ")
			}
			report(p, msg)
		})
		s.events.Changed("generations", "experiments")
		return res, err
	})
	idc <- job.ID
	s.genMu.Lock()
	s.genJobs[id] = job.ID
	s.genMu.Unlock()
	s.events.Changed("experiments")
	return ok(w, job)
}

// samplerPreviews keeps the latest sampler preview of each generation job.
type samplerPreviews struct {
	mu    sync.Mutex
	items map[string]samplerPreview
}

type samplerPreview struct {
	data   []byte
	format string
}

func (p *samplerPreviews) put(job string, data []byte, format string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.items == nil {
		p.items = map[string]samplerPreview{}
	}
	p.items[job] = samplerPreview{data: data, format: format}
}

func (p *samplerPreviews) drop(job string) {
	p.mu.Lock()
	delete(p.items, job)
	p.mu.Unlock()
}

func (s *Server) samplerPreview(w http.ResponseWriter, r *http.Request) error {
	s.previews.mu.Lock()
	pv, found := s.previews.items[r.PathValue("job")]
	s.previews.mu.Unlock()
	if !found {
		return apiErr(http.StatusNotFound, "no preview")
	}
	w.Header().Set("Content-Type", imaging.Format(pv.format).MIME())
	w.Header().Set("Cache-Control", "no-store")
	_, err := w.Write(pv.data)
	return err
}

// generationDetail adds whether the workflow template has changed since.
type generationDetail struct {
	*experiments.Generation
	// Current reports whether the image's workflow version is still the
	// template's current one.
	Current bool `json:"current"`
	// TemplateExists reports whether the template still exists.
	TemplateExists bool `json:"templateExists"`
}

func (s *Server) generationDetail(ctx context.Context, g *experiments.Generation) generationDetail {
	d := generationDetail{Generation: g}
	if g.WorkflowID != 0 {
		if wf, err := experiments.GetWorkflow(ctx, s.b, g.WorkflowID); err == nil {
			d.TemplateExists = true
			d.Current = wf.Workflow.VersionID == g.VersionID
		}
	}
	return d
}

func (s *Server) getGeneration(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	g, err := experiments.GetGeneration(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	return ok(w, s.generationDetail(r.Context(), g))
}

func (s *Server) generationPreview(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	blobID, format, err := experiments.Blob(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	return s.sendPreview(w, r, blobID, format)
}

func (s *Server) generationOriginal(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	g, err := experiments.GetGeneration(ctx, s.b, id)
	if err != nil {
		return err
	}
	blobID, format, err := experiments.Blob(ctx, s.b, id)
	if err != nil {
		return err
	}
	// Generation ids are used again after a discard, so the browser
	// revalidates against the content.
	sha, err := library.BlobSHA(ctx, s.b, blobID)
	if err != nil {
		return err
	}
	etag := `"` + sha + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	data, err := library.BlobDataBySHA(ctx, s.b, sha)
	if err != nil {
		return err
	}
	f := imaging.Format(format)
	disp := "inline"
	if r.URL.Query().Get("download") != "" {
		disp = "attachment"
	}
	name := fmt.Sprintf("%s %04d", strings.TrimSpace(cmpOr(g.ExperimentName, "ComfyUI")), g.ID)
	w.Header().Set("Content-Type", f.MIME())
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": exporter.SafeName(name, f)}))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, err = w.Write(data)
	return err
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// generationWorkflow returns the workflow exactly as the image was made.
func (s *Server) generationWorkflow(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	wf, g, err := experiments.Effective(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	if r.URL.Query().Get("download") != "" {
		return sendJSONFile(w, fmt.Sprintf("%s %04d", g.WorkflowName, g.ID), string(wf.JSON()))
	}
	return ok(w, map[string]string{"json": string(wf.JSON())})
}

func (s *Server) saveGenerationWorkflow(w http.ResponseWriter, r *http.Request) error {
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
	d, err := experiments.SaveEffective(r.Context(), s.b, id, req.Name)
	if err != nil {
		return err
	}
	s.events.Changed("workflows")
	return ok(w, d)
}

func (s *Server) moveGenerations(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		IDs  []int64  `json:"ids"`
		Tags []string `json:"tags"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	res, err := experiments.Move(r.Context(), s.b, req.IDs, req.Tags)
	if err != nil {
		return err
	}
	s.events.Changed("generations", "experiments", "images", "tags", "stats")
	return ok(w, res)
}

func (s *Server) discardGenerations(w http.ResponseWriter, r *http.Request) error {
	var req idsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	n, err := experiments.Discard(r.Context(), s.b, req.IDs)
	if err != nil {
		return err
	}
	s.events.Changed("generations", "experiments", "stats")
	return ok(w, map[string]int{"discarded": n})
}
