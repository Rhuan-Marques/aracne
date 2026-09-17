//go:build windows

package lazydesc

import (
	"os/exec"
	"syscall"
)

// detachedProcess is DETACHED_PROCESS: the child gets no console at all, which is the closest
// Windows equivalent of a new session. Combined with CREATE_NEW_PROCESS_GROUP it also means a
// ctrl-c in the parent's console is not delivered to the worker.
//
// Spelled locally rather than promoting golang.org/x/sys/windows to a direct dependency, the
// same instinct filelock_windows.go follows for its own platform call.
const detachedProcess = 0x00000008

func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= detachedProcess | createNewProcessGroup
}
