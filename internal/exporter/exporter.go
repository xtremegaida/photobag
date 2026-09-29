// Package exporter writes the original bytes of selected images to a folder
// with file names that are valid on both Windows and Linux.
package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"photobag/internal/bag"
	"photobag/internal/imaging"
	"photobag/internal/library"
	"photobag/internal/query"
)

// Existing-file policies.
const (
	ExistingRename    = "rename"
	ExistingOverwrite = "overwrite"
	ExistingSkip      = "skip"
)

// ManifestName is the file written by Options.Manifest.
const ManifestName = "photobag-manifest.json"

// Options control an export.
type Options struct {
	Query query.ImageQuery `json:"query"`
	// KeepStructure recreates the import-relative folders.
	KeepStructure bool `json:"keepStructure,omitempty"`
	// Existing is what to do when a file already exists on disk:
	// rename (default, adds " (2)"), overwrite or skip.
	Existing string `json:"existing,omitempty"`
	Manifest bool   `json:"manifest,omitempty"`
}

// Progress is reported while exporting.
type Progress struct {
	Total   int    `json:"total"`
	Done    int    `json:"done"`
	Written int    `json:"written"`
	Skipped int    `json:"skipped"`
	Failed  int    `json:"failed"`
	Current string `json:"current,omitempty"`
}

