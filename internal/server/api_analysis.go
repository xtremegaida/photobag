package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"photobag/internal/analysis"
	"photobag/internal/library"
	"photobag/internal/llm"
	"photobag/internal/query"
)

// analysisLane runs analysis jobs beside imports and exports: they are
// long, and write little.
const analysisLane = "analysis"

type keyInfo struct {
	Set     bool   `json:"set"`
	Hint    string `json:"hint,omitempty"`
	FromEnv bool   `json:"fromEnv"`
	Env     string `json:"env"`
	Store   string `json:"store"`
}

func (s *Server) keyInfo(endpoint string) (keyInfo, error) {
	key, env, err := s.keys.Key(endpoint)
	return keyInfo{Set: key != "", Hint: analysis.KeyHint(key), FromEnv: env, Env: analysis.KeyEnv, Store: s.keys.Path}, err
}

func defaultPrompts() map[string]string {
	return map[string]string{
		"system":        analysis.DefaultSystemPrompt,
		"ocr":           analysis.DefaultOCRPrompt,
		"caption":       analysis.DefaultCaptionPrompt,
		"danbooru":      analysis.DefaultDanbooruPrompt,
		"categoryList":  analysis.DefaultCategoryPromptList,
		"categoryList2": analysis.DefaultCategoryPromptList2,
		"categoryFree":  analysis.DefaultCategoryPromptFree,
		"categoryFree2": analysis.DefaultCategoryPromptFree2,
	}
}

func (s *Server) settingsView(ctx context.Context, set analysis.Settings) (map[string]any, error) {
	ki, err := s.keyInfo(set.Endpoint)
	if err != nil {
		return nil, err
	}
	return map[string]any{"settings": set, "key": ki, "defaults": defaultPrompts()}, nil
}

func (s *Server) getAnalysisSettings(w http.ResponseWriter, r *http.Request) error {
	set, err := analysis.Load(r.Context(), s.b)
	if err != nil {
		return err
	}
	v, err := s.settingsView(r.Context(), set)
	if err != nil {
		return err
	}
	return ok(w, v)
}

// connectionRequest carries optional unsaved settings from the UI.
type connectionRequest struct {
	Settings *analysis.Settings `json:"settings"`
	// APIKey, when non-nil, replaces the stored key ("" forgets it).
	APIKey *string `json:"apiKey"`
}

func (s *Server) putAnalysisSettings(w http.ResponseWriter, r *http.Request) error {
	var req connectionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Settings == nil {
		return apiErr(http.StatusBadRequest, "settings are required")
	}
	set, err := analysis.Save(r.Context(), s.b, *req.Settings)
	if err != nil {
		return badRequest(err)
	}
	if req.APIKey != nil {
		if err := s.keys.SetKey(set.Endpoint, strings.TrimSpace(*req.APIKey)); err != nil {
			return fmt.Errorf("storing the API key: %w", err)
		}
	}
	s.events.Changed("analysis-settings")
	v, err := s.settingsView(r.Context(), set)
	if err != nil {
		return err
	}
	return ok(w, v)
}

// resolve returns the settings and API key for a request: unsaved values
// from the request, or the stored ones.
func (s *Server) resolve(ctx context.Context, req connectionRequest) (analysis.Settings, string, error) {
	var set analysis.Settings
	if req.Settings != nil {
		set = req.Settings.Normalized()
		if err := set.Validate(); err != nil {
			return set, "", badRequest(err)
		}
	} else {
		var err error
		if set, err = analysis.Load(ctx, s.b); err != nil {
			return set, "", err
		}
	}
	if req.APIKey != nil {
		return set, strings.TrimSpace(*req.APIKey), nil
	}
	key, _, err := s.keys.Key(set.Endpoint)
	return set, key, err
}

// modelClient returns the vision model client for a request.
func (s *Server) modelClient(ctx context.Context, req connectionRequest) (analysis.Settings, *llm.Client, error) {
	set, key, err := s.resolve(ctx, req)
	if err != nil {
		return set, nil, err
	}
	client, err := set.Client(key)
	if err != nil {
		return set, nil, badRequest(err)
	}
	return set, client, nil
}

