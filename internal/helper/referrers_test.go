package helper

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func res(id, kind, path string, conns map[string][]string) domain.Resource {
	return domain.Resource{
		ID:          id,
		Kind:        domain.ResourceKind(kind),
		Name:        id,
		Location:    domain.Location{Path: path},
		Connections: conns,
	}
}

func topoWith(resources ...domain.Resource) *domain.Topology {
	t := &domain.Topology{
		Resources: make(map[string]domain.Resource, len(resources)),
		Warnings:  map[string]domain.TopologyWarning{},
	}
	for _, r := range resources {
		t.Resources[r.ID] = r
	}
	return t
}

// TestScanReferrers_AttributesToReferrer covers every reference edge kind.
// Attribution is the whole point: a warning whose SourceID is the symbol that
// just disappeared gets deleted by CleanupOrphanedWarnings before it can reach
// the database, which is why five of six languages reported nothing.
func TestScanReferrers_AttributesToReferrer(t *testing.T) {
	for _, connType := range []string{"calls", "uses_struct", "uses_class", "uses_named_type", "uses_interface", "uses_extvar"} {
		t.Run(connType, func(t *testing.T) {
			topo := topoWith(
				res("pkg.Caller", "function", "/proj/b.go", map[string][]string{connType: {"pkg.Gone"}}),
			)
			warnings := ScanReferrers(topo, ReferrerScan{
				Removed:   map[string]bool{"pkg.Gone": true},
				Origin:    "/proj/a.go",
				ConnTypes: ReferenceConnTypes,
			})
			if len(warnings) != 1 {
				t.Fatalf("expected 1 warning, got %d: %+v", len(warnings), warnings)
			}
			w := warnings[0]
			if w.SourceID != "pkg.Caller" {
				t.Errorf("SourceID = %q, want the surviving referrer pkg.Caller", w.SourceID)
			}
			if w.TargetID != "pkg.Gone" {
				t.Errorf("TargetID = %q, want pkg.Gone", w.TargetID)
			}
			if w.Kind != domain.WarnNodeRemoved {
				t.Errorf("Kind = %q, want node_removed", w.Kind)
			}
			// goscanner's id scheme, so the two emitters collapse onto one key
			if want := "pkg.Caller@node_removed@pkg.Gone"; w.ID != want {
				t.Errorf("ID = %q, want %q", w.ID, want)
			}
			if !strings.Contains(w.Message, connType) {
				t.Errorf("message should name the edge kind %q, got %q", connType, w.Message)
			}
		})
	}
}

// TestScanReferrers_IgnoresOwnershipEdges: a package losing a has_function edge
// is not a "go verify this caller" signal, and neither is a structural reverse
// edge that every scanner recomputes on each pass.
func TestScanReferrers_IgnoresOwnershipEdges(t *testing.T) {
	topo := topoWith(
		res("pkg", "package", "", map[string][]string{
			"has_function": {"pkg.Gone"},
			"uses_package": {"pkg.Gone"},
		}),
		res("pkg.Impl", "struct", "/proj/c.go", map[string][]string{
			"implemented_by": {"pkg.Gone"},
			"methods":        {"pkg.Gone"},
		}),
	)
	warnings := ScanReferrers(topo, ReferrerScan{
		Removed:   map[string]bool{"pkg.Gone": true},
		ConnTypes: ReferenceConnTypes,
	})
	if len(warnings) != 0 {
		t.Fatalf("ownership/structural edges must not warn, got %+v", warnings)
	}
}

// TestScanReferrers_NilConnTypesAcceptsEveryEdge is the whole-file-removal mode
// that RemoveFileResources relies on.
func TestScanReferrers_NilConnTypesAcceptsEveryEdge(t *testing.T) {
	topo := topoWith(
		res("pkg", "package", "", map[string][]string{"has_function": {"pkg.Gone"}}),
	)
	warnings := ScanReferrers(topo, ReferrerScan{
		Removed: map[string]bool{"pkg.Gone": true},
		Origin:  "pkg/a.go",
	})
	if len(warnings) != 1 {
		t.Fatalf("nil ConnTypes must accept every edge kind, got %+v", warnings)
	}
}

