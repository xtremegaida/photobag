package sysutil

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// IsNetworkPath reports whether path lives on a network share (UNC path or
// mapped network drive).
func IsNetworkPath(path string) bool {
	vol := filepath.VolumeName(path)
	if strings.HasPrefix(vol, `\`) {
		return true
	}
	if vol == "" {
		return false
	}
	root, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(root) == windows.DRIVE_REMOTE
}
