package bag

import (
	"database/sql/driver"
	"fmt"
	"path"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

func init() {
	// pb_glob(pattern, name): case-insensitive shell glob over a file name.
	err := sqlite.RegisterDeterministicScalarFunction("pb_glob", 2,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			pat, _ := args[0].(string)
			name, _ := args[1].(string)
			ok, err := GlobMatch(pat, name)
			if err != nil {
				return nil, err
			}
			if ok {
				return int64(1), nil
			}
			return int64(0), nil
		})
	if err != nil {
		panic(err)
	}
	// pb_contains(text, term): case-insensitive substring search.
	err = sqlite.RegisterDeterministicScalarFunction("pb_contains", 2,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			text, _ := args[0].(string)
			term, _ := args[1].(string)
			if Contains(text, term) {
				return int64(1), nil
			}
			return int64(0), nil
		})
	if err != nil {
		panic(err)
	}
}

var folder = cases.Fold()

// searchForm folds s and treats underscores as spaces, so "long hair"
// finds the Danbooru tag long_hair.
func searchForm(s string) string { return strings.ReplaceAll(Fold(s), "_", " ") }

// Contains reports whether term occurs in text, ignoring case.
func Contains(text, term string) bool {
	return strings.Contains(searchForm(text), searchForm(term))
}

// Fold returns the case-insensitive comparison form of s.
func Fold(s string) string { return folder.String(norm.NFC.String(s)) }

// GlobMatch matches name against a case-insensitive glob (*, ?, [...]).
// Both shell-style [!...] and [^...] negated classes are accepted.
func GlobMatch(pattern, name string) (bool, error) {
	return path.Match(Fold(shellClass(pattern)), Fold(name))
}

func shellClass(p string) string { return strings.ReplaceAll(p, "[!", "[^") }

// ValidateGlob reports whether pattern is a well-formed glob.
func ValidateGlob(pattern string) error {
	if _, err := path.Match(shellClass(pattern), ""); err != nil {
		return fmt.Errorf("invalid name pattern %q: %w", pattern, err)
	}
	return nil
}

// HasGlobMeta reports whether s contains glob metacharacters.
func HasGlobMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

// CleanLabel trims and collapses whitespace in a tag or metric name.
func CleanLabel(s string) string {
	return strings.Join(strings.FieldsFunc(norm.NFC.String(s), unicode.IsSpace), " ")
}

// LabelKey is the unique key for a tag or metric name.
func LabelKey(s string) string { return Fold(CleanLabel(s)) }

// ValidateLabel checks a tag or metric name.
func ValidateLabel(s string) error {
	c := CleanLabel(s)
	if c == "" {
		return fmt.Errorf("name must not be empty")
	}
	if len([]rune(c)) > 100 {
		return fmt.Errorf("name %q is longer than 100 characters", c)
	}
	for _, r := range c {
		if unicode.IsControl(r) {
			return fmt.Errorf("name %q contains control characters", c)
		}
	}
	return nil
}
