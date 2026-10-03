// Package files keeps ordinary files (notes, documentation, anything that
// should travel with the library) in the bag, in a tree of folders. Contents
// are stored in chunks and shared between identical files.
package files

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"photobag/internal/bag"
	"photobag/internal/library"
)

// ErrNotFound is library.ErrNotFound, so the server maps it to 404.
var ErrNotFound = library.ErrNotFound

// ErrConflict reports a name already taken in a folder.
var ErrConflict = errors.New("conflict")

// ErrInvalid marks errors in what was asked for (a bad name, moving a
// folder into itself...), as opposed to failures.
var ErrInvalid = errors.New("invalid")

type invalid struct{ error }

func (e invalid) Is(t error) bool { return t == ErrInvalid }
func (e invalid) Unwrap() error   { return e.error }

func invalidf(format string, args ...any) error { return invalid{fmt.Errorf(format, args...)} }

// Kinds of node, which decide how the web UI shows a file.
const (
	KindFolder   = "folder"
	KindText     = "text"
	KindMarkdown = "markdown"
	KindImage    = "image"
	KindPDF      = "pdf"
	KindAudio    = "audio"
	KindVideo    = "video"
	KindOther    = "other"
)

// Node is a file or folder.
type Node struct {
	ID       int64  `json:"id"`
	ParentID int64  `json:"parentId"` // 0 at the top level
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	// Size is the file's length; for a folder, the total of everything in it.
	Size int64 `json:"size"`
	// Items counts a folder's direct children.
	Items int `json:"items,omitempty"`
	// Type is the media type ("" for folders) and Kind the way to show it.
	Type       string `json:"type,omitempty"`
	Kind       string `json:"kind" tstype:"FileKind"`
	SHA256     string `json:"sha256,omitempty"`
	ModifiedAt int64  `json:"modifiedAt"`
	CreatedAt  int64  `json:"createdAt"`
}

// Listing describes a place in the tree.
type Listing struct {
	// Node is the file or folder; nil at the top level.
	Node *Node `json:"node"`
	// Path runs from the top level down to Node (empty at the top).
	Path []Node `json:"path"`
	// Children are a folder's contents (absent for a file).
	Children []Node `json:"children,omitempty"`
}

// Summary describes everything stored.
type Summary struct {
	Files   int `json:"files"`
	Folders int `json:"folders"`
	// Bytes is the total size of all files; Stored counts identical
	// contents once.
	Bytes  int64 `json:"bytes"`
	Stored int64 `json:"stored"`
}

// Outcomes of storing a file.
const (
	Added    = "added"
	Replaced = "replaced"
	Renamed  = "renamed" // kept apart from an existing file with " (n)"
	Skipped  = "skipped"
)

// PutResult is the file as stored, and what happened.
type PutResult struct {
	Node    *Node  `json:"node"`
	Outcome string `json:"outcome"`
}

// Conflict policies, for a name that is already taken.
const (
	ConflictRename  = "rename"  // keep both: the newcomer gets " (2)"
	ConflictReplace = "replace" // a file replaces the file of the same name
	ConflictSkip    = "skip"    // leave what is there
)

func validConflict(c string) (string, error) {
	switch c {
	case "":
		return ConflictRename, nil
	case ConflictRename, ConflictReplace, ConflictSkip:
		return c, nil
	}
	return "", invalidf("unknown way to handle existing files %q", c)
}

// CleanName checks a file or folder name. Names must be valid on Windows
// and Linux, since files are exported to disk.
func CleanName(name string) (string, error) {
	n, err := library.ValidateName(name)
	if err != nil {
		return "", invalid{err}
	}
	return n, nil
}

func nameKey(name string) string { return bag.Fold(name) }

