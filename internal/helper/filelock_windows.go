//go:build windows

package helper

import (
	"errors"
	"os"
	"time"
)

// lockPath takes an exclusive lock on Windows with an O_EXCL lock file, since flock has no
// equivalent here. A lock file older than staleLockAge is taken over: unlike flock, nothing
// releases it when a process dies.
func lockPath(abs string) (func(), error) {
	path, err := lockFilePath(abs)
	if err != nil {
		return nil, err
	}
	const staleLockAge = 2 * time.Minute
	deadline := time.Now().Add(30 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > staleLockAge {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for the edit lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
