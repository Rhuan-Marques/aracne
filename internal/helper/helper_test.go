package helper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topogrep"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// TestRemoveFileResourcesWarningSurvivesCleanup verifies that when a file is
// removed, the "verify caller" warning is attributed to the surviving
// referencer (SourceID) rather than the deleted node, so it is not discarded by
// CleanupOrphanedWarnings.
func TestRemoveFileResourcesWarningSurvivesCleanup(t *testing.T) {
	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"pkg/a.go": {
				ID:          "pkg/a.go",
				Kind:        domain.ResourceFile,
				Name:        "a.go",
				Connections: map[string][]string{"has_function": {"pkg.Removed"}},
			},
			"pkg.Removed": {
				ID:   "pkg.Removed",
				Kind: domain.ResourceFunction,
				Name: "Removed",
			},
			"pkg.Caller": {
				ID:          "pkg.Caller",
				Kind:        domain.ResourceFunction,
				Name:        "Caller",
				Connections: map[string][]string{"calls": {"pkg.Removed"}},
			},
		},
		Warnings: map[string]domain.TopologyWarning{},
	}

	warnings := RemoveFileResources(topo, "pkg/a.go")
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %+v", len(warnings), warnings)
	}
	w := warnings[0]
	if w.Kind != domain.WarnNodeRemoved {
		t.Fatalf("expected node_removed kind, got %q", w.Kind)
	}
	// The surviving referencer must be the SourceID so the warning is not
	// dropped by CleanupOrphanedWarnings (which removes warnings whose SourceID
	// no longer exists in the topology).
	if w.SourceID != "pkg.Caller" {
		t.Fatalf("expected SourceID=pkg.Caller (survivor), got %q", w.SourceID)
	}
	if w.TargetID != "pkg.Removed" {
		t.Fatalf("expected TargetID=pkg.Removed (deleted node), got %q", w.TargetID)
	}

	// Simulate the manager flow: record warnings, then prune orphans.
	for _, warn := range warnings {
		topo.Warnings[warn.ID] = warn
	}
	CleanupOrphanedWarnings(topo)

	if len(topo.Warnings) != 1 {
		t.Fatalf("warning should survive cleanup (SourceID still exists), got %d", len(topo.Warnings))
	}
	if _, ok := topo.Resources["pkg.Removed"]; ok {
		t.Fatal("pkg.Removed should have been deleted from resources")
	}
	if _, ok := topo.Resources["pkg.Caller"]; !ok {
		t.Fatal("pkg.Caller should still exist")
	}
}

func TestDiffScanFilesErrorsForMissingRoot(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "file_manifest.json")
	if err := WriteManifest(FileManifest{}, manifestPath); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	_, _, deleted, err := DiffScanFiles(filepath.Join(t.TempDir(), "missing"), "go", manifestPath)
	if err == nil {
		t.Fatal("expected missing root error")
	}
	if len(deleted) != 0 {
		t.Fatalf("expected no deleted files on error, got %v", deleted)
	}
}

// The mass-delete guard fires when the walk returns nothing while the files it should have
// found are STILL ON DISK -- a hidden tree, a new ignore rule, a scan rooted somewhere
// unexpected. Marking those deleted would take the whole language out of the graph.
func TestDiffScanFilesRefusesMassDeleteWhenFilesStillExist(t *testing.T) {
	base := t.TempDir()
	// The manifest's file is real and reachable; the SCAN is rooted somewhere else, so the
	// walk returns nothing. This is the shape the guard exists for -- a scan pointed at the
	// wrong directory must not conclude that the project's source was deleted.
	sources := filepath.Join(base, "a")
	scanRoot := filepath.Join(base, "b")
	for _, dir := range []string{sources, scanRoot} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	present := filepath.Join(sources, "main.go")
	if err := os.WriteFile(present, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	root := scanRoot

	manifestPath := filepath.Join(t.TempDir(), "file_manifest.json")
	if err := WriteManifest(FileManifest{present: "2026-01-01T00:00:00Z"}, manifestPath); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	_, _, deleted, err := DiffScanFiles(root, "go", manifestPath)
	if err == nil {
		t.Fatal("expected the mass-delete guard to refuse: the manifest's file is still on disk")
	}
	if len(deleted) != 0 {
		t.Fatalf("expected no deleted files on guard error, got %v", deleted)
	}
}

// The other half of the same rule, and the case the guard used to refuse wrongly: when every
// manifest file is genuinely GONE from disk, the deletion has to be reported.
//
// Refusing it wedged the scan permanently. Remove the last .py file from a mixed repo and
// DiffScanFiles errored; IncrementalScan treated that as fatal for every language; nothing ever
// cleared the manifest entry that caused it, so the graph silently stopped tracking the whole
// project until someone ran `arac scan --all`.
func TestDiffScanFilesReportsDeletionOfTheLastFileOfALanguage(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(root, "main.go")
	manifestPath := filepath.Join(t.TempDir(), "file_manifest.json")
	if err := WriteManifest(FileManifest{gone: "2026-01-01T00:00:00Z"}, manifestPath); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	_, _, deleted, err := DiffScanFiles(root, "go", manifestPath)
	if err != nil {
		t.Fatalf("DiffScanFiles: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != gone {
		t.Fatalf("deleted = %v, want [%s]", deleted, gone)
	}
}

// A manifest write must never be able to leave a truncated file behind. The empty-manifest
// state is silent -- ReadManifest returns an empty map for it -- and it turns every later
// incremental scan into a full re-parse of the project, which is how a topology grows without
// ever being pruned.
func TestWriteManifestReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "file_manifest.json")

	previous := FileManifest{"/proj/a.go": "2026-09-02T00:00:00Z"}
	if err := WriteManifest(previous, manifestPath); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	// A big manifest is the one that takes long enough to be interrupted mid-write, so write
	// one and assert the file is never observed in a partial state.
	large := FileManifest{}
	for i := 0; i < 5000; i++ {
		large[filepath.Join("/proj", "pkg", "file", "deep", "path", fmt.Sprintf("f%d.go", i))] = "2026-09-02T00:00:00Z"
	}
	if err := WriteManifest(large, manifestPath); err != nil {
		t.Fatalf("WriteManifest large: %v", err)
	}
	if got := ReadManifest(manifestPath); len(got) != len(large) {
		t.Fatalf("manifest has %d entries, want %d", len(got), len(large))
	}

	// The temp file the atomic write uses must not survive it: a directory littered with
	// half-written manifests is its own kind of confusing.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "file_manifest.json" {
			t.Fatalf("stray file left beside the manifest: %s", e.Name())
		}
	}

	// The mode has to survive the rename too -- the temp file starts at 0600.
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0644 {
		t.Fatalf("manifest mode = %v, want 0644", perm)
	}
}

