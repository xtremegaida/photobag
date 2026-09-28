//go:build !windows

package sysutil

import "io/fs"

// IsHidden is false on non-Windows systems (dot-files are handled by name).
func IsHidden(fs.DirEntry) bool { return false }
