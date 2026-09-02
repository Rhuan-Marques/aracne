package lazydesc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CleanSourceEnv opts a process into describing resources from the LAST COMMIT instead of from
// the working tree.
//
// This exists for benchmark harnesses and should stay off everywhere else. For a normal user
// the working tree is the truth: a function someone is editing right now must be described as
// they have just written it, and describing the committed version instead would hand them
// prose about code that no longer exists. A benchmark inverts that. There, an agent is midway
// through editing the repository, and any description generated from an edited file describes
// the SOLVED code -- which then gets persisted and leaks the fix into every later run of that
// fixture.
//
// The value is a commit-ish. "1" and "true" are spelled as HEAD, so a harness that just wants
// "before this run" can set ARACNE_DESCRIBE_FROM_COMMIT=1; anything else is passed to git
// verbatim, which lets a harness pin an explicit base commit.
const CleanSourceEnv = "ARACNE_DESCRIBE_FROM_COMMIT"

// cleanSourceRef returns the commit-ish to describe from, or "" when the feature is off.
func cleanSourceRef() string {
	ref := strings.TrimSpace(os.Getenv(CleanSourceEnv))
	switch strings.ToLower(ref) {
	case "", "0", "false", "off":
		return ""
	case "1", "true", "on", "yes":
		return "HEAD"
	}
	return ref
}

// cutFromCommit reads `path` as it stood at `ref` and returns lines [startsAt, endsAt].
//
// Returns ("", false) whenever the answer would be a guess rather than the resource's text.
// That is the important half of this function, because the alternative to "no description" is
// not "a slightly stale description" -- it is a description of whatever unrelated code now sits
// at those line numbers, written confidently and stored permanently.
//
// Three things make it give up:
//
//   - git does not have the path at that commit. A file the agent CREATED during the run has no
//     committed version, and there is nothing clean to describe.
//   - the line range falls outside the committed file. The topology's ranges track the working
//     tree (the background scanner re-indexes on edit), so on an edited file they describe the
//     dirty bytes and may point past the end of the clean ones.
//   - the cut does not contain the resource's own name. This is the real guard: an edit that
//     shifts a declaration by a few lines leaves the range in-bounds but pointing at a
//     neighbour, and only the name check notices. It costs one strings.Contains and it is what
//     makes "clean" mean clean rather than merely old.
func cutFromCommit(ref, path, name string, startsAt, endsAt int) (string, bool) {
	if ref == "" || path == "" || startsAt <= 0 || endsAt < startsAt {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	dir := filepath.Dir(abs)

	top, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false // not a git repository: nothing committed to read
	}
	rel, err := filepath.Rel(strings.TrimSpace(top), abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	// git wants forward slashes in a pathspec on every platform.
	rel = filepath.ToSlash(rel)

	blob, err := gitOutput(dir, "show", fmt.Sprintf("%s:%s", ref, rel))
	if err != nil {
		return "", false
	}

	lines := strings.Split(blob, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if endsAt > len(lines) {
		return "", false
	}
	cut := strings.TrimSpace(strings.Join(lines[startsAt-1:endsAt], "\n"))
	if cut == "" {
		return "", false
	}
	if name != "" && !strings.Contains(cut, name) {
		return "", false
	}
	return cut, true
}

// gitOutput runs one read-only git command in `dir`.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