func TestDiffScanFilesNormalizesWindowsManifestPaths(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "main.go")
	if !strings.HasPrefix(filePath, "/mnt/c/") {
		t.Skip("windows-to-wsl path normalization test requires /mnt/c temp path")
	}
	if err := os.WriteFile(filePath, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	manifestPath := filepath.Join(t.TempDir(), "file_manifest.json")
	windowsPath := "C:" + strings.ReplaceAll(strings.TrimPrefix(filePath, "/mnt/c"), "/", "\\")
	manifest := FileManifest{
		windowsPath: info.ModTime().UTC().Format(time.RFC3339Nano),
	}
	if err := WriteManifest(manifest, manifestPath); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	added, modified, deleted, err := DiffScanFiles(root, "go", manifestPath)
	if err != nil {
		t.Fatalf("DiffScanFiles: %v", err)
	}
	if len(added) != 0 || len(modified) != 0 || len(deleted) != 0 {
		t.Fatalf("expected no changes, got added=%v modified=%v deleted=%v", added, modified, deleted)
	}
}

func TestIsSourceFileMatchesScanRules(t *testing.T) {
	tests := []struct {
		name     string
		root     string
		path     string
		language string
		want     bool
	}{
		// No root: every component is examined, which is what the relative-path
		// callers have always relied on.
		{name: "go source", path: "main.go", language: "go", want: true},
		{name: "go test file", path: "main_test.go", language: "go", want: false},
		{name: "python test file", path: "pkg/test_main.py", language: "python", want: false},
		{name: "python source", path: "pkg/main.py", language: "python", want: true},
		{name: "dot dir inside the repo", path: ".opencode/plugins/hook.js", language: "go", want: false},
		{name: "node_modules", path: "node_modules/pkg/file.go", language: "go", want: false},

		// With a root, only the components INSIDE it are examined. Every path in
		// this table used to be relative, which is exactly why the absolute-path
		// bug went unnoticed: a repo under a hidden ancestor scanned to zero
		// files, silently, for every language.
		{name: "repo under a hidden ancestor", root: "/home/u/.claude/scratch/app", path: "/home/u/.claude/scratch/app/src/a.js", language: "javascript", want: true},
		{name: "repo under a CI cache", root: "/home/runner/.cache/x", path: "/home/runner/.cache/x/main.go", language: "go", want: true},
		{name: "repo in a dot-named dir", root: "/home/u/.dotfiles", path: "/home/u/.dotfiles/main.go", language: "go", want: true},
		{name: "repo under a vendor-named ancestor", root: "/srv/vendor/app", path: "/srv/vendor/app/main.go", language: "go", want: true},
		{name: "repo under an env-named ancestor", root: "/opt/env/app", path: "/opt/env/app/main.py", language: "python", want: true},
		// ...and a dot dir INSIDE the repo is still ignored. This is the
		// distinction the fix has to preserve.
		{name: "dot dir inside a hidden-rooted repo", root: "/home/u/.claude/app", path: "/home/u/.claude/app/.opencode/hook.js", language: "javascript", want: false},
		{name: "node_modules inside a hidden-rooted repo", root: "/home/u/.claude/app", path: "/home/u/.claude/app/node_modules/p/i.js", language: "javascript", want: false},

		// The Rust and Java directory rules had the identical shape and need no
		// dot at all to misfire.
		{name: "rust repo under a tests-named ancestor", root: "/home/u/tests/proj", path: "/home/u/tests/proj/src/lib.rs", language: "rust", want: true},
		{name: "rust tests dir inside the repo", root: "/home/u/proj", path: "/home/u/proj/tests/it.rs", language: "rust", want: false},
		{name: "rust target dir inside the repo", root: "/home/u/proj", path: "/home/u/proj/target/debug/b.rs", language: "rust", want: false},
		{name: "java repo under a build-named ancestor", root: "/srv/build/app", path: "/srv/build/app/src/main/java/com/d/A.java", language: "java", want: true},
		{name: "java repo under a bin-named ancestor", root: "/usr/bin/app", path: "/usr/bin/app/src/main/java/com/d/A.java", language: "java", want: true},
		{name: "java build dir inside the repo", root: "/srv/app", path: "/srv/app/build/com/d/A.java", language: "java", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSourceFile(tt.root, tt.path, tt.language); got != tt.want {
				t.Fatalf("IsSourceFile(%q, %q, %q) = %v, want %v", tt.root, tt.path, tt.language, got, tt.want)
			}
		})
	}
}