// TestScanReferrers_SkipSourceAndRemovedReferrers: a referrer inside the file
// that was just re-parsed is handled by the scanner itself, and a referrer that
// was removed in the same update has no one to tell.
func TestScanReferrers_SkipSourceAndRemovedReferrers(t *testing.T) {
	topo := topoWith(
		res("pkg.SameFile", "function", "/proj/a.go", map[string][]string{"calls": {"pkg.Gone"}}),
		res("pkg.OtherFile", "function", "/proj/b.go", map[string][]string{"calls": {"pkg.Gone"}}),
		res("pkg.AlsoGone", "function", "/proj/a.go", map[string][]string{"calls": {"pkg.Gone"}}),
	)
	warnings := ScanReferrers(topo, ReferrerScan{
		Removed:   map[string]bool{"pkg.Gone": true, "pkg.AlsoGone": true},
		Origin:    "/proj/a.go",
		ConnTypes: ReferenceConnTypes,
		SkipSource: func(id string) bool {
			r, ok := topo.Resources[id]
			return ok && r.Location.Path == "/proj/a.go"
		},
	})
	if len(warnings) != 1 || warnings[0].SourceID != "pkg.OtherFile" {
		t.Fatalf("expected only the other-file referrer to warn, got %+v", warnings)
	}
}

func TestScanReferrers_EmptyRemovedSetIsANoOp(t *testing.T) {
	topo := topoWith(res("pkg.Caller", "function", "/proj/b.go", map[string][]string{"calls": {"pkg.X"}}))
	if w := ScanReferrers(topo, ReferrerScan{Removed: nil, ConnTypes: ReferenceConnTypes}); w != nil {
		t.Fatalf("expected no warnings for an empty removed set, got %+v", w)
	}
}

// TestClearReferrerWarningsForFile: the re-parsed file is authoritative about
// what it references now.
func TestClearReferrerWarningsForFile(t *testing.T) {
	topo := topoWith(
		res("pkg.InFile", "function", "/proj/a.go", nil),
		res("pkg.Elsewhere", "function", "/proj/b.go", nil),
	)
	topo.Warnings["pkg.InFile@node_removed@pkg.Gone"] = domain.TopologyWarning{
		ID: "pkg.InFile@node_removed@pkg.Gone", SourceID: "pkg.InFile",
		Kind: domain.WarnNodeRemoved, TargetID: "pkg.Gone",
	}
	topo.Warnings["pkg.Elsewhere@node_removed@pkg.Gone"] = domain.TopologyWarning{
		ID: "pkg.Elsewhere@node_removed@pkg.Gone", SourceID: "pkg.Elsewhere",
		Kind: domain.WarnNodeRemoved, TargetID: "pkg.Gone",
	}
	// a different kind attributed to the same file must be left alone
	topo.Warnings["pkg.InFile@signature_changed@x"] = domain.TopologyWarning{
		ID: "pkg.InFile@signature_changed@x", SourceID: "pkg.InFile",
		Kind: domain.WarnSignatureChanged, TargetID: "x",
	}

	ClearReferrerWarningsForFile(topo, "/proj/a.go")

	if _, still := topo.Warnings["pkg.InFile@node_removed@pkg.Gone"]; still {
		t.Error("a node_removed warning for the re-parsed file should be cleared")
	}
	if _, gone := topo.Warnings["pkg.Elsewhere@node_removed@pkg.Gone"]; !gone {
		t.Error("warnings for other files must be left alone")
	}
	if _, gone := topo.Warnings["pkg.InFile@signature_changed@x"]; !gone {
		t.Error("only node_removed warnings should be cleared")
	}
}

func TestClearReferrerWarningsForFile_EmptyPathIsANoOp(t *testing.T) {
	topo := topoWith(res("pkg.A", "function", "", nil))
	topo.Warnings["w"] = domain.TopologyWarning{ID: "w", SourceID: "pkg.A", Kind: domain.WarnNodeRemoved, TargetID: "x"}
	ClearReferrerWarningsForFile(topo, "")
	if len(topo.Warnings) != 1 {
		t.Fatal("an empty path must not clear anything")
	}
}

