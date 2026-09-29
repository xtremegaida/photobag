package analysis

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"photobag/internal/bag"
)

// ParseError means a reply did not have the expected form. Hint is sent
// back to the model once, asking for a corrected answer.
type ParseError struct {
	Msg  string
	Hint string
}

func (e *ParseError) Error() string { return e.Msg }

// cleanReply removes Markdown code fences and quotes around a whole reply.
func cleanReply(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		} else {
			s = strings.TrimPrefix(s, "```")
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	for _, q := range [][2]string{{`"`, `"`}, {"“", "”"}, {"'", "'"}} {
		if len(s) >= 2 && strings.HasPrefix(s, q[0]) && strings.HasSuffix(s, q[1]) &&
			!strings.Contains(s[len(q[0]):len(s)-len(q[1])], q[0]) {
			s = strings.TrimSpace(s[len(q[0]) : len(s)-len(q[1])])
		}
	}
	return s
}

var noTextRe = regexp.MustCompile(`^(?:no_text|none|n/?a|nothing|` +
	`(?:there (?:is|are) |there's |i (?:can )?see )?no (?:legible |visible |readable |discernible )?text` +
	`(?: (?:is )?(?:found|visible|present|detected|legible))?(?: in (?:the|this) image)?)$`)

// parseOCR returns the text found in the image ("" when there is none).
func parseOCR(reply string) string {
	s := cleanReply(reply)
	probe := strings.ToLower(strings.TrimRight(strings.TrimSpace(s), ".!"))
	if noTextRe.MatchString(probe) || strings.HasPrefix(probe, "no_text") {
		return ""
	}
	// Normalise line endings and trailing spaces, keep the layout.
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	s = strings.TrimSpace(strings.Join(lines, "\n"))
	return truncate(s, 20000)
}

var captionLabel = regexp.MustCompile(`(?i)^(?:alt[ -]?text|caption|description)\s*:\s*`)

// parseCaption returns a one-paragraph description.
func parseCaption(reply string) (string, error) {
	s := cleanReply(captionLabel.ReplaceAllString(cleanReply(reply), ""))
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "", &ParseError{Msg: "the reply was empty", Hint: "Please write the alt text now."}
	}
	return truncate(s, 2000), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

var (
	tagsLabel = regexp.MustCompile(`(?i)^(?:danbooru\s+)?tags\s*:\s*`)
	numbering = regexp.MustCompile(`^\d+[.)]\s+`)
	weighted  = regexp.MustCompile(`^\((.+?)(?::[\d.]+)?\)$`)
)

// parseDanbooru returns up to max normalised Danbooru tags.
func parseDanbooru(reply string, max int) ([]string, error) {
	s := tagsLabel.ReplaceAllString(cleanReply(reply), "")
	var items []string
	if strings.HasPrefix(s, "[") && json.Unmarshal([]byte(s), &items) == nil {
		// a JSON array of tags
	} else {
		items = strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '，' || r == ';' })
	}
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		t := normTag(it)
		if t == "" || seen[t] || utf8.RuneCountInString(t) > 60 || strings.Count(t, "_") > 6 ||
			strings.HasPrefix(t, "rating:") || strings.Contains(t, "://") {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= max {
			break
		}
	}
	if len(out) == 0 {
		return nil, &ParseError{Msg: "the reply contained no tags",
			Hint: "Reply with only the Danbooru tags, separated by commas."}
	}
	return out, nil
}

func normTag(t string) string {
	t = strings.TrimSpace(t)
	t = strings.TrimSpace(strings.TrimLeft(t, "-*•"))
	t = numbering.ReplaceAllString(t, "")
	t = strings.Trim(t, "\"'`“”‘’ ")
	t = strings.TrimRight(t, ".;:")
	if m := weighted.FindStringSubmatch(t); m != nil {
		t = m[1]
	}
	t = strings.ToLower(strings.Join(strings.Fields(t), "_"))
	for strings.Contains(t, "__") {
		t = strings.ReplaceAll(t, "__", "_")
	}
	return strings.Trim(t, "_")
}

// categoryTagNames returns the tags for a category: the main category,
// plus "Main / Sub" with two levels.
func (s Settings) categoryTagNames(main, sub string) []string {
	out := []string{s.Category.Prefix + main}
	if sub != "" {
		out = append(out, s.Category.Prefix+main+" / "+sub)
	}
	return out
}

func categoryText(main, sub string) string {
	if sub == "" {
		return main
	}
	return main + " / " + sub
}

var (
	catLabel = regexp.MustCompile(`(?i)^(?:(?:main|primary)\s+)?(?:category|class)\s*[:：-]\s*`)
	subLabel = regexp.MustCompile(`(?i)^(?:sub-?\s*category|secondary(?:\s+category)?)\s*[:：-]\s*`)
)

