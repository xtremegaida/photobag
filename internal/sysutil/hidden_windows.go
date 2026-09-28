package sysutil

import (
	"io/fs"
	"syscall"
)

// IsHidden reports whether a directory entry carries the Windows hidden or
// system attribute.
func IsHidden(d fs.DirEntry) bool {
	info, err := d.Info()
	if err != nil {
		return false
	}
	if a, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return a.FileAttributes&(syscall.FILE_ATTRIBUTE_HIDDEN|syscall.FILE_ATTRIBUTE_SYSTEM) != 0
	}
	return false
}