// SplitPath splits a slash-separated relative path into clean names.
// Empty segments and "." are dropped; ".." is refused.
func SplitPath(p string) ([]string, error) {
	var out []string
	for _, seg := range strings.Split(strings.ReplaceAll(p, `\`, "/"), "/") {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			return nil, invalidf("paths must not contain ..")
		}
		n, err := CleanName(seg)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// numbered returns name with " (n)" before its extension.
func numbered(name string, n int, dir bool) string {
	ext := ""
	if !dir {
		ext = path.Ext(name)
		if ext == name { // ".profile"
			ext = ""
		}
	}
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n, ext)
}

// typeInfo maps extensions to a media type and a kind.
var typeInfo = map[string][2]string{}

func init() {
	add := func(kind, typ string, exts ...string) {
		for _, e := range exts {
			typeInfo[e] = [2]string{typ, kind}
		}
	}
	add(KindMarkdown, "text/markdown", ".md", ".markdown", ".mdown", ".mkd")
	add(KindText, "text/plain", ".txt", ".text", ".log", ".ini", ".cfg", ".conf", ".env", ".properties",
		".rst", ".adoc", ".asciidoc", ".tex", ".bib", ".org", ".srt", ".vtt", ".diff", ".patch",
		".gitignore", ".gitattributes", ".editorconfig", ".dockerfile", ".makefile", ".cmake", ".gradle",
		".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".cs", ".go", ".rs", ".java", ".kt", ".kts", ".scala",
		".swift", ".m", ".py", ".pyi", ".rb", ".pl", ".pm", ".php", ".lua", ".r", ".jl", ".dart", ".sql",
		".sh", ".bash", ".zsh", ".fish", ".bat", ".cmd", ".ps1", ".psm1", ".vb", ".vbs", ".asm", ".s",
		".ts", ".tsx", ".jsx", ".vue", ".svelte", ".scss", ".sass", ".less", ".graphql", ".proto", ".tf",
		".nix", ".hs", ".ml", ".ex", ".exs", ".erl", ".clj", ".lisp", ".el", ".vim", ".reg", ".nfo", ".me")
	add(KindText, "text/csv", ".csv")
	add(KindText, "text/tab-separated-values", ".tsv")
	add(KindText, "text/html", ".html", ".htm", ".xhtml")
	add(KindText, "text/css", ".css")
	add(KindText, "text/javascript", ".js", ".mjs", ".cjs")
	add(KindText, "application/json", ".json", ".jsonl", ".ndjson", ".geojson", ".ipynb", ".webmanifest")
	add(KindText, "application/xml", ".xml", ".xsd", ".xsl", ".plist", ".rss", ".atom", ".gpx", ".kml", ".xmp")
	add(KindText, "application/yaml", ".yaml", ".yml")
	add(KindText, "application/toml", ".toml")
	add(KindImage, "image/png", ".png")
	add(KindImage, "image/jpeg", ".jpg", ".jpeg", ".jpe", ".jfif")
	add(KindImage, "image/gif", ".gif")
	add(KindImage, "image/webp", ".webp")
	add(KindImage, "image/avif", ".avif")
	add(KindImage, "image/bmp", ".bmp")
	add(KindImage, "image/x-icon", ".ico")
	add(KindImage, "image/svg+xml", ".svg")
	add(KindOther, "image/tiff", ".tif", ".tiff")
	add(KindPDF, "application/pdf", ".pdf")
	add(KindAudio, "audio/mpeg", ".mp3")
	add(KindAudio, "audio/wav", ".wav")
	add(KindAudio, "audio/ogg", ".ogg", ".oga")
	add(KindAudio, "audio/opus", ".opus")
	add(KindAudio, "audio/flac", ".flac")
	add(KindAudio, "audio/mp4", ".m4a")
	add(KindAudio, "audio/aac", ".aac")
	add(KindVideo, "video/mp4", ".mp4", ".m4v")
	add(KindVideo, "video/webm", ".webm")
	add(KindVideo, "video/ogg", ".ogv")
	add(KindVideo, "video/quicktime", ".mov")
	add(KindOther, "application/zip", ".zip")
	add(KindOther, "application/gzip", ".gz", ".tgz")
	add(KindOther, "application/x-7z-compressed", ".7z")
	add(KindOther, "application/x-tar", ".tar")
	add(KindOther, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx")
	add(KindOther, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx")
	add(KindOther, "application/vnd.openxmlformats-officedocument.presentationml.presentation", ".pptx")
	add(KindOther, "application/vnd.oasis.opendocument.text", ".odt")
	add(KindOther, "application/msword", ".doc")
	add(KindOther, "application/vnd.ms-excel", ".xls")
	add(KindOther, "application/rtf", ".rtf")
	add(KindOther, "application/epub+zip", ".epub")
	add(KindOther, "font/ttf", ".ttf")
	add(KindOther, "font/otf", ".otf")
	add(KindOther, "font/woff2", ".woff2")
	add(KindOther, "application/vnd.sqlite3", ".sqlite", ".db", ".photobag")
}

// textNames are extensionless files that are text by convention.
var textNames = map[string]bool{"readme": true, "license": true, "licence": true, "copying": true, "authors": true,
	"changelog": true, "makefile": true, "dockerfile": true, "notes": true, "todo": true}

// TypeOf decides a file's media type and kind from its name, falling back
// to the type sniffed from its first bytes.
func TypeOf(name, sniffed string) (typ, kind string) {
	ext := strings.ToLower(path.Ext(name))
	if ext == "" && strings.HasPrefix(name, ".") {
		ext = strings.ToLower(name) // .gitignore
	}
	if ti, ok := typeInfo[ext]; ok {
		return ti[0], ti[1]
	}
	if textNames[strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))] && ext == "" {
		return "text/plain", KindText
	}
	base, _, _ := strings.Cut(sniffed, ";")
	base = strings.TrimSpace(base)
	switch {
	case strings.HasPrefix(base, "text/"):
		return base, KindText
	case base == "" || base == "application/octet-stream":
		return "application/octet-stream", KindOther
	}
	return base, KindOther
}
