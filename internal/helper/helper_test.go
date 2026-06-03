package helper

import (
	"os"
	"testing"

	"ltp/internal/topology/domain"
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
