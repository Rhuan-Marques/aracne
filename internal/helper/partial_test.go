package helper

import (
	"os"
	"reflect"
	"sort"
	"testing"

	"aracne/internal/topology/domain"
)

// buildPartialTopo builds a small two-file topology used by the partial-read
// and WriteDelta tests. File nodes own their members via has_function edges and
// carry no loc_path (matching WriteDb); members carry loc_path and may call
// across files.
func buildPartialTopo() *domain.Topology {
	return &domain.Topology{
		Root:      "/test",
		Language:  "go",
		Languages: []string{"go"},
		Resources: map[string]domain.Resource{
			"pkg/a.go": {
				ID:          "pkg/a.go",
				Kind:        domain.ResourceFile,
				Name:        "a.go",
				Connections: map[string][]string{"has_function": {"pkg.Foo", "pkg.Bar"}},
			},
			"pkg/b.go": {
				ID:          "pkg/b.go",
				Kind:        domain.ResourceFile,
				Name:        "b.go",
				Connections: map[string][]string{"has_function": {"pkg.Baz"}},
			},
			"pkg.Foo": {
				ID:          "pkg.Foo",
				Kind:        domain.ResourceFunction,
				Name:        "Foo",
				Language:    "go",
				Description: "does foo",
				Location:    domain.Location{StartsAt: 1, EndsAt: 5, Path: "pkg/a.go"},
				Properties:  map[string]any{"input": []any{}, "output": []any{}},
				Connections: map[string][]string{"calls": {"pkg.Baz"}},
			},
			"pkg.Bar": {
				ID:          "pkg.Bar",
				Kind:        domain.ResourceFunction,
				Name:        "Bar",
				Language:    "go",
				Location:    domain.Location{StartsAt: 7, EndsAt: 9, Path: "pkg/a.go"},
				Connections: map[string][]string{"calls": {"pkg.Foo"}},
			},
			"pkg.Baz": {
				ID:       "pkg.Baz",
				Kind:     domain.ResourceFunction,
				Name:     "Baz",
				Language: "go",
				Location: domain.Location{StartsAt: 1, EndsAt: 4, Path: "pkg/b.go"},
			},
		},
		Warnings: map[string]domain.TopologyWarning{},
		Errors:   map[string]string{},
	}
}

