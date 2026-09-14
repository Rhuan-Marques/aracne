package cli

import (
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/toolspec"
)

// The guard routes work to the aracne tools where they are the better answer. Two kinds of
// command are not that, and both were costing turns in netguard-20260831b: 9 denials for
// commands that never touch the indexed project, and 6 for git revisions the topology does not
// hold. Neither has an aracne equivalent, so refusing them removed a capability and offered
// nothing back. The commands below are taken from that run's transcripts.

const root = "/repo/worktree"

func TestScratchFilesOutsideTheProjectAreNotTheGuardsBusiness(t *testing.T) {
	outside := []string{
		// The exact shape that cost 9 turns: a throwaway reproduction script. The heredoc
		// body names a repo path, which is data, not an operand -- and a real body always
		// begins on the line after the marker, which is what lets it be dropped cleanly.
		"cat > /tmp/chk.mjs <<'EOF'\nimport { render } from \"/repo/worktree/src/card.js\"\nEOF",
		// A redirect may sit AFTER the marker on the same line, so the line itself is kept.
		"python3 - <<'EOF' > /tmp/out.txt\nprint(1)\nEOF",
		`cat /tmp/pr.diff`,
		`sed -n 1,40p /tmp/scratch.py`,
	}
	for _, cmd := range outside {
		if !operatesOutsideProject(cmd, root) {
			t.Errorf("should be outside the project, so not blocked: %q", cmd)
		}
	}
}

func TestAnythingTouchingTheProjectStaysBlocked(t *testing.T) {
	inside := []string{
		`cat /repo/worktree/lib/response.js`,
		// Relative paths are assumed to be inside: the agent's shell has a working directory
		// this hook cannot see, and guessing otherwise is how `cat internal/thing.go` from a
		// subdirectory starts sailing through.
		`cat lib/response.js`,
		`sed -n 100,130p src/main.rs`,
		// One operand inside the root is enough to keep the guard engaged.
		`cat /tmp/notes.txt /repo/worktree/src/card.js`,
		// No path at all: nothing to be certain about, so behaviour is unchanged.
		`cat`,
	}
	for _, cmd := range inside {
		if operatesOutsideProject(cmd, root) {
			t.Errorf("touches the project (or is unknowable), must stay blocked: %q", cmd)
		}
	}
}

// A sibling directory sharing a name prefix is not inside the project.
func TestPrefixSiblingIsNotInsideTheProject(t *testing.T) {
	if !operatesOutsideProject(`cat /repo/worktree-backup/src/a.go`, root) {
		t.Error("/repo/worktree-backup is a sibling of /repo/worktree, not a child")
	}
}

func TestProjectRootIsDerivedFromTheDatabasePath(t *testing.T) {
	// Compared in the shell's spelling, which is the one this root is ever measured against:
	// filepath.Join hands projectRoot a `\`-separated path on Windows and it keeps that
	// spelling, while every path it is compared with comes out of a command. ShellPathUnder
	// reduces both sides the same way, so only this string equality ever saw the difference.
	got := projectRoot(filepath.Join(root, ".aracne", "topology.db"))
	if toolspec.ShellPathClean(got) != toolspec.ShellPathClean(root) {
		t.Errorf("projectRoot = %q, want %q", got, root)
	}
	if projectRoot("/somewhere/else/topology.db") != "" {
		t.Error("a path that is not under .aracne yields no root, so scoping stays off")
	}
	if projectRoot("") != "" {
		t.Error("no database, no root")
	}
}

// aracne indexes the checked-out tree and nothing else. `git show HEAD:f` asks for what it
// already has -- the bypass this classification exists to close -- while `git show
// origin/master:f` asks for something it cannot answer at any price.
func TestOnlyWorkingTreeGitRevisionsCountAsReads(t *testing.T) {
	blocked := []string{
		`git show HEAD:lib/response.js`,
		`git show HEAD:tracing/src/span.rs | sed -n 1,40p`,
	}
	for _, cmd := range blocked {
		keys := commandKeys(cmd, true)
		if !containsKey(keys, toolspec.ReadToolName) {
			t.Errorf("a working-tree revision is a plain source read and must stay blocked: %q -> %v", cmd, keys)
		}
	}
	exempt := []string{
		`git show origin/master:src/cards/top-languages-card.js`,
		`git show master:src/cards/top-languages-card.js | sed -n '1,80p'`,
		`git show HEAD~1:src/a.go`,
		`git show abc1234:src/a.go`,
	}
	for _, cmd := range exempt {
		if keys := commandKeys(cmd, true); containsKey(keys, toolspec.ReadToolName) {
			t.Errorf("aracne cannot serve another revision, so this must not be refused: %q -> %v", cmd, keys)
		}
	}
}

func containsKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// An interpreter's heredoc body IS its program, so the paths in it are operands rather than
// mentions. Replaying the real denials from netguard-20260831b caught this: a `python3` heredoc
// reading a worktree file was being freed as "scratch work in /tmp" because the body was dropped
// before the scan, while the classifier -- which reads whole command lines for interpreters --
// had correctly called it a read.
func TestAnInterpreterHeredocIsJudgedByWhatItsProgramOpens(t *testing.T) {
	const root = "/repo"
	reads := "cd /tmp && python3 - <<'EOF'\nimport ast\nsrc=open('/repo/src/util.py').read()\nEOF"
	if operatesOutsideProject(reads, root) {
		t.Error("a heredoc program reading a project file must stay blocked")
	}
	scratch := "cd /tmp && python3 - <<'EOF'\nopen('/tmp/gen.rs','w').write('fn main(){}')\nEOF"
	if !operatesOutsideProject(scratch, root) {
		t.Error("a heredoc program confined to /tmp is not the guard's business")
	}
	// A non-interpreter heredoc keeps the old treatment: the body is data it is writing out,
	// and a project path inside it is a mention, not something being read.
	mention := "cat > /tmp/chk.mjs <<'EOF'\nimport { render } from \"/repo/src/card.js\"\nEOF"
	if !operatesOutsideProject(mention, root) {
		t.Error("cat writing to /tmp must stay exempt despite naming the project in its body")
	}
}