func TestDBRoundtrip(t *testing.T) {
	path := "test_topology.db"
	defer os.Remove(path)

	topo := &domain.Topology{
		Root:     "/test",
		Language: "go",
		Resources: map[string]domain.Resource{
			"f1": {
				ID:          "f1",
				Kind:        domain.ResourceFunction,
				Name:        "Foo",
				Description: "does foo",
				Location:    domain.Location{StartsAt: 1, EndsAt: 10, Path: "main.go"},
				Properties: map[string]any{
					"input":  []any{},
					"output": []any{},
				},
				Connections: map[string][]string{
					"calls": {"f2"},
				},
			},
			"f2": {
				ID:   "f2",
				Kind: domain.ResourceFunction,
				Name: "Bar",
			},
		},
		Warnings: map[string]domain.TopologyWarning{
			"w1": {
				ID: "w1", SourceID: "f1",
				Kind: domain.WarnUseMissingNode, TargetID: "f3",
				Message: "missing f3",
			},
		},
		Errors: map[string]string{"file.go": "parse error"},
	}

	if err := WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	read, err := ReadDb(path)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}

	if read.Root != "/test" {
		t.Errorf("expected root /test, got %q", read.Root)
	}
	if read.Language != "go" {
		t.Errorf("expected language go, got %q", read.Language)
	}
	if len(read.Resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(read.Resources))
	}
	res, ok := read.Resources["f1"]
	if !ok {
		t.Fatal("expected resource f1")
	}
	if res.Name != "Foo" {
		t.Errorf("expected name Foo, got %q", res.Name)
	}
	if res.Description != "does foo" {
		t.Errorf("expected description 'does foo', got %q", res.Description)
	}
	if res.Location.StartsAt != 1 || res.Location.EndsAt != 10 || res.Location.Path != "main.go" {
		t.Errorf("unexpected location: %+v", res.Location)
	}
	if len(res.Connections["calls"]) != 1 || res.Connections["calls"][0] != "f2" {
		t.Errorf("unexpected connections: %v", res.Connections)
	}
	if len(read.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(read.Warnings))
	}
	if len(read.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(read.Errors))
	}
}

func TestDBWriteOverwrite(t *testing.T) {
	path := "test_overwrite.db"
	defer os.Remove(path)

	topo1 := &domain.Topology{
		Root: "/v1",
		Resources: map[string]domain.Resource{
			"f1": {ID: "f1", Kind: domain.ResourceFunction, Name: "Foo"},
		},
	}
	if err := WriteDb(topo1, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	topo2 := &domain.Topology{
		Root: "/v2",
		Resources: map[string]domain.Resource{
			"f2": {ID: "f2", Kind: domain.ResourceFunction, Name: "Bar"},
		},
	}
	if err := WriteDb(topo2, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	read, err := ReadDb(path)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}
	if read.Root != "/v2" {
		t.Errorf("expected root /v2, got %q", read.Root)
	}
	if len(read.Resources) != 1 {
		t.Errorf("expected 1 resource, got %d", len(read.Resources))
	}
}

func TestDBResourceNoLocation(t *testing.T) {
	path := "test_noloc.db"
	defer os.Remove(path)

	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"p1": {ID: "p1", Kind: domain.ResourcePackage, Name: "mypkg"},
		},
	}
	if err := WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	read, err := ReadDb(path)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}
	res := read.Resources["p1"]
	if res.Location.Path != "" {
		t.Errorf("expected empty path for package, got %q", res.Location.Path)
	}
}

func TestDBResourceWithProperties(t *testing.T) {
	path := "test_props.db"
	defer os.Remove(path)

	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"f1": {
				ID:   "f1",
				Kind: domain.ResourceFunction,
				Name: "Foo",
				Properties: map[string]any{
					"input":  []any{map[string]any{"Name": "x", "Typing": "int"}},
					"output": []any{},
				},
			},
		},
	}
	if err := WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	read, err := ReadDb(path)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}
	res := read.Resources["f1"]
	input, ok := res.Properties["input"]
	if !ok {
		t.Fatal("expected input property")
	}
	inputArr, ok := input.([]any)
	if !ok || len(inputArr) == 0 {
		t.Fatal("expected non-empty input array")
	}
}

func TestUpdateDescription(t *testing.T) {
	path := "test_updatedesc.db"
	defer os.Remove(path)

	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"f1": {ID: "f1", Kind: domain.ResourceFunction, Name: "Foo"},
		},
	}
	if err := WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	if err := UpdateDescription(path, domain.ResourceFunction, "f1", "new description"); err != nil {
		t.Fatalf("UpdateDescription: %v", err)
	}

	read, err := ReadDb(path)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}
	if read.Resources["f1"].Description != "new description" {
		t.Errorf("expected 'new description', got %q", read.Resources["f1"].Description)
	}
}

