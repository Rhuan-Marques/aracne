package cli

import (
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
func commandPaths(command string) []string { return toolspec.CommandPaths(command) }

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
