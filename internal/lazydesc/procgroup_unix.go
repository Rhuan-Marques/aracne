//go:build !windows

package lazydesc

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup puts a provider command in its OWN process group.
//
// exec.CommandContext kills the process it started and nothing else. That is not enough here:
// the CLI provider is typically `claude -p`, which starts subprocesses of its own -- its hooks,
// its own tool calls -- and those are the ones that would go on running, and on spending, with
// nobody left to stop them. A child that leads its own group can be killed as a group.
//
// Its own group, rather than the worker's: killing the worker's group would kill the worker,
// which is a signal death rather than an exit and loses the status it was about to report.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessTree kills a group leader and everything under it.
//
// The negative pid is the whole point: `kill(-pid)` signals the process GROUP, which is the
// child and every descendant that has not set up a session of its own.
func killProcessTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err != nil {
		// The group may already be gone, or the child may never have made it to setpgid. Fall
		// back to the plain kill so a failure here is never the reason something survives.
		return p.Kill()
	}
	return nil
}
