package cli

import (
	"os"
	"path/filepath"
)

// guardDBRelative is the project-relative location of the topology database. Every guard call
// site used to pass this string directly, which is exactly the bug guardDBPath fixes.
const guardDBRelative = ".aracne/topology.db"

// guardDBPath resolves the topology database the guard should consult.
//
// WHY THIS EXISTS. The guard is a hook process, and it used to open the RELATIVE path
// ".aracne/topology.db". A relative path is resolved against a working directory the MODEL
// controls: the Bash tool's cwd persists between calls, so the moment an agent ran
// `cd packages/mui-joy/src/Select && ...` every subsequent hook invocation looked for the
// database one or more directories below the project root, failed to find it, and
// loadGuardConfig fell through to its fail-open branch -- no blocked tools at all.
//
// It is not a hypothetical. In compact-blocked-after-bs-20260830c the `mui/material-ui` cell
// ran its entire task that way: 27 tool calls, 27 of them Bash, two denials at the very end
// (after a command happened to `cd` back to the worktree root) and zero aracne calls. Both
// postmortems misattributed it to the shell-command classifier, which in fact classifies
// `cd x && sed -n 1,20p f.ts` as a read correctly.
//
// Resolution order, first hit wins:
//  1. the hook event's own `cwd` field (Claude Code reports the SESSION directory here, which
//     is the project root the agent was launched in, and is immune to the agent's own `cd`),
//  2. $CLAUDE_PROJECT_DIR, the same value exported to hook subprocesses,
//  3. an upward walk from the process working directory, which recovers the `cd`-into-a-
//     subdirectory case even when neither of the above is set.
//
// Falls back to the bare relative path so a caller always has something to open: an
// unresolvable database still fails open downstream, which is the behaviour that was there
// before and never breaks a session.
func guardDBPath(eventCwd string) string {
	for _, root := range []string{eventCwd, os.Getenv("CLAUDE_PROJECT_DIR")} {
		if root == "" {
			continue
		}
		if p := filepath.Join(root, guardDBRelative); fileExists(p) {
			return p
		}
	}
	if wd, err := os.Getwd(); err == nil {
		if p, ok := findUpward(wd, guardDBRelative); ok {
			return p
		}
	}
	return guardDBRelative
}

// findUpward walks from dir toward the filesystem root looking for rel, returning the first
// hit. Bounded by reaching the root, so a deep tree costs at most its own depth in stats.
func findUpward(dir, rel string) (string, bool) {
	for {
		if p := filepath.Join(dir, rel); fileExists(p) {
			return p, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
