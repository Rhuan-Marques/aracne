package helper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aracne/internal/topology/domain"
)

func TestJSONRoundtrip(t *testing.T) {
	path := "test_topology.json"
	defer os.Remove(path)

	topo := &domain.Topology{
		Root:     "/test",
		Language: "go",
		Resources: map[string]domain.Resource{
			"f1": {
				ID:       "f1",
				Kind:     domain.ResourceFunction,
				Name:     "Foo",
				Location: domain.Location{StartsAt: 1, EndsAt: 10, Path: "main.go"},
			},
		},
		Warnings: map[string]domain.TopologyWarning{
			"w1": {
				ID: "w1", SourceID: "f1",
				Kind: domain.WarnUseMissingNode, TargetID: "f2",
				Message: "missing reference",
			},
		},
		Errors: map[string]string{"file.go": "parse error"},
	}

	if err := WriteJson(topo, path); err != nil {
		t.Fatalf("WriteJson: %v", err)
	}

	read, err := ReadJson(path)
	if err != nil {
		t.Fatalf("ReadJson: %v", err)
	}

	if read.Root != "/test" {
		t.Errorf("expected root /test, got %q", read.Root)
	}
	if read.Language != "go" {
		t.Errorf("expected language go, got %q", read.Language)
	}
	if len(read.Resources) != 1 {
		t.Errorf("expected 1 resource, got %d", len(read.Resources))
	}
	if len(read.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(read.Warnings))
	}
	if len(read.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(read.Errors))
	}
}

func TestReadJsonNonexistent(t *testing.T) {
	_, err := ReadJson("nonexistent.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

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

func TestDiffScanFilesRefusesMassDeleteWhenNoCurrentFiles(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "file_manifest.json")
	manifest := FileManifest{
		filepath.Join(root, "main.go"): "2026-01-01T00:00:00Z",
	}
	if err := WriteManifest(manifest, manifestPath); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	_, _, deleted, err := DiffScanFiles(root, "go", manifestPath)
	if err == nil {
		t.Fatal("expected no source files guard error")
	}
	if len(deleted) != 0 {
		t.Fatalf("expected no deleted files on guard error, got %v", deleted)
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
		path     string
		language string
		want     bool
	}{
		{path: "main.go", language: "go", want: true},
		{path: "main_test.go", language: "go", want: false},
		{path: "pkg/test_main.py", language: "python", want: false},
		{path: "pkg/main.py", language: "python", want: true},
		{path: ".opencode/plugins/hook.js", language: "go", want: false},
		{path: "node_modules/pkg/file.go", language: "go", want: false},
	}

	for _, tt := range tests {
		if got := IsSourceFile(tt.path, tt.language); got != tt.want {
			t.Fatalf("IsSourceFile(%q, %q) = %v, want %v", tt.path, tt.language, got, tt.want)
		}
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

func TestClearDescriptionsFiltersTargets(t *testing.T) {
	path := "test_cleardesc.db"
	defer os.Remove(path)

	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"f1": {ID: "f1", Kind: domain.ResourceFunction, Name: "Foo", Description: "function description"},
			"t1": {ID: "t1", Kind: domain.ResourceType, Name: "Thing", Description: "type description"},
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

func TestApplyDescriptions(t *testing.T) {
	filePath := "test_apply.go"
	defer os.Remove(filePath)

	original := "package test\n\nfunc Foo() int {\n\treturn 42\n}\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"test.Foo": {
				ID:          "test.Foo",
				Kind:        domain.ResourceFunction,
				Name:        "Foo",
				Description: "Foo returns 42",
				Location:    domain.Location{StartsAt: 3, EndsAt: 5, Path: filePath},
			},
		},
	}

	if err := ApplyDescriptions(topo); err != nil {
		t.Fatalf("ApplyDescriptions: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if len(content) <= len(original) {
		t.Error("expected file to grow after applying description")
	}
	if !containsStr(content, "Foo returns 42") {
		t.Errorf("expected description in output, got:\n%s", content)
	}
}

func TestApplyDescriptions_NoDescriptionResource(t *testing.T) {
	filePath := "test_apply_node.go"
	defer os.Remove(filePath)

	original := "package test\n\nfunc Bar() int {\n\treturn 7\n}\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"test.Bar": {
				ID:          "test.Bar",
				Kind:        domain.ResourceFunction,
				Name:        "Bar",
				Description: "",
				Location:    domain.Location{StartsAt: 3, EndsAt: 5, Path: filePath},
			},
		},
	}

	if err := ApplyDescriptions(topo); err != nil {
		t.Fatalf("ApplyDescriptions: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Error("expected no change for empty description")
	}
}

