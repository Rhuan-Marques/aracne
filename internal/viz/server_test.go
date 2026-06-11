package viz

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

func TestServerGraphModes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	root := filepath.Join(dir, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nfunc First() {\n\tSecond()\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile a.go: %v", err)
	}
	topo := &domain.Topology{
		Root:     root,
		Language: "go",
		Resources: map[string]domain.Resource{
			"pkg/a": {
				ID:   "pkg/a",
				Kind: domain.ResourcePackage,
				Name: "pkg/a",
				Connections: map[string][]string{
					"has_file":     {"a.go"},
					"has_function": {"pkg/a.First"},
				},
			},
			"pkg/b": {
				ID:   "pkg/b",
				Kind: domain.ResourcePackage,
				Name: "pkg/b",
				Connections: map[string][]string{
					"has_file":     {"b.go"},
					"has_function": {"pkg/b.Second"},
				},
			},
			"a.go": {
				ID:         "a.go",
				Kind:       domain.ResourceFile,
				Name:       "a.go",
				Properties: map[string]any{"from_package": "pkg/a"},
				Connections: map[string][]string{
					"imports_package": {"pkg/b"},
				},
			},
			"b.go": {
				ID:         "b.go",
				Kind:       domain.ResourceFile,
				Name:       "b.go",
				Properties: map[string]any{"from_package": "pkg/b"},
			},
			"pkg/a.First": {
				ID:       "pkg/a.First",
				Kind:     domain.ResourceFunction,
				Name:     "First",
				Location: domain.Location{Path: "a.go", StartsAt: 1, EndsAt: 4},
				Properties: map[string]any{
					"from_package": "pkg/a",
				},
				Connections: map[string][]string{
					"calls": {"pkg/b.Second"},
				},
			},
			"pkg/b.Second": {
				ID:          "pkg/b.Second",
				Kind:        domain.ResourceFunction,
				Name:        "Second",
				Description: "target function",
				Location:    domain.Location{Path: "b.go", StartsAt: 5, EndsAt: 8},
				Properties: map[string]any{
					"from_package": "pkg/b",
				},
			},
		},
		Warnings: map[string]domain.TopologyWarning{
			"w1": {ID: "w1", SourceID: "pkg/a.First", Kind: domain.WarnUseMissingNode, TargetID: "pkg/b.Second", Message: "test warning"},
		},
		Errors: map[string]string{},
	}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	if err := helper.CreateBug(dbPath, domain.KnownBug{ID: "b1", NodeID: "pkg/b.Second", Description: "test bug", State: domain.BugPending}); err != nil {
		t.Fatalf("CreateBug: %v", err)
	}

	server := httptest.NewServer(NewServer(dbPath))
	defer server.Close()

	var summary Summary
	getJSON(t, server.URL+"/api/summary", &summary)
	if summary.NodeCount != 6 || summary.EdgeCount != 6 || summary.WarningCount != 1 || summary.BugCount != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	var packages GraphResponse
	getJSON(t, server.URL+"/api/graph?mode=packages", &packages)
	if len(packages.Nodes) != 2 || len(packages.Edges) != 1 {
		t.Fatalf("unexpected package graph: %+v", packages)
	}
	if packages.Edges[0].Source != "pkg/a" || packages.Edges[0].Target != "pkg/b" || packages.Edges[0].Type != "imports_package" {
		t.Fatalf("unexpected package edge: %+v", packages.Edges[0])
	}

	var dataFlow GraphResponse
	getJSON(t, server.URL+"/api/graph?mode=data_flow", &dataFlow)
	if len(dataFlow.Nodes) != 2 || len(dataFlow.Edges) != 1 {
		t.Fatalf("unexpected data flow graph: %+v", dataFlow)
	}
	if dataFlow.Edges[0].Type != "calls" {
		t.Fatalf("unexpected data flow edge: %+v", dataFlow.Edges[0])
	}

	var custom GraphResponse
	getJSON(t, server.URL+"/api/graph?kind=function,method&edge_kind=calls", &custom)
	if len(custom.Nodes) != 2 || len(custom.Edges) != 1 {
		t.Fatalf("unexpected custom graph: %+v", custom)
	}

	var neighborhood GraphResponse
	getJSON(t, server.URL+"/api/neighborhood?id=pkg/a.First&depth=1&edge_kind=calls", &neighborhood)
	if len(neighborhood.Nodes) != 2 || len(neighborhood.Edges) != 1 {
		t.Fatalf("unexpected neighborhood: %+v", neighborhood)
	}

	var dataFlowNeighborhood GraphResponse
	getJSON(t, server.URL+"/api/neighborhood?id=pkg/a.First&depth=1&mode=data_flow", &dataFlowNeighborhood)
	if len(dataFlowNeighborhood.Nodes) != 2 || len(dataFlowNeighborhood.Edges) != 1 {
		t.Fatalf("unexpected data flow neighborhood: %+v", dataFlowNeighborhood)
	}
	for _, node := range dataFlowNeighborhood.Nodes {
		if node.Kind == "package" || node.Kind == "dependency" {
			t.Fatalf("data flow neighborhood included non-data-flow node: %+v", node)
		}
	}

	var inspected struct {
		Node GraphNode `json:"node"`
		Code string    `json:"code"`
	}
	getJSON(t, server.URL+"/api/node/pkg%2Fa.First", &inspected)
	wantCode := "package a\nfunc First() {\n\tSecond()\n}"
	if inspected.Code != wantCode {
		t.Fatalf("unexpected inspected code:\n%s", inspected.Code)
	}
}

