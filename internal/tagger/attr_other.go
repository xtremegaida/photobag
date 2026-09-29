//go:build !windows && !linux

package tagger

import "syscall"

func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