// FileReport explains a skipped or failed image.
type FileReport struct {
	ImageID int64  `json:"imageId"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
}

// Report summarises an export.
type Report struct {
	Dir       string       `json:"dir"`
	Total     int          `json:"total"`
	Written   int          `json:"written"`
	Skipped   int          `json:"skipped"`
	Failed    int          `json:"failed"`
	Bytes     int64        `json:"bytes"`
	Manifest  string       `json:"manifest,omitempty"`
	Cancelled bool         `json:"cancelled,omitempty"`
	Files     []FileReport `json:"files"`
}

// ManifestEntry describes one exported file.
type ManifestEntry struct {
	File         string                 `json:"file"`
	UID          string                 `json:"uid"`
	Name         string                 `json:"name"`
	OriginalName string                 `json:"originalName"`
	OriginalPath string                 `json:"originalPath"`
	SHA256       string                 `json:"sha256"`
	Format       string                 `json:"format"`
	Width        int                    `json:"width"`
	Height       int                    `json:"height"`
	TakenAt      string                 `json:"takenAt,omitempty"`
	TakenOffset  string                 `json:"takenOffset,omitempty"`
	Tags         []string               `json:"tags"`
	Scores       map[string]ScoreDetail `json:"scores,omitempty"`
	// Analysis results: the caption, the text found in the image ("" when
	// there was none), Danbooru tags (and the rating, from a tagger) and
	// the category ("Main / Sub").
	Caption  string   `json:"caption,omitempty"`
	OCR      *string  `json:"ocr,omitempty"`
	Danbooru []string `json:"danbooru,omitempty"`
	Rating   string   `json:"rating,omitempty"`
	Category string   `json:"category,omitempty"`
}

// ScoreDetail is an exported per-metric score.
type ScoreDetail struct {
	Score  float64 `json:"score"`
	Stderr float64 `json:"stderr"`
	Rank   int     `json:"rank"`
	Of     int     `json:"of"`
}

// Run exports the images selected by opts.Query into dir.
func Run(ctx context.Context, b *bag.Bag, dir string, opts Options, progress func(Progress)) (*Report, error) {
	if progress == nil {
		progress = func(Progress) {}
	}
	switch opts.Existing {
	case "":
		opts.Existing = ExistingRename
	case ExistingRename, ExistingOverwrite, ExistingSkip:
	default:
		return nil, fmt.Errorf("unknown existing-file policy %q", opts.Existing)
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ids, err := library.ListIDs(ctx, b, opts.Query, query.Sort{Field: query.SortImported})
	if err != nil {
		return nil, err
	}
	rep := &Report{Dir: dir, Total: len(ids), Files: []FileReport{}}
	prog := Progress{Total: len(ids)}
	progress(prog)
	used := map[string]bool{} // lower-cased relative paths claimed by this export
	var manifest []ManifestEntry
	last := time.Now()

	for start := 0; start < len(ids); start += 200 {
		chunk := ids[start:min(start+200, len(ids))]
		images, err := library.GetImages(ctx, b, chunk)
		if err != nil {
			return rep, err
		}
		for _, im := range images {
			if ctx.Err() != nil {
				rep.Cancelled = true
				return rep, nil
			}
			rel, status, reason, n := exportOne(ctx, b, dir, im, opts, used)
			switch status {
			case "written":
				rep.Written++
				rep.Bytes += n
				if opts.Manifest {
					e, err := manifestEntry(ctx, b, rel, im)
					if err != nil {
						return rep, err
					}
					manifest = append(manifest, e)
				}
			case "skipped":
				rep.Skipped++
			default:
				rep.Failed++
			}
			if status != "written" {
				rep.Files = append(rep.Files, FileReport{ImageID: im.ID, Name: im.Name, Status: status, Reason: reason})
			}
			prog.Done++
			prog.Written, prog.Skipped, prog.Failed, prog.Current = rep.Written, rep.Skipped, rep.Failed, im.Name
			if time.Since(last) > 150*time.Millisecond {
				progress(prog)
				last = time.Now()
			}
		}
	}
	if opts.Manifest {
		name, _ := b.Meta(ctx, "name")
		doc := map[string]any{
			"bag":        name,
			"exportedAt": time.Now().UTC().Format(time.RFC3339),
			"query":      opts.Query,
			"images":     manifest,
		}
		data, _ := json.MarshalIndent(doc, "", "  ")
		p := filepath.Join(dir, ManifestName)
		if err := writeAtomic(p, data, 0); err != nil {
			return rep, err
		}
		rep.Manifest = p
	}
	progress(prog)
	return rep, nil
}

func exportOne(ctx context.Context, b *bag.Bag, dir string, im library.Image, opts Options, used map[string]bool) (rel, status, reason string, n int64) {
	name := SafeName(im.Name, imaging.Format(im.Format))
	sub := ""
	if opts.KeepStructure {
		sub = SafeDir(path.Dir(im.OriginalPath))
	}
	rel = path.Join(sub, name)
	target := func(r string) string { return filepath.Join(dir, filepath.FromSlash(r)) }

	exists := func(r string) bool {
		_, err := os.Lstat(target(r))
		return err == nil
	}
	switch {
	case used[strings.ToLower(rel)]:
		rel = uniqueName(rel, func(r string) bool { return used[strings.ToLower(r)] || exists(r) })
	case exists(rel):
		switch opts.Existing {
		case ExistingSkip:
			return rel, "skipped", "file already exists", 0
		case ExistingRename:
			rel = uniqueName(rel, func(r string) bool { return used[strings.ToLower(r)] || exists(r) })
		}
	}
	used[strings.ToLower(rel)] = true

	blob, err := library.Original(ctx, b, im.ID)
	if err != nil {
		return rel, "failed", err.Error(), 0
	}
	p := target(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return rel, "failed", err.Error(), 0
	}
	if err := writeAtomic(p, blob.Data, im.FileMtime); err != nil {
		return rel, "failed", err.Error(), 0
	}
	return rel, "written", "", int64(len(blob.Data))
}

// uniqueName appends " (2)", " (3)", ... before the extension until taken
// reports false.
func uniqueName(rel string, taken func(string) bool) string {
	ext := path.Ext(rel)
	stem := strings.TrimSuffix(rel, ext)
	for i := 2; ; i++ {
		c := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if !taken(c) {
			return c
		}
	}
}

func writeAtomic(p string, data []byte, mtimeMillis int64) error {
	f, err := os.CreateTemp(filepath.Dir(p), ".photobag-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && mtimeMillis > 0 {
		t := time.UnixMilli(mtimeMillis)
		err = os.Chtimes(tmp, t, t)
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func manifestEntry(ctx context.Context, b *bag.Bag, rel string, im library.Image) (ManifestEntry, error) {
	e := ManifestEntry{
		File: rel, UID: im.UID, Name: im.Name, OriginalName: im.OriginalName, OriginalPath: im.OriginalPath,
		SHA256: im.SHA256, Format: im.Format, Width: im.Width, Height: im.Height,
		TakenAt: im.TakenAt, TakenOffset: im.TakenOffset, Tags: im.Tags,
	}
	scores, err := library.Scores(ctx, b, im.ID)
	if err != nil {
		return e, err
	}
	if len(scores) > 0 {
		e.Scores = map[string]ScoreDetail{}
		for _, s := range scores {
			e.Scores[s.MetricName] = ScoreDetail{Score: s.Score, Stderr: s.Stderr, Rank: s.Rank, Of: s.Of}
		}
	}
	as, err := library.Analyses(ctx, b, im.ID)
	if err != nil {
		return e, err
	}
	for _, a := range as {
		switch a.Pipeline {
		case library.PipelineCaption:
			e.Caption = a.Text
		case library.PipelineOCR:
			t := a.Text
			e.OCR = &t
		case library.PipelineDanbooru:
			e.Danbooru, e.Rating = a.Tags, a.Rating
		case library.PipelineCategory:
			e.Category = a.Text
		}
	}
	return e, nil
}

const maxNameBytes = 200

// SafeName turns an image name into a file name valid on Windows and
// Linux, ensuring it carries an extension matching the image format.
func SafeName(name string, f imaging.Format) string {
	s := sanitizeComponent(name)
	ext := strings.ToLower(path.Ext(s))
	ok := false
	for _, e := range f.Extensions() {
		if ext == e {
			ok = true
		}
	}
	if !ok {
		s += f.Extension()
		ext = f.Extension()
	}
	if len(s) > maxNameBytes {
		stem := strings.TrimSuffix(s, path.Ext(s))
		stem = truncateUTF8(stem, maxNameBytes-len(path.Ext(s)))
		s = stem + path.Ext(s)
	}
	return s
}

// SafeDir sanitises each component of a slash-separated relative directory.
func SafeDir(dir string) string {
	if dir == "." || dir == "" || dir == "/" {
		return ""
	}
	var parts []string
	for _, p := range strings.Split(dir, "/") {
		if strings.Trim(p, ". ") == "" { // "", ".", "..", " ." ...
			continue
		}
		parts = append(parts, sanitizeComponent(p))
	}
	return strings.Join(parts, "/")
}

func sanitizeComponent(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7F || strings.ContainsRune(`<>:"/\|?*`, r) {
			sb.WriteRune('_')
		} else {
			sb.WriteRune(r)
		}
	}
	out := strings.TrimLeft(strings.TrimRight(sb.String(), ". "), " ")
	if out == "" {
		out = "image"
	}
	if library.IsReservedWindowsName(out) {
		out = "_" + out
	}
	return out
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
