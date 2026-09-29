package analysis

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"photobag/internal/bag"
	"photobag/internal/library"
	"photobag/internal/tagger"
)

// taggerOptions are the request options for the tagger.
func (s Settings) taggerOptions() tagger.Options {
	t := s.Danbooru.Tagger
	o := tagger.Options{GeneralThreshold: t.General.Threshold, CharacterThreshold: t.Character.Threshold, Characters: t.Character.Include}
	if !t.General.Include {
		o.GeneralThreshold = 1
	}
	return o
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// fromTagger keeps the tags the options ask for: character tags, then up
// to MaxTags general tags, and the rating.
func (s Settings) fromTagger(r *tagger.Result) library.AnalysisData {
	t := s.Danbooru.Tagger
	d := library.AnalysisData{Source: DanbooruFromTagger, Scores: map[string]float64{}}
	seen := map[string]bool{}
	add := func(tag tagger.Tag) {
		d.Tags = append(d.Tags, tag.Tag)
		d.Scores[tag.Tag] = round3(tag.Score)
		seen[tag.Tag] = true
	}
	if t.Character.Include {
		for _, c := range r.Characters {
			if c.Tag != "" && !seen[c.Tag] && c.Score >= t.Character.Threshold {
				add(c)
				d.Characters = append(d.Characters, c.Tag)
			}
		}
	}
	if t.General.Include {
		n := 0
		for _, g := range r.General {
			if n >= s.Danbooru.MaxTags {
				break
			}
			if g.Tag != "" && !seen[g.Tag] && g.Score >= t.General.Threshold {
				add(g)
				n++
			}
		}
	}
	if t.Rating.Include && r.Rating != nil && r.Rating.Tag != "" && r.Rating.Score >= t.Rating.Threshold {
		d.Rating, d.RatingScore = r.Rating.Tag, round3(r.Rating.Score)
	}
	return d
}

// danbooruText is the searchable text of a Danbooru result.
func danbooruText(d library.AnalysisData) string {
	parts := append([]string{}, d.Tags...)
	if d.Rating != "" {
		parts = append(parts, "rating:"+d.Rating)
	}
	return strings.Join(parts, ", ")
}

// kaomoji keep their underscores when tags are written with spaces.
var kaomoji = map[string]bool{
	"0_0": true, "(o)_(o)": true, "+_+": true, "+_-": true, "._.": true, "<o>_<o>": true, "<|>_<|>": true,
	"=_=": true, ">_<": true, "3_3": true, "6_9": true, ">_o": true, "@_@": true, "^_^": true, "o_o": true,
	"u_u": true, "x_x": true, "|_|": true, "||_||": true,
}

// label writes a Danbooru tag as a PhotoBag tag name (without prefix).
func (s Settings) label(tag string) string {
	if s.Danbooru.Spaces && !kaomoji[tag] {
		return strings.TrimSpace(strings.ReplaceAll(tag, "_", " "))
	}
	return tag
}

// danbooruNames lists the PhotoBag tags for a stored Danbooru result under
// the current options. Tagger results are filtered again, so categories
// can be dropped and thresholds raised without tagging again.
func (s Settings) danbooruNames(d library.AnalysisData) []string {
	o := s.Danbooru
	out := []string{}
	add := func(name string) {
		if bag.ValidateLabel(name) == nil {
			out = append(out, name)
		}
	}
	if d.Source != DanbooruFromTagger {
		for _, t := range d.Tags {
			add(o.Prefix + s.label(t))
		}
		return out
	}
	t := o.Tagger
	chars := map[string]bool{}
	for _, c := range d.Characters {
		chars[c] = true
	}
	general := 0
	for _, tag := range d.Tags {
		score, known := d.Scores[tag]
		if chars[tag] {
			if t.Character.Include && (!known || score >= t.Character.Threshold) {
				add(t.CharacterPrefix + s.label(tag))
			}
			continue
		}
		if t.General.Include && (!known || score >= t.General.Threshold) && general < o.MaxTags {
			general++
			add(o.Prefix + s.label(tag))
		}
	}
	if d.Rating != "" && t.Rating.Include && d.RatingScore >= t.Rating.Threshold {
		add(t.RatingPrefix + d.Rating)
	}
	return out
}

// runTagger tags one image with the tagger.
func (a *analyser) runTagger(ctx context.Context, img []byte) (*Outcome, error) {
	o := &Outcome{Pipeline: library.PipelineDanbooru, Model: a.clients.Tagger.Name()}
	start := time.Now()
	r, err := a.clients.Tagger.Tag(ctx, img, a.s.taggerOptions())
	o.Millis = time.Since(start).Milliseconds()
	if err != nil {
		o.Error = err.Error()
		return o, err
	}
	o.Model = a.clients.Tagger.Name()
	o.Reply = r.Raw
	d := a.s.fromTagger(r)
	o.data = &d
	o.Tags, o.Characters, o.Rating, o.RatingScore, o.Scores = d.Tags, d.Characters, d.Rating, d.RatingScore, d.Scores
	o.Text = danbooruText(d)
	if a.s.Danbooru.AddTags {
		o.TagNames, o.attach = a.s.danbooruNames(d), true
	}
	return o, nil
}

// TaggerCheck reports a tagger check.
type TaggerCheck struct {
	// OK means the tagger answered and tagged a test image.
	OK        bool     `json:"ok"`
	Message   string   `json:"message"`
	Name      string   `json:"name,omitempty"`
	OnGPU     bool     `json:"onGpu"`
	Providers []string `json:"providers"`
	TagCount  int      `json:"tagCount"`
	// Tags found in the test image.
	Tags   []string `json:"tags"`
	Millis int64    `json:"millis"`
	// StartMillis is how long starting the local tagger took, if it did.
	StartMillis int64 `json:"startMillis,omitempty"`
}

// CheckTagger asks the tagger for its status and tags a test image.
func CheckTagger(ctx context.Context, t tagger.Tagger) TaggerCheck {
	res := TaggerCheck{Providers: []string{}, Tags: []string{}}
	start := time.Now()
	h, err := t.Health(ctx)
	if err != nil {
		res.Message = err.Error()
		return res
	}
	if since := time.Since(start); since > 2*time.Second {
		res.StartMillis = since.Milliseconds()
	}
	res.Name, res.OnGPU, res.Providers, res.TagCount = t.Name(), h.OnGPU(), h.Providers, h.TagCount
	img, err := CheckImage("42")
	if err != nil {
		res.Message = err.Error()
		return res
	}
	begin := time.Now()
	r, err := t.Tag(ctx, img, tagger.Options{GeneralThreshold: 0.35, CharacterThreshold: 0.85, Characters: true})
	res.Millis = time.Since(begin).Milliseconds()
	if err != nil {
		res.Message = err.Error()
		return res
	}
	for _, g := range r.General {
		res.Tags = append(res.Tags, g.Tag)
	}
	where := "the CPU"
	if res.OnGPU {
		where = "the GPU"
	}
	res.OK = true
	res.Message = fmt.Sprintf("%s works, on %s (%d tags known).", res.Name, where, h.TagCount)
	return res
}
