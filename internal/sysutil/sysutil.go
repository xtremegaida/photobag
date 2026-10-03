// Package sysutil holds small OS-specific helpers.
package sysutil

import "strings"

var (
	junkDirs  = map[string]bool{"$recycle.bin": true, "system volume information": true, "@eadir": true, "#recycle": true}
	junkFiles = map[string]bool{"thumbs.db": true, "desktop.ini": true, "ehthumbs.db": true, ".ds_store": true}
)

// Junk reports operating-system clutter (recycle bins, thumbnail caches)
// that imports leave out.
func Junk(name string, dir bool) bool {
	if dir {
		return junkDirs[strings.ToLower(name)]
	}
	return junkFiles[strings.ToLower(name)]
}