// clients returns what pipelines need for a request.
func (s *Server) clients(ctx context.Context, req connectionRequest, pipelines []string) (analysis.Settings, analysis.Clients, error) {
	set, key, err := s.resolve(ctx, req)
	if err != nil {
		return set, analysis.Clients{}, err
	}
	c, err := set.Clients(key, s.tagger, pipelines)
	if err != nil {
		return set, c, badRequest(err)
	}
	return set, c, nil
}

func (s *Server) analysisModels(w http.ResponseWriter, r *http.Request) error {
	var req connectionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	_, client, err := s.modelClient(r.Context(), req)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	models, err := client.Models(ctx)
	if err != nil {
		return apiErr(http.StatusBadGateway, "%v", err)
	}
	return ok(w, map[string]any{"models": models})
}

func (s *Server) checkAnalysis(w http.ResponseWriter, r *http.Request) error {
	var req connectionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	set, client, err := s.modelClient(r.Context(), req)
	if err != nil {
		return err
	}
	return ok(w, analysis.Check(r.Context(), set, client))
}

// taggerStatus reports the local tagger installation and process.
func (s *Server) taggerStatus(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.tagger.Status())
}

// checkTagger checks the tagger the (unsaved) settings choose, starting
// the local one if needed.
func (s *Server) checkTagger(w http.ResponseWriter, r *http.Request) error {
	var req connectionRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	set, _, err := s.resolve(r.Context(), req)
	if err != nil {
		return err
	}
	t, err := set.TaggerClient(s.tagger)
	if err != nil {
		return badRequest(err)
	}
	return ok(w, analysis.CheckTagger(r.Context(), t))
}

// stopTagger stops the local tagger, freeing its memory.
func (s *Server) stopTagger(w http.ResponseWriter, r *http.Request) error {
	s.tagger.Stop()
	return ok(w, s.tagger.Status())
}

func (s *Server) analysisStats(w http.ResponseWriter, r *http.Request) error {
	st, err := library.AnalysisStats(r.Context(), s.b)
	if err != nil {
		return err
	}
	return ok(w, st)
}

func (s *Server) analysisPlan(w http.ResponseWriter, r *http.Request) error {
	var opts analysis.Options
	if err := readJSON(r, &opts); err != nil {
		return err
	}
	set, err := analysis.Load(r.Context(), s.b)
	if err != nil {
		return err
	}
	plan, err := analysis.MakePlan(r.Context(), s.b, set, opts)
	if err != nil {
		return badRequest(err)
	}
	return ok(w, plan)
}

func (s *Server) startAnalysis(w http.ResponseWriter, r *http.Request) error {
	var opts analysis.Options
	if err := readJSON(r, &opts); err != nil {
		return err
	}
	if err := opts.Validate(); err != nil {
		return badRequest(err)
	}
	set, clients, err := s.clients(r.Context(), connectionRequest{}, opts.Pipelines)
	if err != nil {
		return err
	}
	title := fmt.Sprintf("Analyse %s (%s)", opts.Query.Describe(), strings.Join(opts.Pipelines, ", "))
	job := s.jobs.SubmitTo(analysisLane, "analyze", title, func(ctx context.Context, report func(any, string)) (any, error) {
		plan, err := analysis.MakePlan(ctx, s.b, set, opts)
		if err != nil {
			return nil, err
		}
		tags := false
		for _, p := range opts.Pipelines {
			tags = tags || p == library.PipelineCategory || (p == library.PipelineDanbooru && set.Danbooru.AddTags)
		}
		topics := []string{"analysis"}
		if tags {
			topics = append(topics, "tags")
		}
		var lastEmit time.Time
		stored := 0
		rep, err := analysis.Run(ctx, s.b, set, clients, plan, opts, func(p analysis.Progress) {
			report(p, p.Current)
			if p.Stored > stored && time.Since(lastEmit) > 2*time.Second {
				stored, lastEmit = p.Stored, time.Now()
				s.events.Changed(topics...)
			}
		})
		s.events.Changed(topics...)
		return rep, err
	})
	return ok(w, job)
}