// A function-or-method write lands on the one resource its id names, whichever of the two
// kinds the caller guessed. The scanners store a function with a receiver or a declaring class
// as `method`, and every language tool mapped "Method" (Java also "Constructor") to `function`,
// so each such write was refused with "no function resource with id ...". Any other kind is
// still enforced.
func TestUpdateDescriptionAcceptsEitherCallableKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"com.a.Marked.compute()": {ID: "com.a.Marked.compute()", Kind: domain.ResourceMethod, Name: "compute"},
			"com.a.helper()":         {ID: "com.a.helper()", Kind: domain.ResourceFunction, Name: "helper"},
		},
	}
	if err := WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	if err := UpdateDescription(path, domain.ResourceFunction, "com.a.Marked.compute()", "computes"); err != nil {
		t.Fatalf("function write on a method id: %v", err)
	}
	if err := UpdateDescription(path, domain.ResourceMethod, "com.a.helper()", "helps"); err != nil {
		t.Fatalf("method write on a function id: %v", err)
	}
	if err := UpdateDescription(path, domain.ResourceStruct, "com.a.helper()", "wrong"); err == nil {
		t.Fatal("a struct write on a function id must still be refused")
	}
	if err := UpdateDescription(path, domain.ResourceMethod, "com.a.Missing.m()", "none"); err == nil {
		t.Fatal("a write to an id that does not exist must still fail")
	}

	read, err := ReadDb(path)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}
	if got := read.Resources["com.a.Marked.compute()"].Description; got != "computes" {
		t.Errorf("method description = %q", got)
	}
	if got := read.Resources["com.a.helper()"].Description; got != "helps" {
		t.Errorf("function description = %q", got)
	}
}

func TestClearDescriptionsFiltersTargets(t *testing.T) {
	path := "test_cleardesc.db"
	defer os.Remove(path)

	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"f1": {ID: "f1", Kind: domain.ResourceFunction, Name: "Foo", Description: "function description"},
			"t1": {ID: "t1", Kind: domain.ResourceStruct, Name: "Thing", Description: "type description"},
			"m1": {ID: "m1", Kind: domain.ResourceMethod, Name: "Method"},
		},
	}
	if err := WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	count, err := ClearDescriptions(path, []domain.ResourceKind{domain.ResourceFunction})
	if err != nil {
		t.Fatalf("ClearDescriptions: %v", err)
	}
	if count != 1 {
		t.Fatalf("cleared %d descriptions, want 1", count)
	}

	read, err := ReadDb(path)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}
	if read.Resources["f1"].Description != "" {
		t.Fatalf("function description was not cleared")
	}
	if read.Resources["t1"].Description != "type description" {
		t.Fatalf("type description was unexpectedly cleared")
	}

	count, err = ClearDescriptions(path, nil)
	if err != nil {
		t.Fatalf("ClearDescriptions all: %v", err)
	}
	if count != 1 {
		t.Fatalf("cleared %d descriptions, want 1", count)
	}
}

func TestBugCRUD(t *testing.T) {
	path := "test_bugs.db"
	defer os.Remove(path)

	topo := &domain.Topology{}
	if err := WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	bug := domain.KnownBug{
		ID: "bug_1", NodeID: "f1",
		Description: "nil pointer",
		State:       domain.BugPending,
	}
	if err := CreateBug(path, bug); err != nil {
		t.Fatalf("CreateBug: %v", err)
	}

	bugs, err := ReadBugs(path, "", "")
	if err != nil {
		t.Fatalf("ReadBugs: %v", err)
	}
	if len(bugs) != 1 {
		t.Fatalf("expected 1 bug, got %d", len(bugs))
	}
	if bugs[0].ID != "bug_1" || bugs[0].NodeID != "f1" {
		t.Errorf("unexpected bug: %+v", bugs[0])
	}

	bug2 := domain.KnownBug{
		ID: "bug_2", NodeID: "f2",
		Description: "race condition",
		State:       domain.BugPending,
	}
	if err := CreateBug(path, bug2); err != nil {
		t.Fatalf("CreateBug: %v", err)
	}

	if err := UpdateBugState(path, "bug_1", domain.BugAcknowledged); err != nil {
		t.Fatalf("UpdateBugState: %v", err)
	}

	ackBugs, err := ReadBugs(path, "", domain.BugAcknowledged)
	if err != nil {
		t.Fatalf("ReadBugs: %v", err)
	}
	if len(ackBugs) != 1 || ackBugs[0].ID != "bug_1" {
		t.Fatalf("expected 1 acknowledged bug, got %d", len(ackBugs))
	}

	filtered, err := ReadBugs(path, "f2", "")
	if err != nil {
		t.Fatalf("ReadBugs: %v", err)
	}
	if len(filtered) != 1 || filtered[0].NodeID != "f2" {
		t.Fatalf("expected 1 bug for f2, got %d", len(filtered))
	}

	if err := DeleteBug(path, "bug_1"); err != nil {
		t.Fatalf("DeleteBug: %v", err)
	}
	all, err := ReadBugs(path, "", "")
	if err != nil {
		t.Fatalf("ReadBugs: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 bug after delete, got %d", len(all))
	}

	if err := DeleteAllBugs(path); err != nil {
		t.Fatalf("DeleteAllBugs: %v", err)
	}
	all, err = ReadBugs(path, "", "")
	if err != nil {
		t.Fatalf("ReadBugs: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected 0 bugs after delete all, got %d", len(all))
	}
}

