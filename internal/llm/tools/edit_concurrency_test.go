package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// STO-01. `arac edit` and `arac write` are one process per call, so the in-process file lock
// never contends across calls: two processes that read the same original each write their own
// version and the last atomic rename wins. Every call still reports "edit succeeded", so an
// agent is told its change landed while it was silently reverted -- and
// docs/architecture.md:137 promises parallel sub-agents editing the same file cannot clobber
// each other. These tests drive the real Edit path from several concurrent PROCESSES.

const (
	editChildEnv   = "ARAC_TEST_EDIT_CHILD"
	editChildFile  = "ARAC_TEST_EDIT_FILE"
	editChildIndex = "ARAC_TEST_EDIT_INDEX"
	editChildStart = "ARAC_TEST_EDIT_START"
)

// TestEditChildProcess is the child half of TestSTO01_*: it is skipped in a normal run and
// re-executed by the parent with the environment set.
func TestEditChildProcess(t *testing.T) {
	if os.Getenv(editChildEnv) == "" {
		t.Skip("child half of the concurrent-edit test")
	}
	path := os.Getenv(editChildFile)
	index := os.Getenv(editChildIndex)

	// All children start editing at the same instant, so their read-modify-write windows
	// overlap the way parallel sub-agent calls do.
	if ns, err := strconv.ParseInt(os.Getenv(editChildStart), 10, 64); err == nil {
		if wait := time.Until(time.Unix(0, ns)); wait > 0 {
			time.Sleep(wait)
		}
	}

	newString := "MARK" + index
	args, err := json.Marshal(map[string]any{
		"file_path":  path,
		"old_string": "SLOT" + index,
		"new_string": newString,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := NewEdit(nil, nil).Run(args); err != nil {
		t.Fatalf("edit %s: %v", index, err)
	}
}

// bigFileWithSlots writes a file with one replaceable SLOT<i> line per child, padded so the
// read-modify-write takes long enough for concurrent processes to overlap.
func bigFileWithSlots(t *testing.T, path string, slots int) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < slots; i++ {
		b.WriteString("SLOT" + strconv.Itoa(i) + "\n")
		for j := 0; j < 4000; j++ {
			b.WriteString("padding line to make the file large enough to race\n")
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func TestSTO01_ConcurrentEditProcessesDoNotLoseEdits(t *testing.T) {
	const children = 8
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")
	bigFileWithSlots(t, path, children)

	start := time.Now().Add(1500 * time.Millisecond).UnixNano()
	procs := make([]*exec.Cmd, 0, children)
	for i := 0; i < children; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=TestEditChildProcess", "-test.v")
		cmd.Env = append(os.Environ(),
			editChildEnv+"=1",
			editChildFile+"="+path,
			editChildIndex+"="+strconv.Itoa(i),
			editChildStart+"="+strconv.FormatInt(start, 10),
		)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child %d: %v", i, err)
		}
		procs = append(procs, cmd)
	}
	for i, cmd := range procs {
		if err := cmd.Wait(); err != nil {
			out, _ := os.ReadFile(path)
			t.Fatalf("child %d failed: %v (file has %d bytes)", i, err, len(out))
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	content := string(data)
	var lost []string
	for i := 0; i < children; i++ {
		if !strings.Contains(content, "MARK"+strconv.Itoa(i)) {
			lost = append(lost, fmt.Sprintf("MARK%d", i))
		}
	}
	if len(lost) > 0 {
		t.Fatalf("%d of %d concurrent edits were lost (%s), though every child reported success",
			len(lost), children, strings.Join(lost, ", "))
	}
}
