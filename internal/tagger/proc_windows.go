//go:build windows

package tagger

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

// procHandle is the job object holding the tagger's processes: the venv's
// python.exe is a launcher that starts the real interpreter, and the job
// ends both together, including when PhotoBag itself exits.
type procHandle struct {
	job  windows.Handle
	once *sync.Once
}

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

func prepareCmd(cmd *exec.Cmd) { hideWindow(cmd) }

func afterStart(cmd *exec.Cmd) procHandle {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return procHandle{}
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return procHandle{}
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return procHandle{}
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return procHandle{}
	}
	return procHandle{job: job, once: &sync.Once{}}
}

// release closes the job, ending any process still in it.
func (h procHandle) release() {
	if h.job != 0 {
		h.once.Do(func() { windows.CloseHandle(h.job) })
	}
}

func stopProcess(cmd *exec.Cmd, h procHandle, done <-chan struct{}) {
	if h.job != 0 {
		h.once.Do(func() {
			windows.TerminateJobObject(h.job, 1)
			windows.CloseHandle(h.job)
		})
		return
	}
	cmd.Process.Kill()
}
