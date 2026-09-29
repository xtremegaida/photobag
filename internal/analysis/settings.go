// Package analysis runs images through a vision-language model behind an
// OpenAI-compatible API: OCR, captions, Danbooru tags and categories.
// Danbooru tags can come from a WD tagger instead.
package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"photobag/internal/bag"
	"photobag/internal/library"
	"photobag/internal/llm"
	"photobag/internal/tagger"
)

// Settings configure the model connection and the pipelines. They are
// stored in the bag (meta key "analysis.settings"); the API key is not.
type Settings struct {
	// Endpoint is the API base URL, e.g. http://127.0.0.1:1234/v1.
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	// MaxTokens caps each reply, including any reasoning.
	MaxTokens int `json:"maxTokens"`
	// Temperature is sent when set.
	Temperature *float64 `json:"temperature,omitempty"`
	// Concurrency is how many requests run at once.
	Concurrency    int `json:"concurrency"`
	TimeoutSeconds int `json:"timeoutSeconds"`
	// ImageSize is the long side, in pixels, of the JPEG sent to the model.
	ImageSize int `json:"imageSize"`
	// Extra is a JSON object merged into every request body.
	Extra string `json:"extra"`
	// SystemPrompt replaces the default system message when set.
	SystemPrompt string `json:"systemPrompt"`

	OCR      TextOptions     `json:"ocr"`
	Caption  TextOptions     `json:"caption"`
	Danbooru DanbooruOptions `json:"danbooru"`
	Category CategoryOptions `json:"category"`
}

// TextOptions configure the OCR and caption pipelines.
type TextOptions struct {
	// Prompt replaces the default instructions when set.
	Prompt string `json:"prompt"`
}

// Where Danbooru tags come from.
const (
	DanbooruFromModel  = "model"  // the vision model
	DanbooruFromTagger = "tagger" // a WD tagger server
)

// DanbooruOptions configure the Danbooru tag pipeline.
type DanbooruOptions struct {
	// Source is DanbooruFromModel or DanbooruFromTagger.
	Source string `json:"source"`
	Prompt string `json:"prompt"`
	// MaxTags limits the (general) tags per image.
	MaxTags int `json:"maxTags"`
	// AddTags also attaches the tags to the image as PhotoBag tags.
	AddTags bool `json:"addTags"`
	// Prefix is prepended to PhotoBag tag names, e.g. "db:" (for a tagger,
	// to general tags; see TaggerOptions for the others).
	Prefix string `json:"prefix"`
	// Spaces writes PhotoBag tag names with spaces instead of underscores.
	Spaces bool          `json:"spaces"`
	Tagger TaggerOptions `json:"tagger"`
}

// TaggerOptions configure Danbooru tags from a WD tagger.
type TaggerOptions struct {
	// Local uses the tagger installed with "photobag tagger install";
	// otherwise the server at Endpoint (http://host:port).
	Local    bool   `json:"local"`
	Endpoint string `json:"endpoint"`
	// General, character and rating tags: whether to keep them, and the
	// confidence they need.
	General   TagFilter `json:"general"`
	Character TagFilter `json:"character"`
	Rating    TagFilter `json:"rating"`
	// CharacterPrefix and RatingPrefix are prepended to the PhotoBag tag
	// names of character and rating tags (general tags use
	// DanbooruOptions.Prefix).
	CharacterPrefix string `json:"characterPrefix"`
	RatingPrefix    string `json:"ratingPrefix"`
}

// TagFilter chooses the tags of one tagger category.
type TagFilter struct {
	Include bool `json:"include"`
	// Threshold is the minimum confidence, 0–1.
	Threshold float64 `json:"threshold"`
}

// CategoryOptions configure the category pipeline.
type CategoryOptions struct {
	Prompt string `json:"prompt"`
	// Levels is 1 for a single category, 2 for a main and a sub category.
	Levels int `json:"levels"`
	// Categories lists the allowed categories, one per line, as "Main" or
	// "Main: Sub, Sub". When empty the model names categories itself.
	Categories string `json:"categories"`
	// Prefix is prepended to the category tags.
	Prefix string `json:"prefix"`
}

// Defaults returns the default settings.
func Defaults() Settings {
	return Settings{
		MaxTokens:      4096,
		Concurrency:    2,
		TimeoutSeconds: 300,
		ImageSize:      1024,
		Danbooru: DanbooruOptions{Source: DanbooruFromModel, MaxTags: 30, Spaces: true, Tagger: TaggerOptions{
			General:      TagFilter{Include: true, Threshold: 0.35},
			Character:    TagFilter{Include: true, Threshold: 0.85},
			Rating:       TagFilter{Include: true},
			RatingPrefix: "rating:",
		}},
		Category: CategoryOptions{Levels: 1},
	}
}

const metaKey = "analysis.settings"

// Load reads the bag's settings (defaults for anything unset).
func Load(ctx context.Context, b *bag.Bag) (Settings, error) {
	s := Defaults()
	raw, err := b.Meta(ctx, metaKey)
	if err != nil {
		return s, err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return Defaults(), fmt.Errorf("stored analysis settings are unreadable: %w", err)
		}
	}
	s.normalize()
	return s, nil
}