type imageAnalysisRequest struct {
	connectionRequest
	ImageID   int64    `json:"imageId"`
	Pipelines []string `json:"pipelines"`
}

// tryAnalysis runs pipelines on one image without storing anything, so
// prompts can be tuned. Without an image id a random image is used.
func (s *Server) tryAnalysis(w http.ResponseWriter, r *http.Request) error {
	var req imageAnalysisRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	set, clients, err := s.clients(ctx, req.connectionRequest, req.Pipelines)
	if err != nil {
		return err
	}
	id := req.ImageID
	if id == 0 {
		ids, err := library.ListIDs(ctx, s.b, query.ImageQuery{}, query.Sort{Field: query.SortRandom, Seed: time.Now().UnixNano() % 1e9})
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return apiErr(http.StatusBadRequest, "the library is empty")
		}
		id = ids[0]
	}
	out, err := analysis.RunOne(ctx, s.b, set, clients, id, req.Pipelines, false)
	if err != nil && out == nil {
		return badRequest(err)
	}
	return ok(w, map[string]any{"imageId": id, "outcomes": out})
}

// analyzeImage runs pipelines on one image now and stores the results.
func (s *Server) analyzeImage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req imageAnalysisRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	set, clients, err := s.clients(ctx, connectionRequest{}, req.Pipelines)
	if err != nil {
		return err
	}
	out, err := analysis.RunOne(ctx, s.b, set, clients, id, req.Pipelines, true)
	s.events.Changed("analysis", "tags")
	if err != nil && out == nil {
		if err == library.ErrNotFound {
			return err
		}
		return badRequest(err)
	}
	return ok(w, map[string]any{"outcomes": out})
}

func (s *Server) editAnalysis(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := library.EditAnalysis(r.Context(), s.b, id, r.PathValue("pipeline"), req.Text); err != nil {
		if err == library.ErrNotFound {
			return err
		}
		return badRequest(err)
	}
	s.events.Changed("analysis")
	as, err := library.Analyses(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	return ok(w, as)
}

func (s *Server) deleteAnalysis(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := library.DeleteAnalysis(r.Context(), s.b, id, r.PathValue("pipeline")); err != nil {
		return err
	}
	s.events.Changed("analysis", "tags")
	return ok(w, map[string]bool{"ok": true})
}

// retagAnalyses re-applies Danbooru or category tags from stored results
// with the saved options.
func (s *Server) retagAnalyses(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Pipeline string `json:"pipeline"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Pipeline != library.PipelineDanbooru && req.Pipeline != library.PipelineCategory {
		return apiErr(http.StatusBadRequest, "only Danbooru and category results add tags")
	}
	set, err := analysis.Load(r.Context(), s.b)
	if err != nil {
		return err
	}
	title := "Update Danbooru tags from stored results"
	if req.Pipeline == library.PipelineCategory {
		title = "Update category tags from stored results"
	}
	job := s.jobs.SubmitTo(analysisLane, "retag", title, func(ctx context.Context, report func(any, string)) (any, error) {
		n, err := analysis.Retag(ctx, s.b, set, req.Pipeline, func(done, total int) {
			report(map[string]int{"done": done, "total": total}, "")
		})
		s.events.Changed("tags", "images", "analysis")
		return map[string]any{"updated": n, "pipeline": req.Pipeline, "removed": req.Pipeline == library.PipelineDanbooru && !set.Danbooru.AddTags}, err
	})
	return ok(w, job)
}

func (s *Server) removeAnalysisTags(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Pipeline string `json:"pipeline"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if !library.ValidPipeline(req.Pipeline) {
		return apiErr(http.StatusBadRequest, "unknown pipeline %q", req.Pipeline)
	}
	n, err := library.RemoveSourceTags(r.Context(), s.b, req.Pipeline)
	if err != nil {
		return err
	}
	s.events.Changed("tags", "images", "analysis")
	return ok(w, map[string]int{"removed": n})
}