// splitCategory reads "Main / Sub" (or labelled lines, or JSON) from a reply.
func splitCategory(reply string) (main, sub string) {
	r := cleanReply(reply)
	if strings.HasPrefix(r, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(r), &m) == nil {
			get := func(keys ...string) string {
				for _, k := range keys {
					if v, ok := m[k].(string); ok {
						return v
					}
				}
				return ""
			}
			return tidy(get("main", "category", "main_category", "mainCategory")), tidy(get("sub", "subcategory", "sub_category", "subCategory"))
		}
	}
	var lines []string
	for _, l := range strings.Split(r, "\n") {
		if l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-*•")); l != "" {
			lines = append(lines, l)
		}
	}
	for _, l := range lines {
		switch {
		case subLabel.MatchString(l):
			sub = subLabel.ReplaceAllString(l, "")
		case catLabel.MatchString(l) && main == "":
			main = catLabel.ReplaceAllString(l, "")
		}
	}
	if main == "" && len(lines) > 0 {
		main = lines[0]
	}
	if sub == "" {
		for _, sep := range []string{" / ", " > ", " → ", " » ", "/", ">", "→", " - ", ": "} {
			if a, b, ok := strings.Cut(main, sep); ok {
				main, sub = a, b
				break
			}
		}
	}
	return tidy(main), tidy(sub)
}

func tidy(s string) string {
	s = strings.Trim(strings.TrimSpace(s), "\"'`“”*_.,;:!")
	return strings.Join(strings.Fields(s), " ")
}

// matchName finds name among names ignoring case, whitespace and a plural s.
func matchName(name string, names []string) string {
	k := bag.LabelKey(name)
	if k == "" {
		return ""
	}
	for _, n := range names {
		if bag.LabelKey(n) == k {
			return n
		}
	}
	for _, n := range names {
		nk := bag.LabelKey(n)
		if nk+"s" == k || k+"s" == nk || nk+"es" == k || k+"es" == nk {
			return n
		}
	}
	return ""
}

func words(s string) string {
	return " " + strings.Join(strings.FieldsFunc(bag.Fold(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ") + " "
}

// containedName finds the longest of names mentioned in text.
func containedName(text string, names []string) string {
	t := words(text)
	best := ""
	for _, n := range names {
		if w := words(n); strings.TrimSpace(w) != "" && strings.Contains(t, w) && len(n) > len(best) {
			best = n
		}
	}
	return best
}

func titleIfLower(s string) string {
	if s == "" || strings.ToLower(s) != s {
		return s
	}
	f := strings.Fields(s)
	for i, w := range f {
		r, n := utf8.DecodeRuneInString(w)
		f[i] = string(unicode.ToUpper(r)) + w[n:]
	}
	return strings.Join(f, " ")
}

// parseCategory maps a reply to a category. With a list, the answer must
// name a listed category (and subcategory, when the main one has any).
// Without one, the model's words are tidied and matched to categories
// already in use.
func parseCategory(reply string, levels int, cats []Category, existing []string) (main, sub string, err error) {
	main, sub = splitCategory(reply)
	if levels == 1 {
		sub = ""
	}
	if main == "" {
		return "", "", &ParseError{Msg: "the reply was empty", Hint: "Reply with only the category."}
	}
	if len(cats) == 0 {
		var mains []string
		subsOf := map[string][]string{}
		for _, e := range existing {
			m, s, _ := strings.Cut(e, " / ")
			mains = append(mains, m)
			if s != "" {
				subsOf[bag.LabelKey(m)] = append(subsOf[bag.LabelKey(m)], s)
			}
		}
		if m := matchName(main, mains); m != "" {
			main = m
		} else {
			main = titleIfLower(main)
		}
		if sub != "" {
			if s := matchName(sub, subsOf[bag.LabelKey(main)]); s != "" {
				sub = s
			} else {
				sub = titleIfLower(sub)
			}
		}
		for _, part := range []string{main, sub} {
			if utf8.RuneCountInString(part) > 40 || len(strings.Fields(part)) > 5 {
				return "", "", &ParseError{Msg: fmt.Sprintf("%q is too long for a category", part),
					Hint: "That is too long. Reply with only a short category of one to three words."}
			}
		}
		return main, sub, nil
	}

	names := make([]string, len(cats))
	for i, c := range cats {
		names[i] = c.Name
	}
	list := strings.Join(names, ", ")
	m := matchName(main, names)
	if m == "" && levels == 2 {
		// Only a subcategory was given?
		for _, c := range cats {
			if s := matchName(main, c.Subs); s != "" {
				return c.Name, s, nil
			}
		}
	}
	if m == "" {
		m = containedName(reply, names)
	}
	if m == "" {
		return "", "", &ParseError{Msg: fmt.Sprintf("%q is not in the category list", main),
			Hint: "That is not one of the categories. Reply with exactly one of: " + list + "."}
	}
	if levels == 1 {
		return m, "", nil
	}
	var c Category
	for _, x := range cats {
		if x.Name == m {
			c = x
		}
	}
	if len(c.Subs) == 0 {
		return m, "", nil
	}
	s := matchName(sub, c.Subs)
	if s == "" {
		s = containedName(sub, c.Subs)
	}
	if s == "" {
		s = containedName(reply, c.Subs)
	}
	if s == "" {
		return "", "", &ParseError{Msg: fmt.Sprintf("%q is not a subcategory of %s", sub, m),
			Hint: fmt.Sprintf("Reply with %s / one of: %s.", m, strings.Join(c.Subs, ", "))}
	}
	return m, s, nil
}
