package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/toolspec"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The guard exists to route work to the aracne tools where they are the better answer. A
// command that never touches the indexed project is not that: there is no resource to read, no
// topology to keep in sync, and nothing for the MCP tools to serve. Refusing it removes a
// capability and offers nothing back.
//
// Measured over netguard-20260831b, 9 of 50 denials were exactly this -- an agent writing a
// throwaway reproduction script (`cat > /tmp/chk.mjs <<'EOF'`) or dumping a git blob to
// scratch. Each cost a turn and taught the model nothing, because the advice it got back named
// tools that cannot write to /tmp.

// operatesOutsideProject reports whether every path a command names sits outside the indexed
// project, and there is at least one such path to judge by.
//
// Deliberately conservative in one direction only. A RELATIVE path is treated as inside the
// project, because the agent's shell has a working directory this hook cannot see -- assuming
// otherwise is how a guard starts waving through `cat internal/thing.go` from a subdirectory.
// So the exemption fires only when every path-shaped operand is absolute and outside the root:
// a case we can be certain about, which is the only kind worth acting on here.
func operatesOutsideProject(command, root string) bool {
	if root == "" || strings.TrimSpace(command) == "" {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	paths := commandPaths(command)
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return false // relative: assume it is inside the project
		}
		if isUnder(filepath.Clean(p), absRoot) {
			return false
		}
	}
	return true
}

// isUnder reports whether path is root or lives beneath it. Compared segment-wise so a sibling
// directory sharing a name prefix (`/repo-backup` beside `/repo`) is not mistaken for a child.
func isUnder(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return domain.RelInside(rel)
}

// commandPaths is toolspec.CommandPaths under the name the guard has always used for it.
//
// The extractor moved to toolspec because a second caller needs it: the chat permission
// policy never inspected a bash `command`, so its workspace scope was one `bash` call away
// from bypassed, and "does this command touch the project" must have ONE definition rather
// than two that drift.
//
// A `cd` IN THE SAME COMMAND MOVES EVERY RELATIVE OPERAND AFTER IT, and this is the one place
// the guard can see a working directory other than the project root. `cd /tmp/other && cat
// shapes/shape.go` reads /tmp/other/shapes/shape.go; judged against the root it looked like a
// read of the project's own file, so it was refused (blocked_tools) or nudged with a pointer to
// a tool that cannot read it. So the operands after a top-level `cd`/`pushd` with a literal
// target are rebased onto that target, and the target itself is judged from where the command
// started. A `cd` whose effect cannot be known -- `cd -`, `cd "$D"`, one inside a subshell, a
// pipeline or a background job, a `popd` -- stops the rebasing, and what follows is judged
// exactly as it was before.
func commandPaths(command string) []string {
	if !strings.Contains(command, "cd") {
		return toolspec.CommandPaths(command)
	}
	// A heredoc body is data: a line in it that happens to start with `cd` is not a command.
	limit := len(command)
	if strings.Contains(command, "<<") {
		if nl := strings.IndexByte(command, '\n'); nl >= 0 {
			limit = nl
		}
	}
	var out []string
	from, base := 0, ""
	for _, seg := range splitCommandSegments(command) {
		if seg.start >= limit {
			break
		}
		dir, isCd, known := cdTarget(seg, command)
		if !isCd {
			continue
		}
		out = append(out, rebasePaths(toolspec.CommandPaths(command[from:seg.end]), base)...)
		from = seg.end
		if !known {
			// Unknowable from here on: the rest keeps today's reading.
			return append(out, toolspec.CommandPaths(command[from:])...)
		}
		if base != "" && !filepath.IsAbs(dir) {
			dir = filepath.Join(base, dir)
		}
		base = dir
	}
	return append(out, rebasePaths(toolspec.CommandPaths(command[from:]), base)...)
}

// cdTarget reports whether a segment changes the directory of the commands after it, and to
// where. known is false for a directory change this guard cannot follow.
func cdTarget(seg commandSegment, command string) (dir string, isCd, known bool) {
	argv := segmentArgv(seg)
	if len(argv) == 0 {
		return "", false, false
	}
	switch argv[0] {
	case "cd", "pushd":
	case "popd":
		return "", true, false
	default:
		return "", false, false
	}
	// A directory change inside a subshell, a pipeline stage or a background job ends with it.
	background := seg.endedBy == '&' && !strings.HasPrefix(command[seg.end:], "&&")
	if seg.depth > 0 || seg.pipedInto || seg.endedBy == '|' || background {
		return "", true, false
	}
	var args []string
	for i, a := range argv[1:] {
		if a == "--" {
			args = append(args, argv[i+2:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			continue // -P, -L, -e, -@
		}
		args = append(args, a)
	}
	if len(args) > 1 {
		return "", true, false
	}
	target := "~"
	if len(args) == 1 {
		target = args[0]
	}
	if target == "~" || strings.HasPrefix(target, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", true, false
		}
		target = filepath.Join(home, strings.TrimPrefix(target, "~"))
	}
	if target == "" || target == "-" || strings.ContainsAny(target, "$`*?[~") {
		return "", true, false
	}
	return target, true, true
}

// rebasePaths joins every relative path onto base, and leaves absolute ones alone.
func rebasePaths(paths []string, base string) []string {
	if base == "" {
		return paths
	}
	for i, p := range paths {
		if !filepath.IsAbs(p) {
			paths[i] = filepath.Join(base, p)
		}
	}
	return paths
}

// projectRoot turns the resolved database path back into the directory the topology indexes.
// `<root>/.aracne/topology.db` -> `<root>`.
func projectRoot(dbPath string) string {
	if dbPath == "" {
		return ""
	}
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(abs) // .../.aracne
	if filepath.Base(dir) != ".aracne" {
		return ""
	}
	return filepath.Dir(dir)
}
