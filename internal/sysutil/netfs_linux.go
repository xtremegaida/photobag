package sysutil

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Filesystem magic numbers for network filesystems where SQLite's WAL mode
// (shared memory) is unsafe.
var networkFS = map[int64]bool{
	0x6969:     true, // NFS
	0x517B:     true, // SMB
	0xFF534D42: true, // CIFS
	0xFE534D42: true, // SMB2
	0x564C:     true, // NCP
	0x5346414F: true, // AFS
	0x01021997: true, // 9P (v9fs)
}

// IsNetworkPath reports whether path lives on a network filesystem.
func IsNetworkPath(path string) bool {
	var st unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(path), &st); err != nil {
		return false
	}
	return networkFS[int64(st.Type)]
}
