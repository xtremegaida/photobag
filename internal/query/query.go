// Package query defines ImageQuery, the single selection abstraction used by
// the gallery, export, scoring runs and dedup scope, and compiles it to SQL.
package query

import (
	"encoding/json"
	"fmt"
	"strings"

	"photobag/internal/bag"
)

// Scope selects which lifecycle state images must be in.
type Scope string

const (
	Active Scope = "active" // default: not trashed, not purged
	Trash  Scope = "trash"  // trashed but not yet purged
)

// ImageQuery selects images. All present criteria must hold.
type ImageQuery struct {
	TagsAll  []string `json:"tagsAll,omitempty"`
	TagsAny  []string `json:"tagsAny,omitempty"`
	TagsNone []string `json:"tagsNone,omitempty"`
	Untagged bool     `json:"untagged,omitempty"`
	// NameGlob is a case-insensitive glob on the image name. Plain text
	// without * ? [ matches as a substring.
	NameGlob string `json:"nameGlob,omitempty"`
	// Text searches analysis results (captions, text found in images,
	// Danbooru tags, categories), ignoring case. Every word must occur;
	// "quoted phrases" match as a whole.
	Text string `json:"text,omitempty"`
	// IDs restricts the result to these images (a UI selection).
	IDs   []int64 `json:"ids,omitempty"`
	Scope Scope   `json:"scope,omitempty"`
}

// Validate checks the query for malformed parts.
func (q ImageQuery) Validate() error {
	switch q.Scope {
	case "", Active, Trash:
	default:
		return fmt.Errorf("unknown scope %q", q.Scope)
	}
	if q.NameGlob != "" {
		return bag.ValidateGlob(q.glob())
	}
	return nil
}

func (q ImageQuery) glob() string {
	g := strings.TrimSpace(q.NameGlob)
	if g != "" && !bag.HasGlobMeta(g) {
		g = "*" + g + "*"
	}
	return g
}

// IsEmpty reports whether the query has no filters (selects the whole scope).
func (q ImageQuery) IsEmpty() bool {
	return len(q.TagsAll) == 0 && len(q.TagsAny) == 0 && len(q.TagsNone) == 0 &&
		!q.Untagged && strings.TrimSpace(q.NameGlob) == "" && len(q.IDs) == 0 && len(SearchTerms(q.Text)) == 0
}

// SearchTerms splits a search string into words and "quoted phrases".
func SearchTerms(s string) []string {
	var out []string
	for i, part := range strings.Split(s, `"`) {
		if i%2 == 1 { // inside quotes
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
			continue
		}
		out = append(out, strings.Fields(part)...)
	}
	return out
}

