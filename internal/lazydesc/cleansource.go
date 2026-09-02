package lazydesc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CleanSourceEnv opts a process into describing resources from the LAST COMMIT instead of from
// the working tree.
//
// This exists for benchmark harnesses and should stay off everywhere else. For a normal user
// the working tree is the truth: a function someone is editing right now must be described as
// they have just written it, and describing the committed version instead would hand them
// prose about code that no longer exists. A benchmark inverts that. There, an agent is midway
// through editing the repository, so a description generated from an edited file describes the
// SOLVED code -- which the harness then persists, leaking the fix into every later run of that
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

// hunkRe matches a unified-diff hunk header: @@ -oldStart,oldCount +newStart,newCount @@
var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

type hunk struct{ oldStart, oldCount, newStart, newCount int }

// cutFromCommit reads the resource at `id`'s location as it stood at `ref`.
//
// A resource is identified by its ID, and IDs are stable across an edit -- which is why the
// caller can hand one in and get the past text back. What is NOT stable is the LINE RANGE:
// aracne holds one topology and it describes the present, so `startsAt`/`endsAt` come from the
// working tree, and the background scanner moves them every time the agent saves. Applying a
// present-day range to committed bytes cuts whatever now happens to sit at those numbers.
//
// So the range is translated first. `git diff` already knows exactly how lines moved between
// the commit and the working tree, so mapping a present line back to its committed line is a
// walk over the hunk headers -- no re-parse, and language-agnostic. See mapRangeToPast for
// which edits still map (a rewritten body does) and which do not (a line the agent added has
// no committed counterpart to point at).
//
// Returns ("", false) whenever the answer would be a guess rather than the resource's text.
// That is the important half: the alternative to "no description" is not a slightly stale one,
// it is a confident description of unrelated code, stored permanently.
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
	rel = filepath.ToSlash(rel) // git wants forward slashes in a pathspec on every platform

	blob, err := gitOutput(dir, "show", fmt.Sprintf("%s:%s", ref, rel))
	if err != nil {
		return "", false // no committed version: a file the agent created during the run
	}

	// Translate the present-day range into the range it occupied at `ref`.
	diff, err := gitOutput(dir, "diff", "--unified=0", ref, "--", rel)
	if err != nil {
		return "", false
	}
	pastStart, pastEnd, ok := mapRangeToPast(parseHunks(diff), startsAt, endsAt)
	if !ok {
		return "", false
	}

	lines := strings.Split(blob, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if pastStart < 1 || pastEnd > len(lines) {
		return "", false
	}
	cut := strings.TrimSpace(strings.Join(lines[pastStart-1:pastEnd], "\n"))
	if cut == "" {
		return "", false
	}
	// With the range translated this is an invariant check rather than a heuristic: if the
	// mapped lines do not contain the declaration, the mapping is wrong and the cut is not
	// this resource.
	if name != "" && !strings.Contains(cut, name) {
		return "", false
	}
	return cut, true
}

// parseHunks reads the hunk headers of a unified diff, oldest line first.
func parseHunks(diff string) []hunk {
	var out []hunk
	for _, line := range strings.Split(diff, "\n") {
		m := hunkRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		atoi := func(s string, def int) int {
			if s == "" {
				return def
			}
			n, err := strconv.Atoi(s)
			if err != nil {
				return def
			}
			return n
		}
		out = append(out, hunk{
			oldStart: atoi(m[1], 0), oldCount: atoi(m[2], 1),
			newStart: atoi(m[3], 0), newCount: atoi(m[4], 1),
		})
	}
	return out
}

// mapRangeToPast translates [newStart,newEnd] in the working tree to its range at the commit.
//
// Each endpoint is mapped independently, because the two fail for different reasons and only
// one of them is common. A declaration whose BODY the agent edited still has a well-defined
// committed range -- its first line is untouched, and its last line simply sits at a different
// offset -- and refusing those would throw away exactly the declarations a task is about. What
// has no committed counterpart is a line the agent ADDED: nothing at the commit corresponds to
// it, so a range starting or ending inside an insertion is refused.
func mapRangeToPast(hunks []hunk, newStart, newEnd int) (int, int, bool) {
	pastStart, ok := mapLineToPast(hunks, newStart)
	if !ok {
		return 0, 0, false
	}
	pastEnd, ok := mapLineToPast(hunks, newEnd)
	if !ok || pastEnd < pastStart {
		return 0, 0, false
	}
	return pastStart, pastEnd, true
}

// mapLineToPast returns the line `newLine` occupied at the commit.
func mapLineToPast(hunks []hunk, newLine int) (int, bool) {
	delta := 0 // how far the new file has drifted from the old by this point
	for _, h := range hunks {
		if h.newCount == 0 {
			// Pure deletion: it occupies no new-file line, it only shifts what follows.
			if h.newStart < newLine {
				delta += h.oldCount
			}
			continue
		}
		hunkNewEnd := h.newStart + h.newCount - 1
		if hunkNewEnd < newLine {
			delta += h.oldCount - h.newCount // wholly before us: accumulate the shift
			continue
		}
		if h.newStart > newLine {
			break // wholly after us: cannot affect this line
		}
		// Inside the hunk. A same-size rewrite still maps position-for-position, which is the
		// ordinary "agent changed a line in this function" case. A size change does not: the
		// line may be one the agent added, with nothing at the commit to point at.
		if h.oldCount == h.newCount {
			return h.oldStart + (newLine - h.newStart), true
		}
		return 0, false
	}
	return newLine + delta, true
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