// TestResolveReferrerWarnings: restoring a deleted symbol clears the warning
// about deleting it. Without this the channel fills with permanently stale
// entries and agents learn to ignore it.
func TestResolveReferrerWarnings(t *testing.T) {
	topo := topoWith(
		res("pkg.Caller", "function", "/proj/b.go", map[string][]string{"calls": {"pkg.Back"}}),
		res("pkg.Back", "function", "/proj/a.go", nil),
	)
	topo.Warnings["pkg.Caller@node_removed@pkg.Back"] = domain.TopologyWarning{
		ID: "pkg.Caller@node_removed@pkg.Back", SourceID: "pkg.Caller",
		Kind: domain.WarnNodeRemoved, TargetID: "pkg.Back",
	}
	topo.Warnings["pkg.Caller@node_removed@pkg.StillGone"] = domain.TopologyWarning{
		ID: "pkg.Caller@node_removed@pkg.StillGone", SourceID: "pkg.Caller",
		Kind: domain.WarnNodeRemoved, TargetID: "pkg.StillGone",
	}

	ResolveReferrerWarnings(topo)

	if _, still := topo.Warnings["pkg.Caller@node_removed@pkg.Back"]; still {
		t.Error("a warning whose target is back in the graph should be cleared")
	}
	if _, gone := topo.Warnings["pkg.Caller@node_removed@pkg.StillGone"]; !gone {
		t.Error("a warning whose target is still missing must be kept")
	}
}

// TestRemoveFileResources_BehaviourUnchanged pins the exact id and message of
// the whole-file-removal path, so extracting ScanReferrers out of it is
// provably behaviour-preserving.
func TestRemoveFileResources_BehaviourUnchanged(t *testing.T) {
	topo := topoWith(
		res("pkg/a.go", "file", "pkg/a.go", map[string][]string{"has_function": {"pkg.Removed"}}),
		res("pkg.Removed", "function", "pkg/a.go", nil),
		res("pkg.Caller", "function", "pkg/b.go", map[string][]string{"calls": {"pkg.Removed"}}),
	)

	warnings := RemoveFileResources(topo, "pkg/a.go")

	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %+v", len(warnings), warnings)
	}
	w := warnings[0]
	if want := "pkg.Caller@node_removed@pkg.Removed"; w.ID != want {
		t.Errorf("ID = %q, want %q", w.ID, want)
	}
	if w.SourceID != "pkg.Caller" || w.TargetID != "pkg.Removed" || w.Kind != domain.WarnNodeRemoved {
		t.Errorf("attribution changed: %+v", w)
	}
	want := "pkg.Removed was removed from pkg/a.go, verify pkg.Caller which references it via calls"
	if w.Message != want {
		t.Errorf("message = %q, want %q", w.Message, want)
	}
	// and the inbound edge is still pruned
	if got := topo.Resources["pkg.Caller"].Connections["calls"]; len(got) != 0 {
		t.Errorf("dangling edge should have been pruned, got %v", got)
	}
}

// TestScanReferrers_StripRemovesDanglingEdge: leaving the edge behind means the
// readers silently drop the neighbour from the CONTEXT block, so the agent sees
// an incomplete context with no signal that anything is wrong.
func TestScanReferrers_StripRemovesDanglingEdge(t *testing.T) {
	topo := topoWith(
		res("pkg.Caller", "function", "/proj/b.go", map[string][]string{
			"calls": {"pkg.Gone", "pkg.Kept"},
		}),
	)
	ScanReferrers(topo, ReferrerScan{
		Removed:   map[string]bool{"pkg.Gone": true},
		ConnTypes: ReferenceConnTypes,
		Strip:     true,
	})
	got := topo.Resources["pkg.Caller"].Connections["calls"]
	if len(got) != 1 || got[0] != "pkg.Kept" {
		t.Fatalf("calls = %v, want only pkg.Kept", got)
	}
}

func TestScanReferrers_StripDropsEmptiedConnType(t *testing.T) {
	topo := topoWith(
		res("pkg.Caller", "function", "/proj/b.go", map[string][]string{"calls": {"pkg.Gone"}}),
	)
	ScanReferrers(topo, ReferrerScan{
		Removed:   map[string]bool{"pkg.Gone": true},
		ConnTypes: ReferenceConnTypes,
		Strip:     true,
	})
	if _, still := topo.Resources["pkg.Caller"].Connections["calls"]; still {
		t.Fatal("an emptied edge kind should be removed, not left as an empty slice")
	}
}

