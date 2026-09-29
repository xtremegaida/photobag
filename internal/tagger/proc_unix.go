//go:build !windows

package tagger

import (
	"os/exec"
	"syscall"
	"time"
)

type procHandle struct{}

func (procHandle) release() {}

func hideWindow(*exec.Cmd) {}

// prepareCmd starts the tagger in its own process group, so stopping it
// stops anything it started.
func prepareCmd(cmd *exec.Cmd) { cmd.SysProcAttr = sysProcAttr() }

func afterStart(*exec.Cmd) procHandle { return procHandle{} }

// stopProcess asks the server to shut down, and kills it if it does not.
func stopProcess(cmd *exec.Cmd, _ procHandle, done <-chan struct{}) {
	pid := cmd.Process.Pid
	syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		syscall.Kill(-pid, syscall.SIGKILL)
	}
}
