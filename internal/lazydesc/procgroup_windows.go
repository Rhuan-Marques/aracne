//go:build windows

package lazydesc

import (
	"os"
	"os/exec"
	"syscall"
)

// createNewProcessGroup is CREATE_NEW_PROCESS_GROUP. Spelled here rather than pulling in
// golang.org/x/sys/windows as a direct dependency, the same instinct filelock_windows.go
// follows for its own platform call.
const createNewProcessGroup = 0x00000200

// setProcessGroup puts a provider command in its own console process group, so a console signal
// aimed at the worker does not travel to it and back.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

// killProcessTree kills the provider command.
//
// It kills the child ONLY. Windows has no process-group kill; the equivalent is a Job Object the
// child is assigned at creation, which is a larger piece of platform machinery than this needs.
// The consequence is honest and bounded: on Windows a provider that starts subprocesses of its
// own can leave them behind when the worker is cut short. Everything else that bounds a worker
// -- the context, the watchdog, the lease, the heartbeat-as-ownership-check -- is unaffected,
// because none of it depends on the kill.
func killProcessTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	return p.Kill()
}
