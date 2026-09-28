// Package webui embeds the built single-page app (web/ → dist/).
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built app's files.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Built reports whether the app was built into this binary.
func Built() bool {
	_, err := fs.Stat(FS(), "index.html")
	return err == nil
}
