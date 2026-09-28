package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"photobag/internal/bag"
)

// Analysis pipelines: what a vision-language model is asked about an image.
const (
	PipelineOCR      = "ocr"      // text found in the image
	PipelineCaption  = "caption"  // a short description for people who cannot see it
	PipelineDanbooru = "danbooru" // Danbooru-style tags
	PipelineCategory = "category" // one category, or a main and a sub category
)

// Pipelines lists every pipeline in display order.
var Pipelines = []string{PipelineCaption, PipelineOCR, PipelineDanbooru, PipelineCategory}

// ValidPipeline reports whether p names a pipeline.
func ValidPipeline(p string) bool {
	for _, q := range Pipelines {
		if p == q {
			return true
		}
	}
	return false
}

// Analysis is the current result of one pipeline for one image.
type Analysis struct {
	Pipeline string `json:"pipeline"`
	// Text is the result as plain text: the caption, the text found in the
	// image, the Danbooru tags joined with ", ", or "Main / Sub". It is
	// empty when the pipeline found nothing (e.g. no legible text).
	Text string `json:"text"`
	// Tags are the Danbooru tags, in the model's order.
	Tags []string `json:"tags,omitempty"`
	// Main and Sub are the category (Sub only with two levels).
	Main string `json:"main,omitempty"`
	Sub  string `json:"sub,omitempty"`
	// Model that produced the result.
	Model string `json:"model"`
	// Edited is set once a person corrected the result; analysis jobs
	// never overwrite edited results.
	Edited    bool  `json:"edited"`
	UpdatedAt int64 `json:"updatedAt"`
}

// AnalysisData is the pipeline-specific part stored as JSON.
type AnalysisData struct {
	Tags []string `json:"tags,omitempty"`
	Main string   `json:"main,omitempty"`
	Sub  string   `json:"sub,omitempty"`
}

// Analyses returns an image's analysis results in pipeline order.
func Analyses(ctx context.Context, b *bag.Bag, imageID int64) ([]Analysis, error) {
	m, err := AnalysesFor(ctx, b, []int64{imageID})
	if err != nil {
		return nil, err
	}
	if m[imageID] == nil {
		return []Analysis{}, nil
	}
	return m[imageID], nil
}

// AnalysesFor returns the analysis results of several images.
func AnalysesFor(ctx context.Context, b *bag.Bag, ids []int64) (map[int64][]Analysis, error) {
	out := map[int64][]Analysis{}
	if len(ids) == 0 {
		return out, nil
	}
	js, _ := json.Marshal(ids)
	rows, err := b.R.QueryContext(ctx, `SELECT image_id, pipeline, text, COALESCE(data, ''), model, edited, updated_at
		FROM analyses WHERE image_id IN (SELECT value FROM json_each(?))`, string(js))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var a Analysis
		var data string
		if err := rows.Scan(&id, &a.Pipeline, &a.Text, &data, &a.Model, &a.Edited, &a.UpdatedAt); err != nil {
			return nil, err
		}
		if data != "" {
			var d AnalysisData
			if json.Unmarshal([]byte(data), &d) == nil {
				a.Tags, a.Main, a.Sub = d.Tags, d.Main, d.Sub
			}
		}
		out[id] = append(out[id], a)
	}
	rank := map[string]int{}
	for i, p := range Pipelines {
		rank[p] = i
	}
	for _, list := range out {
		sort.Slice(list, func(i, j int) bool { return rank[list[i].Pipeline] < rank[list[j].Pipeline] })
	}
	return out, rows.Err()
}

// AnalysisWrite stores one pipeline result.
type AnalysisWrite struct {
	ImageID  int64
	Pipeline string
	Text     string
	Data     *AnalysisData
	Model    string
	// Config identifies the prompt and options that produced the result.
	Config string
	// Tags, when non-nil, replaces the tags this pipeline has attached to
	// the image (tags a person added are never removed).
	Tags []string
	// Force replaces a result a person edited (for an explicit re-run).
	Force bool
}

