package domain

import (
	"testing"
)

func TestResourceKindConstants(t *testing.T) {
	tests := []struct {
		kind ResourceKind
		want string
	}{
		{ResourcePackage, "package"},
		{ResourceFile, "file"},
		{ResourceFunction, "function"},
		{ResourceMethod, "method"},
		{ResourceStruct, "struct"},
		{ResourceNamedType, "named_type"},
		{ResourceInterface, "interface"},
		{ResourceVariable, "variable"},
		{ResourceDependency, "dependency"},
	}
	for _, tt := range tests {
		if string(tt.kind) != tt.want {
			t.Errorf("ResourceKind(%s) = %q, want %q", tt.want, string(tt.kind), tt.want)
		}
	}
}

func TestBugStateConstants(t *testing.T) {
	tests := []struct {
		state BugState
		want  string
	}{
		{BugPending, "pending"},
		{BugAcknowledged, "acknowledged"},
		{BugDismissed, "dismissed"},
	}
	for _, tt := range tests {
		if string(tt.state) != tt.want {
			t.Errorf("BugState(%s) = %q, want %q", tt.want, string(tt.state), tt.want)
		}
	}
}

func TestWarningKindConstants(t *testing.T) {
	tests := []struct {
		kind WarningKind
		want string
	}{
		{WarnUseMissingNode, "use_missing_node"},
		{WarnNodeRemoved, "node_removed"},
		{WarnSignatureChanged, "signature_changed"},
	}
	for _, tt := range tests {
		if string(tt.kind) != tt.want {
			t.Errorf("WarningKind(%s) = %q, want %q", tt.want, string(tt.kind), tt.want)
		}
	}
}

func TestLocationDefaults(t *testing.T) {
	loc := Location{}
	if loc.StartsAt != 0 {
		t.Errorf("expected StartsAt=0, got %d", loc.StartsAt)
	}
	if loc.EndsAt != 0 {
		t.Errorf("expected EndsAt=0, got %d", loc.EndsAt)
	}
	if loc.Path != "" {
		t.Errorf("expected empty Path, got %q", loc.Path)
	}
}

func TestLocationCreation(t *testing.T) {
	loc := Location{StartsAt: 10, EndsAt: 20, Path: "/test/file.go"}
	if loc.StartsAt != 10 {
		t.Errorf("expected StartsAt=10, got %d", loc.StartsAt)
	}
	if loc.EndsAt != 20 {
		t.Errorf("expected EndsAt=20, got %d", loc.EndsAt)
	}
	if loc.Path != "/test/file.go" {
		t.Errorf("expected Path=/test/file.go, got %q", loc.Path)
	}
}

func TestCodeEntry(t *testing.T) {
	loc := Location{StartsAt: 1, EndsAt: 3, Path: "test.go"}
	entry := CodeEntry{Location: loc, Cut: "line1\nline2\nline3"}
	if entry.Cut != "line1\nline2\nline3" {
		t.Errorf("expected cut text, got %q", entry.Cut)
	}
	if entry.Location.Path != "test.go" {
		t.Errorf("expected path test.go, got %q", entry.Location.Path)
	}
}

func TestKnownBug(t *testing.T) {
	bug := KnownBug{
		ID:          "bug_1",
		NodeID:      "node_1",
		Description: "nil pointer dereference",
		State:       BugPending,
	}
	if bug.ID != "bug_1" {
		t.Errorf("expected bug ID bug_1, got %q", bug.ID)
	}
	if bug.State != BugPending {
		t.Errorf("expected state pending, got %q", bug.State)
	}
	bug.State = BugAcknowledged
	if bug.State != BugAcknowledged {
		t.Errorf("expected state acknowledged, got %q", bug.State)
	}
}

func TestResourceConstruction(t *testing.T) {
	res := Resource{
		ID:          "res_1",
		Kind:        ResourceFunction,
		Name:        "DoSomething",
		Description: "does something important",
		Location:    Location{StartsAt: 1, EndsAt: 10, Path: "main.go"},
		Properties:  map[string]any{"input": []string{"x int"}},
		Connections: map[string][]string{"calls": {"func_2"}},
	}
	if string(res.Kind) != "function" {
		t.Errorf("expected kind function, got %q", res.Kind)
	}
	if len(res.Connections["calls"]) != 1 || res.Connections["calls"][0] != "func_2" {
		t.Errorf("expected connection to func_2")
	}
}

func TestTopologyWarning(t *testing.T) {
	w := TopologyWarning{
		ID:       "warn_1",
		SourceID: "func_1",
		Kind:     WarnUseMissingNode,
		TargetID: "func_2",
		Message:  "func_1 references missing func_2",
	}
	if w.Kind != WarnUseMissingNode {
		t.Errorf("expected kind use_missing_node, got %q", w.Kind)
	}
	if w.Message != "func_1 references missing func_2" {
		t.Errorf("unexpected message: %q", w.Message)
	}
}

func TestTopologyConstruction(t *testing.T) {
	topo := &Topology{
		Root:     "/project",
		Language: "go",
		Resources: map[string]Resource{
			"f1": {ID: "f1", Kind: ResourceFunction, Name: "Foo"},
			"s1": {ID: "s1", Kind: ResourceStruct, Name: "Bar"},
		},
		Warnings: map[string]TopologyWarning{
			"w1": {ID: "w1", SourceID: "f1", Kind: WarnUseMissingNode, TargetID: "nonexistent"},
		},
		Errors: map[string]string{
			"file.go": "parse error",
		},
	}
	if topo.Root != "/project" {
		t.Errorf("expected root /project, got %q", topo.Root)
	}
	if topo.Language != "go" {
		t.Errorf("expected language go, got %q", topo.Language)
	}
	if len(topo.Resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(topo.Resources))
	}
	if len(topo.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(topo.Warnings))
	}
	if len(topo.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(topo.Errors))
	}
}