func TestOptimizationRulesCollapseAndPersistDefaults(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	root := filepath.Join(dir, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("MkdirAll db dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nfunc root() {}\nfunc mid() {}\nfunc leaf() {}\nfunc Target() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile a.go: %v", err)
	}
	topo := &domain.Topology{
		Root:     root,
		Language: "go",
		Resources: map[string]domain.Resource{
			"root": {
				ID:       "root",
				Kind:     domain.ResourceFunction,
				Name:     "root",
				Location: domain.Location{Path: "a.go", StartsAt: 2, EndsAt: 2},
				Connections: map[string][]string{
					"calls": {"mid"},
				},
			},
			"mid": {
				ID:          "mid",
				Kind:        domain.ResourceFunction,
				Name:        "mid",
				Description: "collapsed middle",
				Location:    domain.Location{Path: "a.go", StartsAt: 3, EndsAt: 3},
				Connections: map[string][]string{
					"calls": {"leaf"},
				},
			},
			"leaf": {
				ID:          "leaf",
				Kind:        domain.ResourceFunction,
				Name:        "leaf",
				Description: "collapsed leaf",
				Location:    domain.Location{Path: "a.go", StartsAt: 4, EndsAt: 4},
				Connections: map[string][]string{
					"calls": {"target"},
				},
			},
			"target": {
				ID:       "target",
				Kind:     domain.ResourceFunction,
				Name:     "Target",
				Location: domain.Location{Path: "a.go", StartsAt: 5, EndsAt: 5},
			},
		},
		Warnings: map[string]domain.TopologyWarning{},
		Errors:   map[string]string{},
	}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	server := httptest.NewServer(NewServer(dbPath))
	defer server.Close()

	var saved []OptimizationRule
	getJSON(t, server.URL+"/api/optimization-rules", &saved)
	if len(saved) != 2 || !saved[0].Favorite || !saved[1].Favorite {
		t.Fatalf("unexpected default rules: %+v", saved)
	}
	if _, err := os.Stat(filepath.Join(dir, ".aracne", "optimization_rules.json")); err != nil {
		t.Fatalf("optimization rules were not persisted: %v", err)
	}

	rules := url.QueryEscape(`[{"id":"test","active":true,"operations":[{"kind":"non_exported"},{"kind":"less_equal_lines","value":"1000"}]}]`)
	var graph GraphResponse
	getJSON(t, server.URL+"/api/graph?kind=function&edge_kind=calls&optimization_rules="+rules, &graph)
	if len(graph.Nodes) != 2 || len(graph.Edges) != 1 {
		t.Fatalf("unexpected optimized graph: %+v", graph)
	}
	if graph.Edges[0].Source != "root" || graph.Edges[0].Target != "target" {
		t.Fatalf("expected inherited edge root -> target, got %+v", graph.Edges[0])
	}
	var rootNode GraphNode
	for _, node := range graph.Nodes {
		if node.ID == "root" {
			rootNode = node
		}
		if node.ID == "mid" || node.ID == "leaf" {
			t.Fatalf("chain node should have been collapsed: %+v", graph.Nodes)
		}
	}
	if len(rootNode.Includes) != 2 || rootNode.Includes[0].ID != "leaf" || rootNode.Includes[1].ID != "mid" {
		t.Fatalf("expected root includes recursive collapsed chain, got %+v", rootNode.Includes)
	}

	var inspected struct {
		Node GraphNode `json:"node"`
		Code string    `json:"code"`
	}
	getJSON(t, server.URL+"/api/node/root?optimization_rules="+rules, &inspected)
	if len(inspected.Node.Includes) != 2 {
		t.Fatalf("expected inspected root includes recursive chain, got %+v", inspected.Node.Includes)
	}
	if !strings.Contains(inspected.Code, "func mid()") || !strings.Contains(inspected.Code, "func leaf()") {
		t.Fatalf("expected merged code for recursive includes, got:\n%s", inspected.Code)
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}