func keys(names []string) []any {
	out := make([]any, 0, len(names))
	for _, n := range names {
		if k := bag.LabelKey(n); k != "" {
			out = append(out, k)
		}
	}
	return out
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// Where compiles the query to a SQL boolean expression over the images
// table aliased as alias.
func (q ImageQuery) Where(alias string) (string, []any) {
	a := alias + "."
	var conds []string
	var args []any
	switch q.Scope {
	case Trash:
		conds = append(conds, a+"deleted_at IS NOT NULL AND "+a+"purged_at IS NULL")
	default:
		conds = append(conds, a+"deleted_at IS NULL AND "+a+"purged_at IS NULL")
	}
	tagExists := "EXISTS (SELECT 1 FROM image_tags it JOIN tags t ON t.id = it.tag_id WHERE it.image_id = " + a + "id AND t.key "
	for _, k := range keys(q.TagsAll) {
		conds = append(conds, tagExists+"= ?)")
		args = append(args, k)
	}
	if ks := keys(q.TagsAny); len(ks) > 0 {
		conds = append(conds, tagExists+"IN ("+placeholders(len(ks))+"))")
		args = append(args, ks...)
	}
	if ks := keys(q.TagsNone); len(ks) > 0 {
		conds = append(conds, "NOT "+tagExists+"IN ("+placeholders(len(ks))+"))")
		args = append(args, ks...)
	}
	if q.Untagged {
		conds = append(conds, "NOT EXISTS (SELECT 1 FROM image_tags it WHERE it.image_id = "+a+"id)")
	}
	if g := q.glob(); g != "" {
		conds = append(conds, "pb_glob(?, "+a+"name)")
		args = append(args, g)
	}
	for _, term := range SearchTerms(q.Text) {
		conds = append(conds, "EXISTS (SELECT 1 FROM analyses an WHERE an.image_id = "+a+"id AND pb_contains(an.text, ?))")
		args = append(args, term)
	}
	if len(q.IDs) > 0 {
		js, _ := json.Marshal(q.IDs)
		conds = append(conds, a+"id IN (SELECT value FROM json_each(?))")
		args = append(args, string(js))
	}
	return strings.Join(conds, " AND "), args
}

// Describe renders a short human-readable summary.
func (q ImageQuery) Describe() string {
	var parts []string
	if q.Scope == Trash {
		parts = append(parts, "in trash")
	}
	if len(q.IDs) > 0 {
		parts = append(parts, fmt.Sprintf("%d selected", len(q.IDs)))
	}
	if len(q.TagsAll) > 0 {
		parts = append(parts, "tagged "+strings.Join(q.TagsAll, " & "))
	}
	if len(q.TagsAny) > 0 {
		parts = append(parts, "any of "+strings.Join(q.TagsAny, " | "))
	}
	if len(q.TagsNone) > 0 {
		parts = append(parts, "not "+strings.Join(q.TagsNone, ", "))
	}
	if q.Untagged {
		parts = append(parts, "untagged")
	}
	if g := strings.TrimSpace(q.NameGlob); g != "" {
		parts = append(parts, "name "+q.glob())
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		parts = append(parts, fmt.Sprintf("described as %q", t))
	}
	if len(parts) == 0 {
		return "all images"
	}
	return strings.Join(parts, ", ")
}

// Sort fields.
const (
	SortImported = "imported"
	SortTaken    = "taken"
	SortName     = "name"
	SortSize     = "size"
	SortPixels   = "pixels"
	SortScore    = "score"
	SortRandom   = "random"
	// SortSimilar chains visually similar images together; with SimilarTo
	// it orders by similarity to that image. Both are computed in Go.
	SortSimilar = "similar"
)

// Sort orders a gallery listing.
type Sort struct {
	Field     string `json:"field,omitempty"`
	Desc      bool   `json:"desc,omitempty"`
	MetricID  int64  `json:"metricId,omitempty"`
	SimilarTo int64  `json:"similarTo,omitempty"`
	Seed      int64  `json:"seed,omitempty"`
}

// Validate checks the sort.
func (s Sort) Validate() error {
	switch s.Field {
	case "", SortImported, SortTaken, SortName, SortSize, SortPixels, SortRandom, SortSimilar:
	case SortScore:
		if s.MetricID == 0 {
			return fmt.Errorf("score sort needs a metric")
		}
	default:
		return fmt.Errorf("unknown sort field %q", s.Field)
	}
	return nil
}

// SQL returns an optional join and the ORDER BY clause (without keyword)
// for images aliased as alias. Similarity sorts return the import order;
// callers reorder in Go.
func (s Sort) SQL(alias string) (join string, order string, args []any) {
	a := alias + "."
	dir := " ASC"
	if s.Desc {
		dir = " DESC"
	}
	switch s.Field {
	case SortTaken:
		// Undated images sort last either way.
		return "", a + "taken_at IS NULL, " + a + "taken_at" + dir + ", " + a + "id" + dir, nil
	case SortName:
		return "", a + "name COLLATE NOCASE" + dir + ", " + a + "id" + dir, nil
	case SortSize:
		return "", a + "size" + dir + ", " + a + "id" + dir, nil
	case SortPixels:
		return "", "(" + a + "width * " + a + "height)" + dir + ", " + a + "id" + dir, nil
	case SortScore:
		// Highest score first by default; unscored images last.
		d := " DESC"
		if s.Desc {
			d = " ASC"
		}
		return "LEFT JOIN scores sc ON sc.image_id = " + a + "id AND sc.metric_id = ?",
			"sc.score IS NULL, sc.score" + d + ", " + a + "id", []any{s.MetricID}
	case SortRandom:
		// Deterministic shuffle per seed so paging is stable.
		return "", "((" + a + "id * 2654435761 + ?) % 4294967291)", []any{s.Seed}
	}
	return "", a + "imported_at" + dir + ", " + a + "id" + dir, nil
}
