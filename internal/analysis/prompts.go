package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"photobag/internal/bag"
	"photobag/internal/library"
)

// DefaultSystemPrompt is the system message sent with every request. It
// is the same for all pipelines, so servers with prompt caching can reuse
// the system message and the image across an image's pipelines.
const DefaultSystemPrompt = "You analyse images for a photo library. Follow the instructions exactly and reply with only what is asked for: no preamble, no explanation, no Markdown."

// Default pipeline instructions. Placeholders: {max} (Danbooru tag limit),
// {categories} (the category list) and {existing} (categories already in
// use, when no list is given).
const (
	DefaultOCRPrompt = `Transcribe all legible text in this image exactly as written: signs, labels, captions, documents, screens, handwriting and watermarks. Keep the original language, spelling and line breaks, and separate distinct areas of text with a blank line. Do not describe the image, translate or comment.
If there is no legible text, reply with exactly: NO_TEXT`

	DefaultCaptionPrompt = `Write alt text for this image for a person who cannot see it: one or two concise sentences (at most 50 words) describing the main subject, what is happening and the setting. Mention clearly visible text briefly. Do not begin with "This image shows" or "A picture of", and do not guess at things you cannot see.`

	DefaultDanbooruPrompt = `Tag this image with Danbooru tags, as used on the Danbooru image board: general tags for the number and kind of subjects (such as 1girl, 2boys, no_humans), hair, eyes, clothing, pose, expression, objects, animals, background, setting, lighting, composition and style; add character and copyright tags only when you are certain.
Write tags in lowercase with underscores instead of spaces, most important first, at most {max} tags. Reply with only the tags, separated by commas. Do not include rating or meta tags.`

	DefaultCategoryPromptList = `Classify this image into exactly one of these categories:
{categories}
Reply with only the category name, exactly as written in the list.`

	DefaultCategoryPromptList2 = `Classify this image with one main category and one subcategory from this list (each line is "Main category: subcategories"):
{categories}
Reply with only "Main category / Subcategory", written exactly as in the list. If the main category has no subcategories, reply with only the main category.`

	DefaultCategoryPromptFree = `Give this image one short, general category of one or two words, such as Landscape, Portrait, Food, Architecture, Animals, Document or Screenshot.{existing}
Reply with only the category.`

	DefaultCategoryPromptFree2 = `Give this image a general main category and a more specific subcategory, each of one to three words, such as "Animals / Dogs", "Travel / Beaches" or "Documents / Receipts".{existing}
Reply with only "Main category / Subcategory".`
)

// DefaultPrompt returns the built-in instructions for a pipeline under
// the given settings (the category prompt depends on its options).
func DefaultPrompt(pipeline string, s Settings) string {
	switch pipeline {
	case library.PipelineOCR:
		return DefaultOCRPrompt
	case library.PipelineCaption:
		return DefaultCaptionPrompt
	case library.PipelineDanbooru:
		return DefaultDanbooruPrompt
	case library.PipelineCategory:
		hasList := len(ParseCategories(s.Category.Categories)) > 0
		switch {
		case hasList && s.Category.Levels == 2:
			return DefaultCategoryPromptList2
		case hasList:
			return DefaultCategoryPromptList
		case s.Category.Levels == 2:
			return DefaultCategoryPromptFree2
		default:
			return DefaultCategoryPromptFree
		}
	}
	return ""
}

// template returns the instructions in effect for a pipeline.
func (s Settings) template(pipeline string) string {
	var custom string
	switch pipeline {
	case library.PipelineOCR:
		custom = s.OCR.Prompt
	case library.PipelineCaption:
		custom = s.Caption.Prompt
	case library.PipelineDanbooru:
		custom = s.Danbooru.Prompt
	case library.PipelineCategory:
		custom = s.Category.Prompt
	}
	if strings.TrimSpace(custom) != "" {
		return strings.TrimSpace(custom)
	}
	return DefaultPrompt(pipeline, s)
}

func (s Settings) system() string {
	if p := strings.TrimSpace(s.SystemPrompt); p != "" {
		return p
	}
	return DefaultSystemPrompt
}

