package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultDBRelative is the project-relative location of the topology database. Every guard
// call site used to pass this string directly, which is exactly the bug guardDBPath fixes --
// and every CLI verb still did, which is what ProjectDBPath fixes.
const DefaultDBRelative = ".aracne/topology.db"

// guardDBRelative is DefaultDBRelative under the name the guard has always used for it.
const guardDBRelative = DefaultDBRelative

// ProjectDBPath resolves the topology database a CLI verb should open.
//
// WHY THE CLI NEEDS THIS TOO. guardDBPath exists because a relative ".aracne/topology.db" is
// resolved against a working directory the caller controls, and a hook that opened it from a
// subdirectory silently found nothing. Every `arac` verb had the same bug with a worse
// outcome: InitRegistry does not fail on a missing database, it CREATES one -- so
// `arac read` from a subdirectory built a second, partial topology under `<subdir>/.aracne/`,
// answered from it, and left a stray database and a fresh default config behind. A project on
// intercept_line_ranges then answered those calls under cli semantics.
//
// Only the DEFAULT spelling walks up. An explicit `--db out/topo.db` names a path the caller
// chose and is resolved where they typed it, exactly as before.
func ProjectDBPath(dbPath string) string {
	if strings.TrimSpace(dbPath) == "" {
		dbPath = DefaultDBRelative
	}
	if filepath.IsAbs(dbPath) || fileExists(dbPath) || dbPath != DefaultDBRelative {
		return dbPath
	}
	if wd, err := os.Getwd(); err == nil {
		if p, ok := findUpward(wd, dbPath); ok {
			return p
		}
	}
	return dbPath
}

// ProjectRootFor is the directory a resolved database indexes, or "." when the path does not
// sit under a `.aracne` directory. It is what a scan should be rooted at, so a first scan
// triggered from a subdirectory indexes the project rather than the subdirectory.
func ProjectRootFor(dbPath string) string {
	if root := projectRoot(dbPath); root != "" {
		return root
	}
	return "."
}

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
