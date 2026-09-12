package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// WithPathLock runs fn holding an exclusive, CROSS-PROCESS lock on every path given.
//
// `arac edit` and `arac write` are one process per call: the model's parallel tool calls are
// parallel processes, and TopologyManager.WithFileLock -- a sync.Map of mutexes -- cannot see
// across them. Each process read the same original, applied its own edit to that copy and
// renamed its version over the file, so the last writer won and every other call still
// reported success. docs/architecture.md promises the opposite, and a silently reverted edit
// is the most expensive failure available here: the model believes the code says something it
// does not.
//
// The lock lives beside the topology rather than in the source tree (a lock file next to the
// source would be scanned, committed and edited by the very agents it guards), keyed by a hash
// of the absolute path so two spellings of one file take the same lock. Paths are locked in
// sorted order, so two processes editing the same pair of files in different orders cannot
// deadlock, and the OS releases everything if a process dies mid-edit.
func WithPathLock(paths []string, fn func() error) error {
	unique := map[string]bool{}
	var keys []string
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if !unique[abs] {
			unique[abs] = true
			keys = append(keys, abs)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		release, err := lockPath(key)
		if err != nil {
			// A lock that cannot be taken must not stop the edit: the failure mode it
			// prevents is rare, and refusing to edit at all is worse than the race.
			continue
		}
		defer release()
	}
	return fn()
}

// WithTopologyLock runs fn holding an exclusive, CROSS-PROCESS lock on one topology database.
//
// Every writer of the graph reads it, computes against that snapshot and writes it back --
// and the warnings table is rewritten WHOLESALE from the snapshot (WriteIncremental,
// WriteScopedResources), as is the file manifest (StampManifest). The only serialization used
// to be a per-process RWMutex around each individual statement, which sees nothing across
// processes; and concurrent processes are the normal case here, not an edge: the model's
// parallel tool calls are parallel PostToolUse hooks, each one an `arac update-file`, running
// beside the guard's pre-tool scan and `arac scanner run`. Six concurrent updates each raised
// their node_removed warning and three of them survived -- permanently, since nothing
// re-raises a warning whose cause is already recorded in the graph.
//
// fn MUST NOT take this lock again: it is the OS lock, not a reentrant one. The locked
// internals of TopologyManager exist for exactly that reason. Lock ORDER is file lock (the
// source file, `arac edit`) then database lock, everywhere -- nothing takes them the other way
// round, so the nesting cannot deadlock.
func WithTopologyLock(dbPath string, fn func() error) error {
	if dbPath == "" {
		return fn()
	}
	return WithPathLock([]string{CanonicalPath(dbPath)}, fn)
}

// lockFilePath is where one path's lock file lives: a stable per-user directory, named by the
// hash of the absolute path being guarded.
func lockFilePath(abs string) (string, error) {
	sum := sha256.Sum256([]byte(abs))
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("aracne-locks-%d", os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".lock"), nil
}
