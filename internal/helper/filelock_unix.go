//go:build !windows

package helper

import (
	"os"
	"syscall"
)

// lockPath takes an exclusive advisory lock (flock) on the lock file for abs, blocking until
// it is free, and returns the release. flock is released by the kernel when the process
// exits, so a crashed `arac edit` cannot leave a lock nobody can clear.
func lockPath(abs string) (func(), error) {
	path, err := lockFilePath(abs)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
