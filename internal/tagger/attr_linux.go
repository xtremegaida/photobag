package tagger

import "syscall"

// sysProcAttr also ends the tagger when PhotoBag dies.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