func TestApplyDescriptions_NoLocation(t *testing.T) {
	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"test.Foo": {
				ID:          "test.Foo",
				Kind:        domain.ResourceFunction,
				Name:        "Foo",
				Description: "description",
			},
		},
	}
	if err := ApplyDescriptions(topo); err != nil {
		t.Fatalf("ApplyDescriptions: %v", err)
	}
}

func TestApplyDescriptions_SkippedKinds(t *testing.T) {
	filePath := "test_apply_skip.go"
	defer os.Remove(filePath)

	original := "package test\n\nconst X = 1\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	topo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"test.X": {
				ID:          "test.X",
				Kind:        domain.ResourceDependency,
				Name:        "X",
				Description: "some dep",
				Location:    domain.Location{StartsAt: 3, EndsAt: 3, Path: filePath},
			},
		},
	}

	if err := ApplyDescriptions(topo); err != nil {
		t.Fatalf("ApplyDescriptions: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Error("expected no change for dependency kind")
	}
}

func TestDefaultConfigDescriptions(t *testing.T) {
	cfg := DefaultConfig()
	want := []domain.ResourceKind{domain.ResourceFunction, domain.ResourceMethod, domain.ResourceType, domain.ResourceInterface, domain.ResourceFile}
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
	if cfg.Scan.Mode != ScanModeDefault {
		t.Fatalf("Scan.Mode = %q, want default", cfg.Scan.Mode)
	}
	if len(cfg.Descriptions.Kinds) != len(DefaultNeedDescription()) {
		t.Fatalf("Descriptions.Kinds = %v, want defaults", cfg.Descriptions.Kinds)
	}
}

func TestLoadConfigNewSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"descriptions":{"kinds":["file","type"]},"scan":{"mode":"all"}}`), 0644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg, ok := LoadConfigStrict(path)
	if !ok {
		t.Fatal("new-schema config should parse cleanly")
	}
	if len(cfg.Descriptions.Kinds) != 2 || cfg.Descriptions.Kinds[0] != domain.ResourceFile || cfg.Descriptions.Kinds[1] != domain.ResourceType {
		t.Fatalf("Descriptions.Kinds = %v, want [file type]", cfg.Descriptions.Kinds)
	}
	if cfg.Scan.Mode != ScanModeAll {
		t.Fatalf("Scan.Mode = %q, want all", cfg.Scan.Mode)
	}
	if cfg.Read.MaxFileSize <= 0 {
		t.Fatalf("Read.MaxFileSize = %d, want normalized default", cfg.Read.MaxFileSize)
	}
}

func TestEffectiveReadScan(t *testing.T) {
	if got := DefaultConfig().EffectiveReadScan(); got != ReadScanNone {
		t.Fatalf("default EffectiveReadScan = %q, want none", got)
	}
	cases := map[string]ReadScanMode{
		"":          ReadScanNone,
		"none":      ReadScanNone,
		"None":      ReadScanNone,
		" default ": ReadScanDefault,
		"FULL":      ReadScanFull,
		"Hard":      ReadScanHard,
		"bogus":     ReadScanNone,
	}
	for in, want := range cases {
		c := &Config{Read: ReadSection{Scan: ReadScanMode(in)}}
		if got := c.EffectiveReadScan(); got != want {
			t.Fatalf("EffectiveReadScan(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadConfigReadScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	// A valid read.scan value survives loading.
	if err := os.WriteFile(path, []byte(`{"scan":{"mode":"default"},"read":{"scan":"full"}}`), 0644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg, ok := LoadConfigStrict(path)
	if !ok {
		t.Fatal("config with read.scan should parse cleanly")
	}
	if cfg.Read.Scan != ReadScanFull {
		t.Fatalf("Read.Scan = %q, want full", cfg.Read.Scan)
	}

	// An invalid read.scan value normalizes to none (preserving current behavior).
	if err := os.WriteFile(path, []byte(`{"scan":{"mode":"default"},"read":{"scan":"bogus"}}`), 0644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg2, ok2 := LoadConfigStrict(path)
	if !ok2 {
		t.Fatal("config should parse cleanly")
	}
	if cfg2.Read.Scan != ReadScanNone {
		t.Fatalf("invalid Read.Scan normalized to %q, want none", cfg2.Read.Scan)
	}
}

func TestEffectiveAgentInheritanceAndOverride(t *testing.T) {
	cfg := DefaultConfig()
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

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStrInner(s, substr))
}

func containsStrInner(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