// Save validates and stores settings.
func Save(ctx context.Context, b *bag.Bag, s Settings) (Settings, error) {
	s.normalize()
	if err := s.Validate(); err != nil {
		return s, err
	}
	js, _ := json.Marshal(s)
	return s, b.SetMeta(ctx, metaKey, string(js))
}

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	return max(lo, min(hi, v))
}

// Normalized returns s with defaults for unset values and limits applied.
func (s Settings) Normalized() Settings {
	s.normalize()
	return s
}

func (s *Settings) normalize() {
	d := Defaults()
	s.Endpoint = llm.NormalizeEndpoint(s.Endpoint)
	s.Model = strings.TrimSpace(s.Model)
	s.MaxTokens = clamp(s.MaxTokens, 16, 1<<17, d.MaxTokens)
	s.Concurrency = clamp(s.Concurrency, 1, 32, d.Concurrency)
	s.TimeoutSeconds = clamp(s.TimeoutSeconds, 10, 3600, d.TimeoutSeconds)
	s.ImageSize = clamp(s.ImageSize, 256, 4096, d.ImageSize)
	s.Extra = strings.TrimSpace(s.Extra)
	s.Danbooru.MaxTags = clamp(s.Danbooru.MaxTags, 1, 200, d.Danbooru.MaxTags)
	s.Category.Levels = clamp(s.Category.Levels, 1, 2, 1)
	s.Danbooru.Prefix = strings.TrimLeft(s.Danbooru.Prefix, " ")
	s.Category.Prefix = strings.TrimLeft(s.Category.Prefix, " ")
	if s.Danbooru.Source != DanbooruFromTagger {
		s.Danbooru.Source = DanbooruFromModel
	}
	t := &s.Danbooru.Tagger
	t.Endpoint = tagger.NormalizeEndpoint(t.Endpoint)
	t.General.Threshold = clampScore(t.General.Threshold, 0.01)
	t.Character.Threshold = clampScore(t.Character.Threshold, 0.01)
	t.Rating.Threshold = clampScore(t.Rating.Threshold, 0)
	t.CharacterPrefix = strings.TrimLeft(t.CharacterPrefix, " ")
	t.RatingPrefix = strings.TrimLeft(t.RatingPrefix, " ")
}

// clampScore keeps a threshold within lo–1, rounded to 0.001.
func clampScore(v, lo float64) float64 {
	return math.Round(max(lo, min(1, v))*1000) / 1000
}

// Validate checks settings for mistakes a person should fix.
func (s Settings) Validate() error {
	if s.Endpoint != "" {
		u, err := url.Parse(s.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("the endpoint must be an http:// or https:// URL, such as http://127.0.0.1:1234/v1")
		}
	}
	if s.Temperature != nil && (*s.Temperature < 0 || *s.Temperature > 2) {
		return fmt.Errorf("temperature must be between 0 and 2")
	}
	if _, err := s.extra(); err != nil {
		return err
	}
	if e := s.Danbooru.Tagger.Endpoint; e != "" {
		if err := tagger.ValidateEndpoint(e); err != nil {
			return err
		}
	}
	for _, p := range []string{s.Danbooru.Prefix, s.Category.Prefix, s.Danbooru.Tagger.CharacterPrefix, s.Danbooru.Tagger.RatingPrefix} {
		if len([]rune(p)) > 40 {
			return fmt.Errorf("tag prefixes must be at most 40 characters")
		}
	}
	return nil
}

func (s Settings) extra() (map[string]any, error) {
	if s.Extra == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s.Extra), &m); err != nil {
		return nil, fmt.Errorf("extra request parameters must be a JSON object: %v", err)
	}
	delete(m, "messages")
	delete(m, "stream")
	return m, nil
}

// Configured reports whether an endpoint is set.
func (s Settings) Configured() bool { return s.Endpoint != "" }

// UsesTagger reports whether a pipeline uses the tagger instead of the
// vision model.
func (s Settings) UsesTagger(pipeline string) bool {
	return pipeline == library.PipelineDanbooru && s.Danbooru.Source == DanbooruFromTagger
}

// Backends reports whether pipelines need the vision model and the tagger.
func (s Settings) Backends(pipelines []string) (model, tagger bool) {
	for _, p := range pipelines {
		if s.UsesTagger(p) {
			tagger = true
		} else {
			model = true
		}
	}
	return model, tagger
}

// Client builds an API client from the settings.
func (s Settings) Client(apiKey string) (*llm.Client, error) {
	if !s.Configured() {
		return nil, fmt.Errorf("no model endpoint is configured (Analysis → Connection)")
	}
	extra, err := s.extra()
	if err != nil {
		return nil, err
	}
	return llm.New(llm.Config{
		Endpoint: s.Endpoint, APIKey: apiKey, Model: s.Model,
		MaxTokens: s.MaxTokens, Temperature: s.Temperature, Extra: extra,
		Timeout: time.Duration(s.TimeoutSeconds) * time.Second, Retries: 3,
	}), nil
}
