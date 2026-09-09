package helper

import (
	"os"
	"path/filepath"
)

// AtomicWriteFile replaces the file at path with data, atomically: the bytes go to a temp
// file in the same directory, are flushed, and are renamed over the destination. A reader
// sees either the whole old file or the whole new one, never a truncated middle.
//
// WHY EVERY WRITER OF A REAL FILE SHOULD USE IT. os.WriteFile truncates first and writes
// second. Interrupted between the two -- a Ctrl-C, a hook whose timeout fires and takes the
// process with it, an OOM kill -- it leaves a zero-byte or half-written file. WriteManifest
// was hardened against exactly that after this repository's own manifest was found empty
// beside a 2.5 GB database; SOURCE files are less recoverable than a manifest and were still
// being written the unsafe way by `edit` and `write`. The harness files `arac setup` and
// `arac disable` generate are the same argument one step further out -- and
// .claude/settings.json is the worst of them, because readJSONConfig exits on a file it
// cannot parse, so one torn write makes every later setup and disable fail until someone
// repairs it by hand.
//
// The destination's existing permissions are preserved when it is already there, so making a
// script executable and then describing it does not silently un-executable it. A new file
// gets perm.
//
// A failed rename leaves the previous file in place, which is the safe outcome.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // No-op once the rename succeeds; cleans up every path that fails.
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Flushed before the rename, so a crash right after it cannot leave the destination
	// pointing at bytes the filesystem has not committed.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
