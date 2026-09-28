//go:build !windows

package sysutil

// Roots lists filesystem roots.
func Roots() []string { return []string{"/"} }
