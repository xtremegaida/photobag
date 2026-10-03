package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"photobag/internal/bag"
	"photobag/internal/files"
	"photobag/internal/imaging"
	"photobag/internal/sysutil"
)

// Images uploaded from a browser are imported in two steps: they wait in a
// folder next to the bag, which is then imported like any other and
// removed. Before anything is sent, the browser offers the files' names,
// sizes and first bytes, so that only images are uploaded.

// UploadOffer is a file a browser offers to upload for an import.
type UploadOffer struct {
	// Path is the file's path as dropped: its name, below the names of
	// the folders it was dropped with.
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Head holds the file's first bytes (imaging.SniffLen of them), which
	// tell whether it is an image.
	Head []byte `json:"head"`
}

// UploadPlan answers an offer: the files to leave out, and why. The rest
// are to be sent.
type UploadPlan struct {
	ID   string       `json:"id"`
	Skip []FileReport `json:"skip"`
}

// PlanUpload decides which offered files to take: images (judging by their
// first bytes) small enough to import, whose names a folder on any system
// can hold, each path once. Hidden and system files are left out, as
// imports from folders leave them out. accepted maps the offered path of
// each file taken to its path in the upload folder; bytes is their size.
func PlanUpload(offers []UploadOffer) (accepted map[string]string, skip []FileReport, bytes int64) {
	accepted = map[string]string{}
	skip = []FileReport{}
	seen := map[string]bool{}
	for _, o := range offers {
		rel, fr := planOne(o)
		if fr == nil {
			key := bag.Fold(rel)
			if seen[key] || accepted[o.Path] != "" {
				fr = skipped("another file has the same path")
			}
			seen[key] = true
		}
		if fr != nil {
			fr.Path = o.Path
			skip = append(skip, *fr)
			continue
		}
		accepted[o.Path] = rel
		bytes += o.Size
	}
	return accepted, skip, bytes
}

func planOne(o UploadOffer) (rel string, fr *FileReport) {
	names, err := files.SplitPath(o.Path)
	if err != nil {
		return "", failed(err.Error())
	}
	if len(names) == 0 {
		return "", failed("the file has no name")
	}
	for i, n := range names {
		if strings.HasPrefix(n, ".") || sysutil.Junk(n, i < len(names)-1) {
			return "", skipped("hidden or system file")
		}
	}
	rel = strings.Join(names, "/")
	if f, reason := imaging.Sniff(o.Head, rel); f == "" {
		return "", skipped(reason)
	}
	if o.Size > MaxFileSize {
		return "", failed(fmt.Sprintf("file is too large (%d MB)", o.Size>>20))
	}
	return rel, nil
}

const uploadPrefix = ".upload-"

// UploadDir is the folder where files uploaded for an import wait, next to
// the bag.
func UploadDir(bagPath, id string) string {
	dir, base := filepath.Split(bagPath)
	return filepath.Join(dir, "."+base+uploadPrefix+id)
}

// CleanUploads removes the upload folders of the bag that a previous run
// left behind.
func CleanUploads(bagPath string) {
	dir, base := filepath.Split(bagPath)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := "." + base + uploadPrefix
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// DescribeUpload names what was dropped, by its top-level names, for
// titles: "Holiday", "Holiday and beach.jpg", "Holiday, beach.jpg and 3
// more".
func DescribeUpload(offers []UploadOffer) string {
	var tops []string
	seen := map[string]bool{}
	for _, o := range offers {
		top, _, _ := strings.Cut(strings.Trim(strings.ReplaceAll(o.Path, `\`, "/"), "/"), "/")
		if top != "" && !seen[top] {
			seen[top] = true
			tops = append(tops, top)
		}
	}
	switch len(tops) {
	case 0:
		return "files"
	case 1:
		return tops[0]
	case 2:
		return tops[0] + " and " + tops[1]
	}
	return fmt.Sprintf("%s, %s and %d more", tops[0], tops[1], len(tops)-2)
}
