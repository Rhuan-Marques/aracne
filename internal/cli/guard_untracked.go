package cli

import (
	"os"
	"path/filepath"
	"time"

	"aracne/internal/helper"
)

// untrackedCheckTimeout bounds the topology load this check performs. It runs inside a
// PreToolUse hook on the agent's critical path, so a slow answer is worse than no answer --
// and unlike the read proxy, timing out here means the command stays BLOCKED, which is the
// behaviour that was already shipping.
const untrackedCheckTimeout = 3 * time.Second

// readsOnlyUntrackedFiles reports whether every file a command reads exists on disk and has no
// topology nodes.
//
// WHY. Blocking a read is a good trade only when aracne can answer it better. For a file the
// topology does not model, it cannot answer it at all: `Read.rawFileUnit` falls back to the
// same raw bytes `cat` would have produced, minus the line window the shell command asked for.
// The refusal buys nothing and costs a turn.
//
// Measured in batched-20260901a: 7 of 35 remaining denials targeted files with no nodes --
// CHANGELOG.md, doc/fd.1, HISTORY.rst. It was the direct cause of the worst regression in the
// matrix. Denied a plain read of CHANGELOG.md, the `fd` cell fell back to grep with the pattern
// `.` (a "dump everything" query), then to `python3 -c "print(repr(open(...).read()[:400]))"`
// to inspect whitespace it could not otherwise see: four turns for a file the topology has
// nothing to say about.
//
// This is the same principle as operatesOutsideProject, moved from LOCATION to CONTENT. Both
// answer one question: is there a resource here that aracne could serve instead?
//
// Conservative in every direction that matters:
//   - at least one path must resolve to a real regular file, so a glob, a typo or a pipeline
//     with no readable operand keeps today's behaviour;
//   - if ANY resolved file is tracked, the command stays blocked -- one indexed file in a
//     multi-file read is enough to make the aracne tool the better answer;
//   - a path that does not resolve is ignored rather than assumed untracked;
//   - any failure to consult the topology returns false, keeping the block.
func readsOnlyUntrackedFiles(command, dbPath string) bool {
	root := projectRoot(dbPath)
	if root == "" {
		return false
	}
	files := existingReadFiles(commandPaths(command), root)
	return noneAreTracked(files, dbPath)
}

// readsOnlyUntrackedPath is the native-tool form: one explicit path, no shell to parse.
func readsOnlyUntrackedPath(path, dbPath string) bool {
	root := projectRoot(dbPath)
	if root == "" || path == "" {
		return false
	}
	return noneAreTracked(existingReadFiles([]string{path}, root), dbPath)
}

// noneAreTracked reports whether there is at least one file to judge and the topology holds
// nodes for none of them.
func noneAreTracked(files []string, dbPath string) bool {
	if len(files) == 0 {
		return false
	}
	tracked, ok := trackedFiles(files, dbPath)
	if !ok {
		return false // could not consult the topology: keep the block
	}
	return len(tracked) == 0
}

// existingReadFiles resolves path-shaped tokens to absolute paths of regular files that exist.
//
// A relative token is tried against the project root, mirroring readPathCandidates in the read
// tool. The agent's shell may have `cd`'d somewhere the hook cannot see, so a token that
// resolves nowhere is dropped -- which keeps the block rather than guessing.
func existingReadFiles(tokens []string, root string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range tokens {
		for _, cand := range []string{tok, filepath.Join(root, tok)} {
			if !filepath.IsAbs(cand) {
				continue
			}
			cand = filepath.Clean(cand)
			if seen[cand] {
				break
			}
			info, err := os.Stat(cand)
			// Regular files only: a directory is not a read, and /dev/null (which `2>/dev/null`
			// puts in front of this) is a character device that exists and is untracked --
			// exactly the shape that would turn this check into a blanket exemption.
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			seen[cand] = true
			out = append(out, cand)
			break
		}
	}
	return out
}

// trackedFiles returns the subset of paths the topology holds nodes for, and whether the
// topology could be consulted at all. A false second return means "unknown", never "empty" --
// the caller keeps the block rather than treating an unreadable database as an empty one.
func trackedFiles(paths []string, dbPath string) ([]string, bool) {
	if len(paths) == 0 {
		return nil, true
	}
	type result struct {
		tracked []string
		ok      bool
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- result{nil, false}
			}
		}()
		if _, err := os.Stat(dbPath); err != nil {
			done <- result{nil, false}
			return
		}
		hits, err := helper.TrackedFiles(dbPath, paths)
		if err != nil {
			done <- result{nil, false}
			return
		}
		var tracked []string
		for _, p := range paths {
			if hits[p] {
				tracked = append(tracked, p)
			}
		}
		done <- result{tracked, true}
	}()
	select {
	case r := <-done:
		return r.tracked, r.ok
	case <-time.After(untrackedCheckTimeout):
		return nil, false
	}
}
