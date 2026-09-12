package helper

import (
	"fmt"
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
//
// A SYMLINK IS WRITTEN THROUGH, NOT REPLACED. The rename swaps whatever directory entry sits at
// path, so renaming onto a symlink replaced the link with a regular file and never touched the
// file it pointed at: `arac edit` on `pkg/shared.go -> ../shared/shared.go` reported success,
// left the real file unchanged and cut the link. os.WriteFile follows the link, and so does
// this now: the link chain is resolved first and the temp file is renamed over its target.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	path, err := resolveSymlinkTarget(path)
	if err != nil {
		return err
	}
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

// maxSymlinkHops bounds resolveSymlinkTarget, as the kernel's own limit does for open(2).
const maxSymlinkHops = 40

// resolveSymlinkTarget follows path while it names a symbolic link and returns the path the
// chain ends at -- which may not exist yet, since a dangling link is written through to create
// its target, as os.WriteFile would. A relative link is resolved against the directory that
// holds it. Only the final component matters: a symlinked directory higher up is already
// followed by the rename itself.
func resolveSymlinkTarget(path string) (string, error) {
	for hops := 0; ; hops++ {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		if hops == maxSymlinkHops {
			return "", fmt.Errorf("%s: too many levels of symbolic links", path)
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			dir := filepath.Dir(path)
			// Resolved physically, so "../x" from a link reached through a symlinked
			// directory means what the kernel would take it to mean.
			if real, evalErr := filepath.EvalSymlinks(dir); evalErr == nil {
				dir = real
			}
			target = filepath.Join(dir, target)
		}
		path = target
	}
}
