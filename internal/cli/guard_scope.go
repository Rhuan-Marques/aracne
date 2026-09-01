package cli

import (
	"path/filepath"
	"strings"

	"aracne/internal/toolspec"
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
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// commandPaths pulls the file-shaped operands out of a shell command: anything carrying a
// directory separator or a file extension, minus flags and the pieces of shell syntax that
// merely look like paths.
//
// It reads the whole command string rather than the segment structure, because the point here
// is "does this command touch the project AT ALL" -- one operand inside the root is enough to
// keep the guard engaged, so over-collecting is the safe error.
func commandPaths(command string) []string {
	// A heredoc BODY is usually data, not operands: `cat > /tmp/x.mjs <<'EOF' … "/repo/a.js"`
	// writes to /tmp and merely MENTIONS the repo. Judging it by that mention would keep the
	// very case this function exists to release.
	//
	// Unless an interpreter is running it. `python3 - <<'EOF' … open('/repo/x.py')` performs
	// its file operations inside the body, which is exactly why the classifier reads the whole
	// command line for interpreters (toolspec.InterpreterProgramKey). Dropping the body here
	// while the classifier keeps it would free a genuine repo read -- caught by replaying the
	// run's real denials, where one `python3` heredoc reading a worktree file slipped through.
	//
	// When the body is dropped, only the body is: a redirect may follow the heredoc marker on
	// the same line (`python3 - <<'EOF' > /tmp/out.txt`), and cutting at the marker loses the
	// one operand saying where the command was actually writing.
	if strings.Contains(command, "<<") && !runsAnInterpreter(command) {
		if nl := strings.IndexByte(command, '\n'); nl >= 0 {
			command = command[:nl]
		}
	}
	var out []string
	for _, tok := range strings.FieldsFunc(command, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '|' || r == ';' || r == '&' ||
			r == '(' || r == ')' || r == '\'' || r == '"' || r == '<'
	}) {
		tok = strings.TrimLeft(tok, ">")
		tok = strings.Trim(tok, "'\"`")
		if tok == "" || strings.HasPrefix(tok, "-") {
			continue
		}
		// A `rev:path` operand (git) names a path but not one on disk to compare; the git
		// classifier decides those, so they are not this function's business.
		if strings.Contains(tok, ":") && !filepath.IsAbs(tok) {
			continue
		}
		if strings.ContainsRune(tok, filepath.Separator) || hasFileExtension(tok) {
			out = append(out, tok)
		}
	}
	return out
}

// runsAnInterpreter reports whether any command word before the heredoc body runs an inline
// program, in which case the body is that program and its paths are real operands.
func runsAnInterpreter(command string) bool {
	head := command
	if nl := strings.IndexByte(head, '\n'); nl >= 0 {
		head = head[:nl]
	}
	for _, tok := range strings.Fields(head) {
		if toolspec.IsInterpreter(strings.Trim(tok, "'\"`")) {
			return true
		}
	}
	return false
}

// hasFileExtension is a cheap "looks like a filename" test: a dot with something after it and
// no path separator needed. `NR>=1` and `2.13` are excluded by requiring a letter to lead the
// extension.
func hasFileExtension(tok string) bool {
	dot := strings.LastIndex(tok, ".")
	if dot <= 0 || dot == len(tok)-1 {
		return false
	}
	ext := tok[dot+1:]
	if len(ext) > 8 {
		return false
	}
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return ext[0] >= 'a' && ext[0] <= 'z' || ext[0] >= 'A' && ext[0] <= 'Z'
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
