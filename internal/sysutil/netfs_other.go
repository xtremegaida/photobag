//go:build !linux && !windows

package sysutil

// IsNetworkPath is not implemented on this platform.
func IsNetworkPath(string) bool { return false }
