package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// scannedProject builds a real project, scans it, and returns its database path. A synthetic
// topology would let the test agree with a wrong idea of how files are recorded; the scanner
// keys a FILE resource by ABSOLUTE path and puts that same path in every symbol's Location,
// and this check depends on both.
func scannedProject(t *testing.T) (root, dbPath string) {
	t.Helper()
	root = t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/proj\n\ngo 1.21\n")
	write("app.go", "package main\n\nfunc Serve() string { return \"ok\" }\n\nfunc main() { _ = Serve() }\n")
	write("CHANGELOG.md", "# Changelog\n\n- first release\n")
	write("doc/tool.1", ".TH TOOL 1\n.SH NAME\ntool \\- does things\n")

	dbPath = filepath.Join(root, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatalf("load: %v", err)
	}
	// FullScan persists the database itself. (Do NOT follow it with mgr.Write(dbPath): Write
	// copies dbPath onto the destination, so passing the same path truncates the database to
	// zero bytes and the scan silently disappears.)
	if err := mgr.FullScan(root, NewScannerRegistry()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	// The fixture is a project in an intercepting mode. Without a config on disk the guard
	// loads defaults, and the default is ModeAracneRead -- which intercepts searches but not
	// reads, so every read-interception assertion built on this fixture would go quietly
	// vacuous rather than fail.
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeLineRange
	if err := helper.SaveConfig(cfg, helper.ConfigPath(dbPath)); err != nil {
		t.Fatal(err)
	}
	// Guard against a silently empty scan making every assertion below vacuous.
	if tracked, ok := trackedFiles([]string{filepath.Join(root, "app.go")}, dbPath); !ok || len(tracked) != 1 {
		t.Fatalf("fixture did not index app.go (ok=%v tracked=%v)", ok, tracked)
	}
	return root, dbPath
}

func TestReadOfAFileWithNoTopologyIsNotWorthBlocking(t *testing.T) {
	root, dbPath := scannedProject(t)
	for _, cmd := range []string{
		"sed -n '1,18p' " + filepath.Join(root, "CHANGELOG.md"),
		"cat CHANGELOG.md",
		"awk 'NR<=18' CHANGELOG.md",
		"sed -n '50,80p' doc/tool.1",
		// The shape that caused the fd regression: a pipeline the read proxy cannot answer,
		// on a file the topology has nothing to say about.
		"sed -n '1,18p' CHANGELOG.md | cat -A | sed -n '1,18p'",
	} {
		if !readsOnlyUntrackedFiles(cmd, dbPath) {
			t.Errorf("still blocked, but aracne has no nodes for it: %s", cmd)
		}
	}
}

func TestReadOfAnIndexedFileStaysBlocked(t *testing.T) {
	root, dbPath := scannedProject(t)
	for _, cmd := range []string{
		"cat app.go",
		"sed -n '1,20p' " + filepath.Join(root, "app.go"),
		"head -20 app.go",
		// One indexed file among several is enough: the aracne read is the better answer for
		// that one, so the whole command keeps its denial.
		"cat CHANGELOG.md app.go",
		"sed -n '1,5p' doc/tool.1; sed -n '1,5p' app.go",
	} {
		if readsOnlyUntrackedFiles(cmd, dbPath) {
			t.Errorf("exempted a read of an indexed file: %s", cmd)
		}
	}
}

func TestNothingToJudgeMeansNoExemption(t *testing.T) {
	root, dbPath := scannedProject(t)

	// A glob never resolves through os.Stat, so there is no file to classify. Assuming
	// "untracked" here would exempt every wildcard read in the repository.
	if readsOnlyUntrackedFiles("cat *.md", dbPath) {
		t.Error("a glob must not be treated as an untracked file")
	}
	if readsOnlyUntrackedFiles("cat missing-file.md", dbPath) {
		t.Error("a path that does not exist must not be treated as untracked")
	}
	// /dev/null exists, is untracked, and rides along on any `2>/dev/null`. Were it counted,
	// the redirect alone would exempt reads of indexed source.
	if readsOnlyUntrackedFiles("cat "+filepath.Join(root, "app.go")+" 2>/dev/null", dbPath) {
		t.Error("a /dev/null redirect must not exempt a read of indexed source")
	}
	// A directory is not a read.
	if readsOnlyUntrackedFiles("cat "+filepath.Join(root, "doc"), dbPath) {
		t.Error("a directory must not count as an untracked file")
	}
}

func TestAnUnreadableTopologyKeepsTheBlock(t *testing.T) {
	root, _ := scannedProject(t)
	// Failing OPEN here would silently disable the guard for every read whenever the database
	// is missing or corrupt -- the opposite of the safe direction for this check.
	if readsOnlyUntrackedFiles("cat CHANGELOG.md", filepath.Join(root, ".aracne", "nope.db")) {
		t.Error("a missing database must keep the block, not lift it")
	}
	if readsOnlyUntrackedPath(filepath.Join(root, "CHANGELOG.md"), "") {
		t.Error("no database path must keep the block")
	}
}

func TestTheNativeReadToolGetsTheSameTreatment(t *testing.T) {
	root, dbPath := scannedProject(t)
	if !readsOnlyUntrackedPath(filepath.Join(root, "CHANGELOG.md"), dbPath) {
		t.Error("native Read of an unmodelled file should not be blocked either")
	}
	if readsOnlyUntrackedPath(filepath.Join(root, "app.go"), dbPath) {
		t.Error("native Read of indexed source must stay blocked")
	}
}

// The end-to-end shape: the exemption has to survive decideGuard, and must not leak into edit.
func TestDecideGuardLetsThroughOnlyUnmodelledReads(t *testing.T) {
	root, dbPath := scannedProject(t)
	blocked := map[string]bool{"read": true, "edit": true, "write": true, "grep": true}

	bash := func(cmd string) map[string]interface{} { return map[string]interface{}{"command": cmd} }

	if d := decideGuard("Bash", bash("cat "+filepath.Join(root, "CHANGELOG.md")), blocked, false, dbPath, toolspec.SurfaceMCP); d.Deny {
		t.Errorf("read of an unmodelled file denied: %s", d.Message)
	}
	if d := decideGuard("Bash", bash("cat "+filepath.Join(root, "app.go")), blocked, false, dbPath, toolspec.SurfaceMCP); !d.Deny {
		t.Error("read of indexed source was not denied")
	}
	// An EDIT must go through the tool that re-syncs the topology whether or not the file is
	// indexed today -- writing to an unmodelled path is one way it becomes indexed. The
	// exemption is gated on read being the ONLY thing denied, which is what keeps this so.
	if d := decideGuard("Bash", bash("sed -i s/a/b/ "+filepath.Join(root, "CHANGELOG.md")), blocked, false, dbPath, toolspec.SurfaceMCP); !d.Deny {
		t.Error("an in-place edit of an unmodelled file must still be denied")
	}
	// A read that also implicates a second blocked key keeps its denial, even on an
	// unmodelled file: the exemption answers "can aracne read this", not "is any of this
	// command harmless".
	mixed := bash("cat " + filepath.Join(root, "CHANGELOG.md") + " && sed -i s/a/b/ " + filepath.Join(root, "CHANGELOG.md"))
	if d := decideGuard("Bash", mixed, blocked, false, dbPath, toolspec.SurfaceMCP); !d.Deny {
		t.Error("a command that also edits must stay denied")
	}
}