func TestScanReferrers_NoStripByDefault(t *testing.T) {
	topo := topoWith(
		res("pkg.Caller", "function", "/proj/b.go", map[string][]string{"calls": {"pkg.Gone"}}),
	)
	ScanReferrers(topo, ReferrerScan{
		Removed:   map[string]bool{"pkg.Gone": true},
		ConnTypes: ReferenceConnTypes,
	})
	if got := topo.Resources["pkg.Caller"].Connections["calls"]; len(got) != 1 {
		t.Fatalf("Strip is opt-in; edges must be untouched, got %v", got)
	}
}

// TestReferenceConnType pins the language/kind table. The class asymmetry is the
// only real trap: JS, TS and Python map a class to ResourceStruct but reference
// it with uses_class, while Go, Rust and Java use uses_struct. Get this wrong
// and a restored edge is written under an edge kind nothing reads.
func TestReferenceConnType(t *testing.T) {
	cases := []struct {
		lang string
		kind domain.ResourceKind
		want string
	}{
		{"go", domain.ResourceFunction, "calls"},
		{"python", domain.ResourceMethod, "calls"},
		{"go", domain.ResourceStruct, "uses_struct"},
		{"rust", domain.ResourceStruct, "uses_struct"},
		{"java", domain.ResourceStruct, "uses_struct"},
		{"javascript", domain.ResourceStruct, "uses_class"},
		{"typescript", domain.ResourceStruct, "uses_class"},
		{"python", domain.ResourceStruct, "uses_class"},
		{"go", domain.ResourceInterface, "uses_interface"},
		{"go", domain.ResourceNamedType, "uses_named_type"},
		{"go", domain.ResourceVariable, "uses_extvar"},
		{"go", domain.ResourceKind("package"), ""},
		{"go", domain.ResourceKind("file"), ""},
	}
	for _, c := range cases {
		if got := ReferenceConnType(c.lang, c.kind); got != c.want {
			t.Errorf("ReferenceConnType(%q, %q) = %q, want %q", c.lang, c.kind, got, c.want)
		}
	}
	// every kind the table produces must be a recognised reference edge
	for _, c := range cases {
		if c.want != "" && !ReferenceConnTypes[c.want] {
			t.Errorf("%q is not in ReferenceConnTypes; strip and restore would disagree", c.want)
		}
	}
}

// TestResolveReferrerWarnings_RestoresStrippedEdge is the property that makes
// stripping safe at all. The referrer's own file is not re-parsed when the
// target comes back, so if this did not restore the edge it would stay lost
// until a cold `scan --hard`.
func TestResolveReferrerWarnings_RestoresStrippedEdge(t *testing.T) {
	topo := topoWith(
		res("mod.Caller", "function", "/proj/b.js", nil),
		res("mod.Klass", "struct", "/proj/a.js", nil),
	)
	caller := topo.Resources["mod.Caller"]
	caller.Language = "javascript"
	topo.Resources["mod.Caller"] = caller

	topo.Warnings["mod.Caller@node_removed@mod.Klass"] = domain.TopologyWarning{
		ID: "mod.Caller@node_removed@mod.Klass", SourceID: "mod.Caller",
		Kind: domain.WarnNodeRemoved, TargetID: "mod.Klass",
	}

	ResolveReferrerWarnings(topo)

	if len(topo.Warnings) != 0 {
		t.Fatalf("warning should be cleared, got %+v", topo.Warnings)
	}
	got := topo.Resources["mod.Caller"].Connections["uses_class"]
	if len(got) != 1 || got[0] != "mod.Klass" {
		t.Fatalf("uses_class = %v, want the restored edge to mod.Klass", got)
	}
}

func TestResolveReferrerWarnings_DoesNotDuplicateExistingEdge(t *testing.T) {
	topo := topoWith(
		res("pkg.Caller", "function", "/proj/b.go", map[string][]string{"calls": {"pkg.Back"}}),
		res("pkg.Back", "function", "/proj/a.go", nil),
	)
	topo.Warnings["pkg.Caller@node_removed@pkg.Back"] = domain.TopologyWarning{
		ID: "pkg.Caller@node_removed@pkg.Back", SourceID: "pkg.Caller",
		Kind: domain.WarnNodeRemoved, TargetID: "pkg.Back",
	}

	ResolveReferrerWarnings(topo)

	if got := topo.Resources["pkg.Caller"].Connections["calls"]; len(got) != 1 {
		t.Fatalf("calls = %v, want no duplicate", got)
	}
}