func TestGetCallers(t *testing.T) {
	path := "test_callers.db"
	defer os.Remove(path)

	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"f1": {
				ID:   "f1",
				Kind: domain.ResourceFunction,
				Name: "Caller",
				Connections: map[string][]string{
					"calls": {"f2"},
				},
			},
			"f2": {
				ID:   "f2",
				Kind: domain.ResourceFunction,
				Name: "Target",
			},
		},
	}
	if err := WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	callers, err := GetCallers(path, "f2", "calls")
	if err != nil {
		t.Fatalf("GetCallers: %v", err)
	}
	if len(callers) != 1 || callers[0] != "f1" {
		t.Errorf("expected [f1], got %v", callers)
	}

	noCallers, err := GetCallers(path, "f1", "calls")
	if err != nil {
		t.Fatalf("GetCallers: %v", err)
	}
	if len(noCallers) != 0 {
		t.Errorf("expected no callers for f1, got %v", noCallers)
	}
}

func TestUpdateBugStateNonexistent(t *testing.T) {
	path := "test_nonexistent_bug.db"
	defer os.Remove(path)

	topo := &domain.Topology{}
	if err := WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	err := UpdateBugState(path, "nonexistent", domain.BugAcknowledged)
	if err == nil {
		t.Error("expected error for updating nonexistent bug")
	}
}

func TestDefaultConfigDescriptions(t *testing.T) {
	cfg := DefaultConfig()
	want := []domain.ResourceKind{domain.ResourceFunction, domain.ResourceMethod, domain.ResourceStruct, domain.ResourceInterface}
	if len(cfg.Descriptions.Kinds) != len(want) {
		t.Fatalf("Descriptions.Kinds = %v, want %v", cfg.Descriptions.Kinds, want)
	}
	for i := range want {
		if cfg.Descriptions.Kinds[i] != want[i] {
			t.Fatalf("Descriptions.Kinds = %v, want %v", cfg.Descriptions.Kinds, want)
		}
	}
	if got := cfg.AgentParam("claude_code", "descriptions-generation-executor", "max-batch-size", 0); got != DefaultDescriptionBatchSize {
		t.Fatalf("executor max-batch-size = %d, want %d", got, DefaultDescriptionBatchSize)
	}
}

func TestShouldDescribe(t *testing.T) {
	targets := DescribeTargetSet([]domain.ResourceKind{domain.ResourceFunction, domain.ResourceVariable, domain.ResourceStruct})

	// Small functions render as full code, external vars are hidden.
	filter := domain.ContextFilter{
		ExtVarsVisibility: domain.VisibilityHidden,
		SmallFnVisibility: domain.VisibilityFull,
		SmallFnThreshold:  5,
	}

	bigFn := domain.Resource{Kind: domain.ResourceFunction, Location: domain.Location{StartsAt: 1, EndsAt: 20}}
	smallFn := domain.Resource{Kind: domain.ResourceFunction, Location: domain.Location{StartsAt: 1, EndsAt: 3}}
	extVar := domain.Resource{Kind: domain.ResourceVariable}
	described := domain.Resource{Kind: domain.ResourceFunction, Description: "already documented", Location: domain.Location{StartsAt: 1, EndsAt: 20}}
	offTarget := domain.Resource{Kind: domain.ResourceInterface, Location: domain.Location{StartsAt: 1, EndsAt: 20}}

	cases := []struct {
		name              string
		res               domain.Resource
		includeNotVisible bool
		want              bool
	}{
		{"normal-size fn always counts", bigFn, false, true},
		{"small fn rendered full is skipped", smallFn, false, false},
		{"small fn included when include_not_visible", smallFn, true, true},
		{"hidden external var is skipped", extVar, false, false},
		{"external var included when include_not_visible", extVar, true, true},
		{"already-described is skipped", described, false, false},
		{"off-target kind is skipped", offTarget, false, false},
	}
	for _, tc := range cases {
		if got := ShouldDescribe(tc.res, targets, filter, tc.includeNotVisible); got != tc.want {
			t.Errorf("%s: ShouldDescribe = %v, want %v", tc.name, got, tc.want)
		}
	}

	// Under the default all-normal filter, visibility never skips a target.
	if !ShouldDescribe(smallFn, targets, domain.DefaultContextFilter(), false) {
		t.Errorf("small fn should count under default all-normal filter")
	}
	if !ShouldDescribe(extVar, targets, domain.DefaultContextFilter(), false) {
		t.Errorf("external var should count under default all-normal filter")
	}
}