// WriteAnalysis stores a machine result. It reports false (and changes
// nothing) when the image's current result was edited by a person (unless
// Force is set) or the image has been purged.
func WriteAnalysis(ctx context.Context, b *bag.Bag, w AnalysisWrite) (bool, error) {
	var data any
	if w.Data != nil {
		js, _ := json.Marshal(w.Data)
		data = string(js)
	}
	written := false
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		var edited int
		err := tx.QueryRowContext(ctx, "SELECT edited FROM analyses WHERE image_id = ? AND pipeline = ?", w.ImageID, w.Pipeline).Scan(&edited)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if edited != 0 && !w.Force {
			return nil
		}
		now := bag.NowMillis()
		res, err := tx.ExecContext(ctx, `INSERT INTO analyses(image_id, pipeline, text, data, model, config, created_at, updated_at)
			SELECT id, ?, ?, ?, ?, ?, ?, ? FROM images WHERE id = ? AND purged_at IS NULL
			ON CONFLICT(image_id, pipeline) DO UPDATE SET text = excluded.text, data = excluded.data,
				model = excluded.model, config = excluded.config, edited = 0, updated_at = excluded.updated_at`,
			w.Pipeline, w.Text, data, w.Model, w.Config, now, now, w.ImageID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		written = true
		if w.Tags != nil {
			return SetSourceTags(ctx, tx, w.ImageID, w.Pipeline, w.Tags)
		}
		return nil
	})
	return written, err
}

// EditAnalysis replaces a result's text with a person's correction. Only
// free-text pipelines (caption, OCR) can be edited; tags are edited as tags.
func EditAnalysis(ctx context.Context, b *bag.Bag, imageID int64, pipeline, text string) error {
	if pipeline != PipelineCaption && pipeline != PipelineOCR {
		return fmt.Errorf("%s results cannot be edited as text; edit the image's tags instead", pipeline)
	}
	text = strings.TrimSpace(text)
	if len(text) > 100_000 {
		return fmt.Errorf("text is too long")
	}
	return b.Tx(ctx, func(tx *sql.Tx) error {
		now := bag.NowMillis()
		res, err := tx.ExecContext(ctx, `INSERT INTO analyses(image_id, pipeline, text, model, edited, created_at, updated_at)
			SELECT id, ?, ?, '', 1, ?, ? FROM images WHERE id = ? AND purged_at IS NULL
			ON CONFLICT(image_id, pipeline) DO UPDATE SET text = excluded.text, edited = 1, updated_at = excluded.updated_at`,
			pipeline, text, now, now, imageID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// DeleteAnalysis removes a result and the tags its pipeline attached to the image.
func DeleteAnalysis(ctx context.Context, b *bag.Bag, imageID int64, pipeline string) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM analyses WHERE image_id = ? AND pipeline = ?", imageID, pipeline); err != nil {
			return err
		}
		return SetSourceTags(ctx, tx, imageID, pipeline, []string{})
	})
}

// PipelineStats counts results of one pipeline over active images.
type PipelineStats struct {
	Pipeline string `json:"pipeline"`
	// Analysed images have a result; Empty ones found nothing (no text).
	Analysed int `json:"analysed"`
	Empty    int `json:"empty"`
	Edited   int `json:"edited"`
	// TagLinks is how many tags this pipeline has attached to images.
	TagLinks int `json:"tagLinks"`
}

// AnalysisStats returns per-pipeline counts, in pipeline order.
func AnalysisStats(ctx context.Context, b *bag.Bag) ([]PipelineStats, error) {
	byName := map[string]*PipelineStats{}
	out := make([]PipelineStats, len(Pipelines))
	for i, p := range Pipelines {
		out[i].Pipeline = p
		byName[p] = &out[i]
	}
	rows, err := b.R.QueryContext(ctx, `SELECT a.pipeline, count(*), count(CASE WHEN a.text = '' THEN 1 END), sum(a.edited)
		FROM analyses a JOIN images i ON i.id = a.image_id
		WHERE i.deleted_at IS NULL AND i.purged_at IS NULL GROUP BY a.pipeline`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p string
		var s PipelineStats
		if err := rows.Scan(&p, &s.Analysed, &s.Empty, &s.Edited); err != nil {
			rows.Close()
			return nil, err
		}
		if t := byName[p]; t != nil {
			t.Analysed, t.Empty, t.Edited = s.Analysed, s.Empty, s.Edited
		}
	}
	rows.Close()
	rows, err = b.R.QueryContext(ctx, `SELECT it.source, count(*) FROM image_tags it JOIN images i ON i.id = it.image_id
		WHERE it.source IS NOT NULL AND i.deleted_at IS NULL AND i.purged_at IS NULL GROUP BY it.source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		if t := byName[p]; t != nil {
			t.TagLinks = n
		}
	}
	return out, rows.Err()
}

// ExistingCategories returns the categories already assigned, most used
// first: "Main" entries, and "Main / Sub" entries when subs exist.
func ExistingCategories(ctx context.Context, b *bag.Bag, limit int) ([]string, error) {
	rows, err := b.R.QueryContext(ctx, `SELECT text, count(*) AS n FROM analyses
		WHERE pipeline = ? AND text <> '' GROUP BY text ORDER BY n DESC, text LIMIT ?`, PipelineCategory, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
