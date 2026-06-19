package topology

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestWithFileLockSerializesSameFile proves that concurrent operations on the
// same file are serialized: 50 racing read-modify-write increments must all
// land. Without the per-file lock this loses updates.
func TestWithFileLockSerializesSameFile(t *testing.T) {
	m := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "counter.txt")
	if err := os.WriteFile(path, []byte("0"), 0644); err != nil {
		t.Fatal(err)
	}
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.WithFileLock(path, func(_ bool) (string, error) {
				data, _ := os.ReadFile(path)
				v, _ := strconv.Atoi(strings.TrimSpace(string(data)))
				return "", os.WriteFile(path, []byte(strconv.Itoa(v+1)), 0644)
			})
		}()
	}
	wg.Wait()
	data, _ := os.ReadFile(path)
	got, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if got != n {
		t.Fatalf("counter = %d, want %d (lost updates mean the per-file lock is not serializing)", got, n)
	}
}

// TestWithFileLockReportsContention checks that the second caller, forced to
// queue behind a holder of the same file lock, observes waited=true.
func TestWithFileLockReportsContention(t *testing.T) {
	m := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_, _ = m.WithFileLock(path, func(waited bool) (string, error) {
			if waited {
				t.Errorf("first holder should not report waited")
			}
			close(held)
			<-release
			return "", nil
		})
	}()
	<-held
	got := make(chan bool, 1)
	go func() {
		_, _ = m.WithFileLock(path, func(waited bool) (string, error) {
			got <- waited
			return "", nil
		})
	}()
	time.Sleep(50 * time.Millisecond) // let the second caller reach the contended lock
	close(release)
	if !<-got {
		t.Fatal("second caller should report waited=true under contention")
	}
}