func writePartialTopo(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/partial.db"
	if err := WriteDb(buildPartialTopo(), path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	return path
}

func sortedKeys(m map[string]domain.Resource) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestReadResourcesByFile(t *testing.T) {
	path := writePartialTopo(t)

	got, err := ReadResourcesByFile(path, "pkg/a.go")
	if err != nil {
		t.Fatalf("ReadResourcesByFile: %v", err)
	}

	want := []string{"pkg.Bar", "pkg.Foo"}
	if keys := sortedKeys(got); !reflect.DeepEqual(keys, want) {
		t.Fatalf("members for pkg/a.go = %v, want %v", keys, want)
	}
	// The file node itself has loc_path == "" so it must NOT be returned.
	if _, ok := got["pkg/a.go"]; ok {
		t.Fatal("file node pkg/a.go should not be returned by ReadResourcesByFile")
	}
	// Outgoing connections must be hydrated.
	if calls := got["pkg.Foo"].Connections["calls"]; len(calls) != 1 || calls[0] != "pkg.Baz" {
		t.Fatalf("pkg.Foo calls = %v, want [pkg.Baz]", got["pkg.Foo"].Connections["calls"])
	}
}

func TestReadResourcesByIDsMatchesReadDb(t *testing.T) {
	path := writePartialTopo(t)

	full, err := ReadDb(path)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}

	ids := []string{"pkg.Foo", "pkg.Bar", "pkg.Baz"}
	got, err := ReadResourcesByIDs(path, ids)
	if err != nil {
		t.Fatalf("ReadResourcesByIDs: %v", err)
	}

	if len(got) != len(ids) {
		t.Fatalf("got %d resources, want %d", len(got), len(ids))
	}
	for _, id := range ids {
		res, ok := got[id]
		if !ok {
			t.Fatalf("missing resource %s", id)
		}
		want := full.Resources[id]
		if !reflect.DeepEqual(res, want) {
			t.Fatalf("resource %s mismatch with ReadDb:\n got=%+v\nwant=%+v", id, res, want)
		}
	}

	// Empty input yields an empty (non-nil) map.
	empty, err := ReadResourcesByIDs(path, nil)
	if err != nil {
		t.Fatalf("ReadResourcesByIDs(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("expected empty map for nil ids, got %v", empty)
	}
}

func TestReadResourcesByKind(t *testing.T) {
	path := writePartialTopo(t)

	funcs, err := ReadResourcesByKind(path, domain.ResourceFunction)
	if err != nil {
		t.Fatalf("ReadResourcesByKind(function): %v", err)
	}
	want := []string{"pkg.Bar", "pkg.Baz", "pkg.Foo"}
	if keys := sortedKeys(funcs); !reflect.DeepEqual(keys, want) {
		t.Fatalf("function resources = %v, want %v", keys, want)
	}
	for _, res := range funcs {
		if res.Kind != domain.ResourceFunction {
			t.Fatalf("resource %s has kind %q, want function", res.ID, res.Kind)
		}
	}

	files, err := ReadResourcesByKind(path, domain.ResourceFile)
	if err != nil {
		t.Fatalf("ReadResourcesByKind(file): %v", err)
	}
	if keys := sortedKeys(files); !reflect.DeepEqual(keys, []string{"pkg/a.go", "pkg/b.go"}) {
		t.Fatalf("file resources = %v, want [pkg/a.go pkg/b.go]", keys)
	}
	// File-node connections are hydrated too.
	if hf := files["pkg/a.go"].Connections["has_function"]; len(hf) != 2 {
		t.Fatalf("pkg/a.go has_function = %v, want 2 entries", hf)
	}
}

func TestReadReverseConnections(t *testing.T) {
	path := writePartialTopo(t)

	// pkg.Baz is called by pkg.Foo; pkg.Foo is called by pkg.Bar.
	rev, err := ReadReverseConnections(path, []string{"pkg.Baz", "pkg.Foo"}, "calls")
	if err != nil {
		t.Fatalf("ReadReverseConnections: %v", err)
	}
	if got := rev["pkg.Baz"]; !reflect.DeepEqual(got, []string{"pkg.Foo"}) {
		t.Fatalf("callers of pkg.Baz = %v, want [pkg.Foo]", got)
	}
	if got := rev["pkg.Foo"]; !reflect.DeepEqual(got, []string{"pkg.Bar"}) {
		t.Fatalf("callers of pkg.Foo = %v, want [pkg.Bar]", got)
	}

	// Without a conn-type filter, pkg.Baz is reachable via both calls and
	// has_function (from pkg/b.go).
	all, err := ReadReverseConnections(path, []string{"pkg.Baz"}, "")
	if err != nil {
		t.Fatalf("ReadReverseConnections(no type): %v", err)
	}
	sources := append([]string(nil), all["pkg.Baz"]...)
	sort.Strings(sources)
	if !reflect.DeepEqual(sources, []string{"pkg.Foo", "pkg/b.go"}) {
		t.Fatalf("all sources of pkg.Baz = %v, want [pkg.Foo pkg/b.go]", sources)
	}

	// Unknown target yields no entry.
	none, err := ReadReverseConnections(path, []string{"pkg.Nonexistent"}, "calls")
	if err != nil {
		t.Fatalf("ReadReverseConnections(unknown): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no reverse connections, got %v", none)
	}
}

func TestWriteDelta(t *testing.T) {
	path := writePartialTopo(t)

	// Upsert a modified pkg.Foo (new description + dropped call edge, new edge)
	// and delete pkg.Bar.
	modifiedFoo := domain.Resource{
		ID:          "pkg.Foo",
		Kind:        domain.ResourceFunction,
		Name:        "Foo",
		Language:    "go",
		Description: "now does foo differently",
		Location:    domain.Location{StartsAt: 2, EndsAt: 6, Path: "pkg/a.go"},
		Properties:  map[string]any{"input": []any{}, "output": []any{}},
		Connections: map[string][]string{"calls": {"pkg.Bar"}},
	}
	if err := WriteDelta(path, []domain.Resource{modifiedFoo}, []string{"pkg.Bar"}); err != nil {
		t.Fatalf("WriteDelta: %v", err)
	}

	read, err := ReadDb(path)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}

	// Deleted resource is gone.
	if _, ok := read.Resources["pkg.Bar"]; ok {
		t.Fatal("pkg.Bar should have been deleted")
	}

	// Modified resource is present and updated.
	foo, ok := read.Resources["pkg.Foo"]
	if !ok {
		t.Fatal("pkg.Foo should still exist")
	}
	if foo.Description != "now does foo differently" {
		t.Fatalf("pkg.Foo description = %q, want updated", foo.Description)
	}
	if foo.Location.StartsAt != 2 || foo.Location.EndsAt != 6 {
		t.Fatalf("pkg.Foo location not updated: %+v", foo.Location)
	}
	// Stale outgoing edge (calls -> pkg.Baz) replaced by calls -> pkg.Bar.
	if calls := foo.Connections["calls"]; !reflect.DeepEqual(calls, []string{"pkg.Bar"}) {
		t.Fatalf("pkg.Foo calls = %v, want [pkg.Bar] (stale edge should be dropped)", calls)
	}

	// Untouched resources remain intact.
	baz, ok := read.Resources["pkg.Baz"]
	if !ok {
		t.Fatal("pkg.Baz should still exist")
	}
	if baz.Name != "Baz" || baz.Location.Path != "pkg/b.go" {
		t.Fatalf("pkg.Baz altered unexpectedly: %+v", baz)
	}
	// pkg.Bar's outgoing edges should be gone from the connections table.
	if _, ok := read.Resources["pkg.Bar"]; ok {
		t.Fatal("pkg.Bar should not be present")
	}

	// Delete-only and upsert-only no-op edges should both be tolerated.
	if err := WriteDelta(path, nil, nil); err != nil {
		t.Fatalf("WriteDelta(nil,nil): %v", err)
	}
}

// TestWriteDeltaOnFreshDB verifies WriteDelta creates the schema and works on a
// path with no prior WriteDb.
func TestWriteDeltaOnFreshDB(t *testing.T) {
	path := t.TempDir() + "/fresh.db"
	defer os.Remove(path)

	res := domain.Resource{
		ID:          "pkg.New",
		Kind:        domain.ResourceFunction,
		Name:        "New",
		Connections: map[string][]string{"calls": {"pkg.Other"}},
	}
	if err := WriteDelta(path, []domain.Resource{res}, nil); err != nil {
		t.Fatalf("WriteDelta on fresh db: %v", err)
	}

	got, err := ReadResourcesByIDs(path, []string{"pkg.New"})
	if err != nil {
		t.Fatalf("ReadResourcesByIDs: %v", err)
	}
	if _, ok := got["pkg.New"]; !ok {
		t.Fatal("pkg.New should exist after WriteDelta on fresh db")
	}
	if calls := got["pkg.New"].Connections["calls"]; !reflect.DeepEqual(calls, []string{"pkg.Other"}) {
		t.Fatalf("pkg.New calls = %v, want [pkg.Other]", calls)
	}
}