// ShouldRegenerateDescription selects the exact complement of ShouldDescribe's population:
// resources that ARE described, but over their kind's budget.
func TestShouldRegenerateDescription(t *testing.T) {
	targets := DescribeTargetSet([]domain.ResourceKind{domain.ResourceFunction, domain.ResourceVariable, domain.ResourceStruct})

	// Small functions render as full code, external vars are hidden.
	filter := domain.ContextFilter{
		ExtVarsVisibility: domain.VisibilityHidden,
		SmallFnVisibility: domain.VisibilityFull,
		SmallFnThreshold:  5,
	}

	overFn := strings.Repeat("x", domain.DescriptionBudgetFunction+1)
	atFn := strings.Repeat("x", domain.DescriptionBudgetFunction)
	bigLoc := domain.Location{StartsAt: 1, EndsAt: 20}

	cases := []struct {
		name              string
		res               domain.Resource
		includeNotVisible bool
		want              bool
	}{
		{"over-budget fn is regenerated", domain.Resource{Kind: domain.ResourceFunction, Description: overFn, Location: bigLoc}, false, true},
		{"at-budget fn is left alone", domain.Resource{Kind: domain.ResourceFunction, Description: atFn, Location: bigLoc}, false, false},
		{"undescribed fn is not this pass's job", domain.Resource{Kind: domain.ResourceFunction, Location: bigLoc}, false, false},
		{"trailing whitespace does not push it over", domain.Resource{Kind: domain.ResourceFunction, Description: atFn + "\n", Location: bigLoc}, false, false},
		{"off-target kind is skipped", domain.Resource{Kind: domain.ResourceInterface, Description: overFn, Location: bigLoc}, false, false},
		{"small fn rendered full is skipped", domain.Resource{Kind: domain.ResourceFunction, Description: overFn, Location: domain.Location{StartsAt: 1, EndsAt: 3}}, false, false},
		{"small fn included when include_not_visible", domain.Resource{Kind: domain.ResourceFunction, Description: overFn, Location: domain.Location{StartsAt: 1, EndsAt: 3}}, true, true},
		{"hidden external var is skipped", domain.Resource{Kind: domain.ResourceVariable, Description: overFn}, false, false},
		{"external var included when include_not_visible", domain.Resource{Kind: domain.ResourceVariable, Description: overFn}, true, true},
	}
	for _, tc := range cases {
		if got := ShouldRegenerateDescription(tc.res, targets, filter, tc.includeNotVisible); got != tc.want {
			t.Errorf("%s: ShouldRegenerateDescription = %v, want %v", tc.name, got, tc.want)
		}
	}

	// Budgets are per kind: text that fits a function overruns a struct.
	between := strings.Repeat("x", domain.DescriptionBudgetType+1)
	structRes := domain.Resource{Kind: domain.ResourceStruct, Description: between, Location: bigLoc}
	fnRes := domain.Resource{Kind: domain.ResourceFunction, Description: between, Location: bigLoc}
	if !ShouldRegenerateDescription(structRes, targets, domain.DefaultContextFilter(), false) {
		t.Errorf("a %d-char struct description overruns the %d-char type budget", len(between), domain.DescriptionBudgetType)
	}
	if ShouldRegenerateDescription(fnRes, targets, domain.DefaultContextFilter(), false) {
		t.Errorf("the same text still fits the %d-char function budget", domain.DescriptionBudgetFunction)
	}
}

func TestLoadConfigCleanBreakOnOldFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	// An old-format config has none of the new-schema fields.
	if err := os.WriteFile(path, []byte(`{"scan_mode":"hard","tool_modes":{"read":"mcp"},"need_description":["file"]}`), 0644); err != nil {
		t.Fatalf("WriteFile legacy config: %v", err)
	}
	cfg, ok := LoadConfigStrict(path)
	if ok {
		t.Fatal("old-format config should not parse as valid new schema")
	}
	if len(cfg.Descriptions.Kinds) != len(DefaultNeedDescription()) {
		t.Fatalf("Descriptions.Kinds = %v, want defaults", cfg.Descriptions.Kinds)
	}
}

func TestLoadConfigNewSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"descriptions":{"kinds":["file","struct"]},"read":{"max_file_size":4096}}`), 0644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg, ok := LoadConfigStrict(path)
	if !ok {
		t.Fatal("new-schema config should parse cleanly")
	}
	if len(cfg.Descriptions.Kinds) != 2 || cfg.Descriptions.Kinds[0] != domain.ResourceFile || cfg.Descriptions.Kinds[1] != domain.ResourceStruct {
		t.Fatalf("Descriptions.Kinds = %v, want [file type]", cfg.Descriptions.Kinds)
	}
	if cfg.Read.MaxFileSize <= 0 {
		t.Fatalf("Read.MaxFileSize = %d, want normalized default", cfg.Read.MaxFileSize)
	}
}

func TestEffectivePreToolScan(t *testing.T) {
	if got := DefaultConfig().EffectivePreToolScan(); got != PreToolScanDefault {
		t.Fatalf("default EffectivePreToolScan = %q, want default", got)
	}
	cases := map[string]PreToolScanMode{
		"":          PreToolScanDefault,
		"none":      PreToolScanNone,
		"None":      PreToolScanNone,
		" default ": PreToolScanDefault,
		"FULL":      PreToolScanFull,
		// `hard` rebuilds from scratch, dropping every description and bug -- before EVERY
		// tool call. Validate rejects it outright; a config that reaches here unvalidated
		// gets the incremental scan rather than a rebuild.
		"Hard":  PreToolScanDefault,
		"bogus": PreToolScanDefault,
	}
	for in, want := range cases {
		c := &Config{Scan: ScanSection{PreTool: PreToolScanMode(in)}}
		if got := c.EffectivePreToolScan(); got != want {
			t.Fatalf("EffectivePreToolScan(%q) = %q, want %q", in, got, want)
		}
	}

	// And it is refused loudly, so the setting is a typo someone fixes rather than a quiet
	// downgrade they never notice.
	hard := DefaultConfig()
	hard.Scan.PreTool = PreToolScanHard
	if err := hard.Validate(); err == nil {
		t.Fatal("scan.pre_tool: hard must be rejected -- it clears descriptions on every tool call")
	}
}

func TestEffectivePipePassthrough(t *testing.T) {
	if !DefaultConfig().EffectivePipePassthrough() {
		t.Fatal("default EffectivePipePassthrough should be true")
	}
	var c Config // zero value: PipePassthrough nil
	if !c.EffectivePipePassthrough() {
		t.Fatal("nil pipe_passthrough should default to true")
	}
	c.Read.PipePassthrough = boolPtr(false)
	if c.EffectivePipePassthrough() {
		t.Fatal("explicit pipe_passthrough=false should be false")
	}
	c.Read.PipePassthrough = boolPtr(true)
	if !c.EffectivePipePassthrough() {
		t.Fatal("explicit pipe_passthrough=true should be true")
	}
}

func TestLoadConfigPreToolScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	// A valid scan.pre_tool value survives loading.
	if err := os.WriteFile(path, []byte(`{"scan":{"pre_tool":"full"}}`), 0644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg, ok := LoadConfigStrict(path)
	if !ok {
		t.Fatal("config with scan.pre_tool should parse cleanly")
	}
	if cfg.Scan.PreTool != PreToolScanFull {
		t.Fatalf("Scan.PreTool = %q, want full", cfg.Scan.PreTool)
	}

	// An absent or invalid value normalizes to the incremental scan, so a project that
	// never heard of the field still gets a fresh graph before each tool call.
	for _, body := range []string{
		`{"scan":{"pre_tool":"bogus"}}`,
		`{"read":{"max_file_size":524288}}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatalf("WriteFile config: %v", err)
		}
		cfg2, ok2 := LoadConfigStrict(path)
		if !ok2 {
			t.Fatalf("config %s should parse cleanly", body)
		}
		if cfg2.Scan.PreTool != PreToolScanDefault {
			t.Fatalf("Scan.PreTool for %s = %q, want default", body, cfg2.Scan.PreTool)
		}
	}

	// "none" is the one way to switch the freshness guarantee off.
	if err := os.WriteFile(path, []byte(`{"scan":{"pre_tool":"none"}}`), 0644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg3, ok3 := LoadConfigStrict(path)
	if !ok3 {
		t.Fatal("config with pre_tool none should parse cleanly")
	}
	if cfg3.Scan.PreTool != PreToolScanNone {
		t.Fatalf("Scan.PreTool = %q, want none", cfg3.Scan.PreTool)
	}
}

func TestEffectiveAgentInheritanceAndOverride(t *testing.T) {
	cfg := DefaultConfig()
	// MCP tools only exist in ModeMCP -- EffectiveAgent returns the SERVABLE list, so asking
	// about mcp_tools in any other mode correctly answers "none". Inheritance is what this
	// test is about, so it runs in the mode where there is something to inherit.
	cfg.Mode = ModeMCP
	hunter := cfg.EffectiveAgent("claude_code", "bug-hunter")
	if !containsConfigString(hunter.MCPTools, "bug_report") {
		t.Fatalf("bug-hunter should have bug_report: %v", hunter.MCPTools)
	}
	if containsConfigString(hunter.MCPTools, "edit") {
		t.Fatalf("bug-hunter should not have edit: %v", hunter.MCPTools)
	}

	// Per-harness model override wins for claude_code only.
	cfg.LLM.ClaudeCode.Agents["bug-hunter"] = AgentConfig{Model: "sonnet"}
	hunter = cfg.EffectiveAgent("claude_code", "bug-hunter")
	if hunter.Model != "sonnet" {
		t.Fatalf("claude_code bug-hunter model = %q, want sonnet", hunter.Model)
	}
	if !containsConfigString(hunter.MCPTools, "bug_report") {
		t.Fatalf("override should not drop inherited tools: %v", hunter.MCPTools)
	}
	if oc := cfg.EffectiveAgent("opencode", "bug-hunter"); oc.Model == "sonnet" {
		t.Fatalf("opencode bug-hunter should not inherit claude_code override: %q", oc.Model)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}

	// Unknown MCP tool on a sub-agent.
	cfg := DefaultConfig()
	cfg.LLM.Any.Agents["bug-hunter"] = AgentConfig{MCPTools: []string{"read", "read_inferface"}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "read_inferface") {
		t.Fatalf("expected unknown-tool error naming read_inferface, got: %v", err)
	}

	// Unknown chat tool.
	cfg = DefaultConfig()
	cfg.Viz.Chat.Agents.Agents["explorer"] = ChatAgentConfig{Tools: []string{"not_a_tool"}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "not_a_tool") {
		t.Fatalf("expected unknown chat-tool error, got: %v", err)
	}

	// A chat-only native tool is invalid as an MCP tool.
	cfg = DefaultConfig()
	cfg.LLM.Any.MainAgent.MCPTools = append(cfg.LLM.Any.MainAgent.MCPTools, "ls")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "ls") {
		t.Fatalf("expected error for ls as MCP tool, got: %v", err)
	}
}

func TestVizChatAgentsRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Viz.Chat.Agents.Model = "deepseek/deepseek-v4-flash"
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loaded.Viz.Chat.Agents.Model != "deepseek/deepseek-v4-flash" {
		t.Fatalf("model = %q, want deepseek/deepseek-v4-flash", loaded.Viz.Chat.Agents.Model)
	}
	if _, ok := loaded.Viz.Chat.Agents.Agents["descriptions-generation-executor"]; !ok {
		t.Fatalf("executor agent lost in round-trip: %+v", loaded.Viz.Chat.Agents.Agents)
	}
}

func containsConfigString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestGrepDescriptionKindsDefaultWhenKeyAbsent(t *testing.T) {
	// An existing config predating the key must pick up the defaults rather than
	// silently losing description search.
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"read":{"max_file_size":524288}}`), 0644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg, ok := LoadConfigStrict(path)
	if !ok {
		t.Fatal("config should parse cleanly")
	}
	if len(cfg.Grep.DescriptionKinds) != len(DefaultGrepDescriptionKinds()) {
		t.Fatalf("Grep.DescriptionKinds = %v, want defaults", cfg.Grep.DescriptionKinds)
	}
	if len(DefaultConfig().Grep.DescriptionKinds) == 0 {
		t.Fatal("DefaultConfig must ship the grep description kinds")
	}
}

func TestGrepDescriptionKindsEmptyListDisablesAndSurvivesNormalization(t *testing.T) {
	// [] is a deliberate "never match on a description". Back-filling it into the
	// defaults, the way an empty Descriptions.Kinds is treated, would make the
	// setting impossible to express.
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"grep":{"description_kinds":[]}}`), 0644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg, ok := LoadConfigStrict(path)
	if !ok {
		t.Fatal("config should parse cleanly")
	}
	if cfg.Grep.DescriptionKinds == nil {
		t.Fatal("an explicit [] must stay non-nil, or it reads as unset")
	}
	if len(cfg.Grep.DescriptionKinds) != 0 {
		t.Fatalf("Grep.DescriptionKinds = %v, want empty", cfg.Grep.DescriptionKinds)
	}
}

func TestGrepDescriptionKindsAreNormalized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"grep":{"description_kinds":["functions","named-type"]}}`), 0644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg, ok := LoadConfigStrict(path)
	if !ok {
		t.Fatal("config should parse cleanly")
	}
	want := []domain.ResourceKind{domain.ResourceFunction, domain.ResourceNamedType}
	if len(cfg.Grep.DescriptionKinds) != len(want) {
		t.Fatalf("Grep.DescriptionKinds = %v, want %v", cfg.Grep.DescriptionKinds, want)
	}
	for i := range want {
		if cfg.Grep.DescriptionKinds[i] != want[i] {
			t.Fatalf("Grep.DescriptionKinds = %v, want %v", cfg.Grep.DescriptionKinds, want)
		}
	}
}

// topogrep restates this default so it stays free of the config package; the two
// must not drift.
func TestDefaultDescriptionKindsMatchConfig(t *testing.T) {
	cfg, lib := DefaultGrepDescriptionKinds(), topogrep.DefaultDescriptionKinds()
	if len(cfg) != len(lib) {
		t.Fatalf("helper=%v topogrep=%v", cfg, lib)
	}
	for i := range cfg {
		if cfg[i] != lib[i] {
			t.Fatalf("helper=%v topogrep=%v", cfg, lib)
		}
	}
}

// contract_verbosity is validated at load/init time, the same way mode is, so a typo fails
// fast and loudly instead of silently costing a project the long contract it asked for.
func TestValidateRejectsAnUnknownContractVerbosity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ContractVerbosity = "verbose"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted contract_verbosity \"verbose\"")
	}
	for _, ok := range []string{"", ContractVerbosityLow, ContractVerbosityHigh, "HIGH", " low "} {
		cfg.ContractVerbosity = ok
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate rejected contract_verbosity %q: %v", ok, err)
		}
	}
}

// EffectiveContractVerbosity resolves a typo to the DEFAULT rather than failing, for the same
// reason EffectiveMode does: a config aracne cannot read must never leave a project with no
// contract. The default it falls back to has to be the cheap one -- every byte of the contract
// is re-sent on every request, so a typo must not silently quadruple that.
func TestEffectiveContractVerbosityFallsBackToLow(t *testing.T) {
	for _, in := range []string{"", "verbose", "medium", "  "} {
		cfg := DefaultConfig()
		cfg.ContractVerbosity = in
		if got := cfg.EffectiveContractVerbosity(); got != ContractVerbosityLow {
			t.Errorf("contract_verbosity %q resolved to %q, want %q", in, got, ContractVerbosityLow)
		}
	}
	cfg := DefaultConfig()
	cfg.ContractVerbosity = " HIGH "
	if got := cfg.EffectiveContractVerbosity(); got != ContractVerbosityHigh {
		t.Errorf("contract_verbosity %q resolved to %q, want %q", " HIGH ", got, ContractVerbosityHigh)
	}
}
