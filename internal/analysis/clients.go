package analysis

import (
	"fmt"
	"time"

	"photobag/internal/llm"
	"photobag/internal/tagger"
)

// Clients are the services pipelines call.
type Clients struct {
	// Model is the vision model: every pipeline but Danbooru tags from a
	// tagger.
	Model *llm.Client
	// Tagger makes Danbooru tags when they come from a WD tagger.
	Tagger tagger.Tagger
}

func (c Clients) check(s Settings, pipelines []string) error {
	model, tag := s.Backends(pipelines)
	if model && c.Model == nil {
		return fmt.Errorf("no model endpoint is configured (Analysis → Connection)")
	}
	if tag && c.Tagger == nil {
		return fmt.Errorf("no tagger is configured (Analysis → Pipelines → Danbooru tags)")
	}
	return nil
}

// Clients builds what pipelines need: the model client (with apiKey) and
// the tagger (local, when the settings choose the local installation).
func (s Settings) Clients(apiKey string, local *tagger.Local, pipelines []string) (Clients, error) {
	var c Clients
	model, tag := s.Backends(pipelines)
	var err error
	if model {
		if c.Model, err = s.Client(apiKey); err != nil {
			return c, err
		}
	}
	if tag {
		if c.Tagger, err = s.TaggerClient(local); err != nil {
			return c, err
		}
	}
	return c, nil
}

// TaggerClient returns the tagger the settings choose.
func (s Settings) TaggerClient(local *tagger.Local) (tagger.Tagger, error) {
	t := s.Danbooru.Tagger
	if t.Local {
		if local == nil {
			return nil, fmt.Errorf("the local tagger is not available here")
		}
		if in := tagger.Detect(local.Dir); !in.Ready {
			return nil, fmt.Errorf("%s", in.Problem)
		}
		return local, nil
	}
	if t.Endpoint == "" {
		return nil, fmt.Errorf("no tagger address is set (Analysis → Pipelines → Danbooru tags)")
	}
	return tagger.New(t.Endpoint, time.Duration(s.TimeoutSeconds)*time.Second), nil
}