// A referrer that itself disappeared has nothing to restore onto.
func TestResolveReferrerWarnings_ToleratesMissingReferrer(t *testing.T) {
	topo := topoWith(res("pkg.Back", "function", "/proj/a.go", nil))
	topo.Warnings["pkg.Gone@node_removed@pkg.Back"] = domain.TopologyWarning{
		ID: "pkg.Gone@node_removed@pkg.Back", SourceID: "pkg.Gone",
		Kind: domain.WarnNodeRemoved, TargetID: "pkg.Back",
	}
	ResolveReferrerWarnings(topo)
	if len(topo.Warnings) != 0 {
		t.Fatal("the warning should still be cleared when the referrer is gone")
	}
}

// TestRemoveFileResources_KeepsSymbolsThatMovedToAnotherFile is the rename case.
//
// IncrementalScan registers added and modified files BEFORE it removes deleted ones, so a
// symbol whose file was renamed has already been re-registered against the new path by the time
// the removal runs. Deleting by id alone took it back out: in Go a resource id is
// `module/package.Symbol` and carries no filename, so `mv pkg/a.go pkg/b.go` collided on every
// id in the file. The graph kept the new FILE node and lost every declaration in it, and no
// later incremental scan restored them because the manifest already called b.go current.
func TestRemoveFileResources_KeepsSymbolsThatMovedToAnotherFile(t *testing.T) {
	topo := topoWith(
		// The renamed-away file, still owning both symbols in the pre-update graph.
		res("pkg/a.go", "file", "", map[string][]string{"has_function": {"pkg.Moved", "pkg.Gone"}}),
		// Already re-registered against the new file by phase 1/2 of the scan.
		res("pkg.Moved", "function", "pkg/b.go", nil),
		// Genuinely deleted along with a.go: nothing re-homed it.
		res("pkg.Gone", "function", "pkg/a.go", nil),
		res("pkg/b.go", "file", "", map[string][]string{"has_function": {"pkg.Moved"}}),
		res("pkg", "package", "", map[string][]string{
			"has_file":     {"pkg/a.go", "pkg/b.go"},
			"has_function": {"pkg.Moved", "pkg.Gone"},
		}),
	)

	warnings := RemoveFileResources(topo, "pkg/a.go")

	if _, ok := topo.Resources["pkg.Moved"]; !ok {
		t.Error("a symbol re-registered against another file must survive the removal")
	}
	if _, ok := topo.Resources["pkg.Gone"]; ok {
		t.Error("a symbol that still lives in the removed file must be deleted")
	}
	if _, ok := topo.Resources["pkg/a.go"]; ok {
		t.Error("the removed file node itself must always go")
	}

	// The ownership edges the new file and the package hold on the survivor are what make it
	// reachable at all; the strip pass reads the same set, so it has to spare them too.
	if got := topo.Resources["pkg/b.go"].Connections["has_function"]; len(got) != 1 || got[0] != "pkg.Moved" {
		t.Errorf("the new file lost its member: has_function = %v", got)
	}
	if got := topo.Resources["pkg"].Connections["has_function"]; len(got) != 1 || got[0] != "pkg.Moved" {
		t.Errorf("the package lost the moved function: has_function = %v", got)
	}
	// The stale edge to the file that really is gone is still pruned.
	if got := topo.Resources["pkg"].Connections["has_file"]; len(got) != 1 || got[0] != "pkg/b.go" {
		t.Errorf("the package kept a dangling has_file: %v", got)
	}

	// And the model is not told to go and verify a declaration that never moved.
	for _, w := range warnings {
		if w.TargetID == "pkg.Moved" {
			t.Errorf("warned about a symbol that is still in the graph: %+v", w)
		}
	}
}

// A file node carries an empty Location.Path -- its identity IS its path -- so the ownership
// test must never be applied to it, or a whole-file removal would remove nothing.
func TestRemoveFileResources_FileNodeIsNeverSparedByTheOwnershipTest(t *testing.T) {
	topo := topoWith(
		res("pkg/a.go", "file", "", map[string][]string{"has_function": {"pkg.Only"}}),
		res("pkg.Only", "function", "pkg/a.go", nil),
	)

	RemoveFileResources(topo, "pkg/a.go")

	if len(topo.Resources) != 0 {
		t.Errorf("whole-file removal left %d resource(s): %v", len(topo.Resources), topo.Resources)
	}
}
