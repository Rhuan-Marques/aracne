package helper

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AracBinary is the command that should be written into a generated hook or plugin, and the one
// a detached description worker is launched with.
//
// The ABSOLUTE path of the binary doing the writing, when it can be resolved, and the bare name
// otherwise. `arac setup` is the one moment where the answer is known for certain, and the
// scripts used to hard-code `arac` and hope: a build kept at ./bin/arac, a Homebrew install
// whose shell a GUI-launched editor does not inherit, a login PATH the harness does not share --
// each turned every tool call into a failing hook, not a quiet degradation. interceptCommand
// already resolves os.Executable() for exactly this reason.
//
// A VERSIONED path is not the same promise as an absolute one. EvalSymlinks turns a stable
// `/usr/local/bin/arac` into the `Cellar/arac/1.0.0/bin/arac` behind it, and the next upgrade
// deletes that file. The hook scripts and the OpenCode plugins survive it -- they fall back to
// `arac` on PATH when the recorded path is gone -- but the three MCP entries setup writes
// (.mcp.json, opencode.json, every generated agent's inline mcpServers) have no fallback at all:
// a server that cannot start is silent in both harnesses, the tool list simply comes back short.
// So when PATH holds a name for THE SAME FILE, that name is recorded instead. It keeps the
// property the absolute path was for, and loses the version pin.
//
// It lives in helper rather than in cli because lazydesc needs it to spawn a worker, and cli
// imports lazydesc -- so the dependency can only run this way round.
func AracBinary() string { return AracBinaryFrom(os.Executable, exec.LookPath) }

// AracBinaryFrom is AracBinary over its two lookups, so a test can stand at both.
func AracBinaryFrom(executable func() (string, error), lookPath func(string) (string, error)) string {
	exe, err := executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return "arac"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil && resolved != "" {
		exe = resolved
	}
	// Same file, not same name: a stale `arac` earlier on PATH -- an older install, another
	// checkout -- would otherwise be written into the config in place of the binary that is
	// actually running this setup.
	if onPath, err := lookPath("arac"); err == nil && filepath.IsAbs(onPath) && sameFileOnDisk(onPath, exe) {
		return onPath
	}
	return exe
}

// sameFileOnDisk reports whether two paths name one file, following symlinks.
func sameFileOnDisk(a, b string) bool {
	infoA, err := os.Stat(a)
	if err != nil {
		return false
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(infoA, infoB)
}
