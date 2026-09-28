//go:build unix

package sysutil

import "golang.org/x/sys/unix"

// DiskFree returns the bytes available to the current user on the volume
// holding dir.
func DiskFree(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
