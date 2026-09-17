//go:build !windows

package lazydesc

import (
	"os/exec"
	"syscall"
)

// detach puts the worker in a new SESSION.
//
// Setsid, not just Setpgid: a new session has no controlling terminal, so the worker receives
// neither the SIGINT a ctrl-c sends to the foreground process group nor the SIGHUP that arrives
// when the terminal closes. Without it, a read the user interrupts takes its worker down with
// it -- and the whole point of the worker is that the tokens already spent survive the read.
//
// It is one field. The rest of what makes a detached child safe -- stdio on /dev/null, an
// explicit cwd, a reaper that never blocks -- is platform-independent and lives in spawn.go.
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