// Prompt expands a pipeline's instructions. existing lists categories in
// use (only consulted for free-form categories).
func (s Settings) Prompt(pipeline string, existing []string) string {
	t := s.template(pipeline)
	t = strings.ReplaceAll(t, "{max}", strconv.Itoa(s.Danbooru.MaxTags))
	if pipeline == library.PipelineCategory {
		cats := ParseCategories(s.Category.Categories)
		if len(cats) > 0 {
			list := formatCategories(cats, s.Category.Levels)
			if strings.Contains(t, "{categories}") {
				t = strings.ReplaceAll(t, "{categories}", list)
			} else {
				t += "\n\nCategories:\n" + list
			}
			t = strings.ReplaceAll(t, "{existing}", "")
		} else {
			hint := ""
			if len(existing) > 0 {
				hint = " Prefer one of the categories already in use when it fits: " + strings.Join(existing, ", ") + "."
			}
			t = strings.ReplaceAll(t, "{existing}", hint)
			t = strings.ReplaceAll(t, "{categories}", "")
		}
	}
	return t
}

// ConfigHash identifies the model, prompts and options that shape a
// pipeline's results, so jobs can re-run results made differently.
func (s Settings) ConfigHash(pipeline string) string {
	h := sha256.New()
	if s.UsesTagger(pipeline) {
		// The model's settings do not matter; the tagger's model is not
		// known in advance, so switching servers does not redo results.
		t := s.Danbooru.Tagger
		fmt.Fprintf(h, "%s\x00tagger\x00%v %v %v %v %v %v %d", pipeline, t.General.Include, t.General.Threshold,
			t.Character.Include, t.Character.Threshold, t.Rating.Include, t.Rating.Threshold, s.Danbooru.MaxTags)
		return hex.EncodeToString(h.Sum(nil))[:16]
	}
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%d\x00", pipeline, s.Model, s.system(), s.template(pipeline), s.ImageSize)
	switch pipeline {
	case library.PipelineDanbooru:
		fmt.Fprintf(h, "%d", s.Danbooru.MaxTags)
	case library.PipelineCategory:
		fmt.Fprintf(h, "%d\x00%s", s.Category.Levels, formatCategories(ParseCategories(s.Category.Categories), 2))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Category is an allowed main category with its subcategories.
type Category struct {
	Name string   `json:"name"`
	Subs []string `json:"subs,omitempty"`
}

// ParseCategories reads a category list: one category per line, written
// "Main" or "Main: Sub, Sub" (or "Main / Sub" to add one subcategory).
// Blank lines and lines starting with # are ignored.
func ParseCategories(text string) []Category {
	var out []Category
	index := map[string]int{}
	add := func(main string, subs []string) {
		main = bag.CleanLabel(main)
		if main == "" {
			return
		}
		k := bag.LabelKey(main)
		i, ok := index[k]
		if !ok {
			i = len(out)
			index[k] = i
			out = append(out, Category{Name: main})
		}
		for _, s := range subs {
			s = bag.CleanLabel(s)
			if s == "" {
				continue
			}
			dup := false
			for _, e := range out[i].Subs {
				if bag.LabelKey(e) == bag.LabelKey(s) {
					dup = true
				}
			}
			if !dup {
				out[i].Subs = append(out[i].Subs, s)
			}
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*•"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if main, subs, ok := strings.Cut(line, ":"); ok {
			add(main, strings.FieldsFunc(subs, func(r rune) bool { return r == ',' || r == ';' || r == '|' }))
		} else if main, sub, ok := strings.Cut(line, " / "); ok {
			add(main, []string{sub})
		} else {
			add(line, nil)
		}
	}
	return out
}

func formatCategories(cats []Category, levels int) string {
	var b strings.Builder
	for _, c := range cats {
		b.WriteString("- ")
		b.WriteString(c.Name)
		if levels == 2 && len(c.Subs) > 0 {
			b.WriteString(": ")
			b.WriteString(strings.Join(c.Subs, ", "))
		}
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}
