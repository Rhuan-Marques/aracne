package viz

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// neighborhoodTestServer serves a small multi-language topology: three Go packages whose
// imports are recorded the way the Go scanner records them (imports_package on the file,
// uses_package on the member), two Python modules joined file->file, and a TypeScript file
// that depends on a dependency node the scanner labels javascript.
func neighborhoodTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	topo := &domain.Topology{
		Root:     dir,
		Language: "multi",
		Resources: map[string]domain.Resource{
			"m/a": {ID: "m/a", Kind: domain.ResourcePackage, Name: "m/a", Language: "go",
				Connections: map[string][]string{"has_file": {"a/a.go"}, "has_function": {"m/a.Run"}}},
			"m/b": {ID: "m/b", Kind: domain.ResourcePackage, Name: "m/b", Language: "go",
				Connections: map[string][]string{"has_file": {"b/b.go"}, "has_function": {"m/b.Help"}}},
			"m/c": {ID: "m/c", Kind: domain.ResourcePackage, Name: "m/c", Language: "go",
				Connections: map[string][]string{"has_file": {"c/c.go"}}},
			"a/a.go": {ID: "a/a.go", Kind: domain.ResourceFile, Name: "a.go", Language: "go",
				Properties:  map[string]any{"from_package": "m/a"},
				Connections: map[string][]string{"imports_package": {"m/b"}}},
			"b/b.go": {ID: "b/b.go", Kind: domain.ResourceFile, Name: "b.go", Language: "go",
				Properties: map[string]any{"from_package": "m/b"}},
			"c/c.go": {ID: "c/c.go", Kind: domain.ResourceFile, Name: "c.go", Language: "go",
				Properties:  map[string]any{"from_package": "m/c"},
				Connections: map[string][]string{"imports_package": {"m/a"}}},
			"m/a.Run": {ID: "m/a.Run", Kind: domain.ResourceFunction, Name: "Run", Language: "go",
				Properties:  map[string]any{"from_package": "m/a"},
				Connections: map[string][]string{"uses_package": {"m/b"}, "calls": {"m/b.Help"}}},
			"m/b.Help": {ID: "m/b.Help", Kind: domain.ResourceFunction, Name: "Help", Language: "go",
				Properties: map[string]any{"from_package": "m/b"}},
			"py/consumer.py": {ID: "py/consumer.py", Kind: domain.ResourceFile, Name: "consumer.py", Language: "python",
				Connections: map[string][]string{"imports_module": {"py/shapes.py"}}},
			"py/shapes.py": {ID: "py/shapes.py", Kind: domain.ResourceFile, Name: "shapes.py", Language: "python"},
			"ts/view.tsx": {ID: "ts/view.tsx", Kind: domain.ResourceFile, Name: "view.tsx", Language: "typescript",
				Connections: map[string][]string{"has_function": {"ts/view.View"}, "imports_dependency": {"javascript:dependency:react"}}},
			"ts/view.View":                {ID: "ts/view.View", Kind: domain.ResourceFunction, Name: "View", Language: "typescript"},
			"javascript:dependency:react": {ID: "javascript:dependency:react", Kind: domain.ResourceDependency, Name: "react", Language: "javascript"},
		},
		Warnings: map[string]domain.TopologyWarning{},
		Errors:   map[string]string{},
	}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	server := httptest.NewServer(NewServer(dbPath))
	t.Cleanup(server.Close)
	return server
}

func neighborhoodIDs(t *testing.T, server *httptest.Server, query url.Values) ([]string, GraphResponse) {
	t.Helper()
	var graph GraphResponse
	getJSON(t, server.URL+"/api/neighborhood?"+query.Encode(), &graph)
	ids := make([]string, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		ids = append(ids, node.ID)
	}
	sort.Strings(ids)
	return ids, graph
}

func sameIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestPackagesNeighborhoodUsesRolledUpGoImports is VZ-4. The Packages & Modules view joins Go
// packages by rolling file- and member-level imports_package/uses_package up to the owning
// package; the double-click neighbourhood walked the raw edges instead, so a package that
// imports others had no neighbours at all and an imported one showed the importing FILES --
// nodes this view never draws.
func TestPackagesNeighborhoodUsesRolledUpGoImports(t *testing.T) {
	server := neighborhoodTestServer(t)

	for _, tc := range []struct {
		name  string
		query url.Values
		want  []string
	}{
		{"importer", url.Values{"id": {"m/a"}, "depth": {"1"}, "mode": {"packages"}}, []string{"m/a", "m/b", "m/c"}},
		{"imported", url.Values{"id": {"m/b"}, "depth": {"1"}, "mode": {"packages"}}, []string{"m/a", "m/b"}},
		{"depth two", url.Values{"id": {"m/b"}, "depth": {"2"}, "mode": {"packages"}}, []string{"m/a", "m/b", "m/c"}},
		{"outgoing only", url.Values{"id": {"m/a"}, "depth": {"1"}, "mode": {"packages"}, "direction": {"out"}}, []string{"m/a", "m/b"}},
		{"incoming only", url.Values{"id": {"m/a"}, "depth": {"1"}, "mode": {"packages"}, "direction": {"in"}}, []string{"m/a", "m/c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids, graph := neighborhoodIDs(t, server, tc.query)
			if !sameIDs(ids, tc.want) {
				t.Fatalf("nodes = %v, want %v", ids, tc.want)
			}
			for _, edge := range graph.Edges {
				if edge.Source == "a/a.go" || edge.Target == "a/a.go" || edge.Source == "c/c.go" {
					t.Fatalf("edge %+v starts at a Go file, which the Packages view never shows", edge)
				}
			}
		})
	}

	// The edges are the view's own: a -> b, not a.go -> b.
	_, graph := neighborhoodIDs(t, server, url.Values{"id": {"m/a"}, "depth": {"1"}, "mode": {"packages"}})
	var ab, ca bool
	for _, edge := range graph.Edges {
		ab = ab || (edge.Source == "m/a" && edge.Target == "m/b" && edge.Type == "imports_package")
		ca = ca || (edge.Source == "m/c" && edge.Target == "m/a" && edge.Type == "imports_package")
	}
	if !ab || !ca {
		t.Fatalf("want package->package edges m/a->m/b and m/c->m/a, got %+v", graph.Edges)
	}
}

// TestPackagesNeighborhoodModulesUnchanged pins the other half of the hybrid view: Python/JS
// modules were already file->file and must stay exactly that.
func TestPackagesNeighborhoodModulesUnchanged(t *testing.T) {
	server := neighborhoodTestServer(t)
	ids, graph := neighborhoodIDs(t, server, url.Values{"id": {"py/shapes.py"}, "depth": {"1"}, "mode": {"packages"}})
	if want := []string{"py/consumer.py", "py/shapes.py"}; !sameIDs(ids, want) {
		t.Fatalf("nodes = %v, want %v", ids, want)
	}
	if len(graph.Edges) != 1 || graph.Edges[0].Type != "imports_module" || graph.Edges[0].Source != "py/consumer.py" {
		t.Fatalf("want the one imports_module edge, got %+v", graph.Edges)
	}
}

// TestNeighborhoodAppliesLanguageFilter is VZ-8: the SPA sends its language filter with every
// neighbourhood request, and the main graph honours it; the neighbourhood did not, so a
// TypeScript drill-down pulled in a javascript dependency node.
func TestNeighborhoodAppliesLanguageFilter(t *testing.T) {
	server := neighborhoodTestServer(t)

	ids, _ := neighborhoodIDs(t, server, url.Values{"id": {"ts/view.tsx"}, "depth": {"1"}, "language": {"typescript"}})
	if want := []string{"ts/view.View", "ts/view.tsx"}; !sameIDs(ids, want) {
		t.Fatalf("language=typescript: nodes = %v, want %v", ids, want)
	}
	// Unfiltered ("all", which the SPA sends by default) keeps every neighbour.
	ids, _ = neighborhoodIDs(t, server, url.Values{"id": {"ts/view.tsx"}, "depth": {"1"}, "language": {"all"}})
	if want := []string{"javascript:dependency:react", "ts/view.View", "ts/view.tsx"}; !sameIDs(ids, want) {
		t.Fatalf("language=all: nodes = %v, want %v", ids, want)
	}
	// The Packages view filters too: a Go-only drill-down from a Go package shows only Go.
	ids, _ = neighborhoodIDs(t, server, url.Values{"id": {"m/a"}, "depth": {"4"}, "mode": {"packages"}, "language": {"go"}})
	if want := []string{"m/a", "m/b", "m/c"}; !sameIDs(ids, want) {
		t.Fatalf("packages language=go: nodes = %v, want %v", ids, want)
	}
}

// TestNodeIDWithPercentSign is VZ-7: r.URL.Path is already decoded, and a second
// url.PathUnescape turned "a%41b" into "aAb" (not found) and a bare "%" into a 400.
func TestNodeIDWithPercentSign(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	ids := []string{"web/a%41b.pctFn", "web/100%.fn", "pkg/a.Plain"}
	resources := map[string]domain.Resource{}
	for _, id := range ids {
		resources[id] = domain.Resource{ID: id, Kind: domain.ResourceFunction, Name: id}
	}
	if err := helper.WriteDb(&domain.Topology{Root: dir, Language: "javascript", Resources: resources}, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	server := httptest.NewServer(NewServer(dbPath))
	defer server.Close()

	for _, id := range ids {
		// Exactly what app.js sends: '/api/node/' + encodeURIComponent(id).
		resp, err := http.Get(server.URL + "/api/node/" + url.PathEscape(id))
		if err != nil {
			t.Fatalf("GET %s: %v", id, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /api/node/ for %q: status %d, want 200", id, resp.StatusCode)
		}
	}
}
