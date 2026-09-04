package viz

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
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
	// Bug counts and /api/bugs are gated on features.bug_management; this test asserts the
	// enabled behaviour, so the project config has to turn it on.
	writeVizConfig(t, dbPath, true)

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

// TestServerPackagesAndModulesHybrid verifies the renamed "Packages & Modules"
// view is hybrid: Python/JS files appear as module nodes joined by file->file
// imports_module edges, while a Go package still appears as a package node.
func TestServerPackagesAndModulesHybrid(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	topo := &domain.Topology{
		Root:     dir,
		Language: "multi",
		Resources: map[string]domain.Resource{
			"proj/consumer.py": {
				ID:       "proj/consumer.py",
				Kind:     domain.ResourceFile,
				Name:     "consumer.py",
				Language: "python",
				Connections: map[string][]string{
					"imports_module": {"proj/shapes.py"},
				},
			},
			"proj/shapes.py": {
				ID:       "proj/shapes.py",
				Kind:     domain.ResourceFile,
				Name:     "shapes.py",
				Language: "python",
			},
			"pkg/a": {
				ID:       "pkg/a",
				Kind:     domain.ResourcePackage,
				Name:     "pkg/a",
				Language: "go",
				Connections: map[string][]string{
					"has_file": {"a.go"},
				},
			},
			"pkg/b": {
				ID:       "pkg/b",
				Kind:     domain.ResourcePackage,
				Name:     "pkg/b",
				Language: "go",
			},
			"a.go": {
				ID:          "a.go",
				Kind:        domain.ResourceFile,
				Name:        "a.go",
				Language:    "go",
				Properties:  map[string]any{"from_package": "pkg/a"},
				Connections: map[string][]string{"imports_package": {"pkg/b"}},
			},
		},
		Errors: map[string]string{},
	}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	server := httptest.NewServer(NewServer(dbPath))
	defer server.Close()

	var graph GraphResponse
	getJSON(t, server.URL+"/api/graph?mode=packages", &graph)

	// Nodes: 2 python module (file) nodes + 2 go package nodes. Go files are NOT
	// nodes in this view.
	if len(graph.Nodes) != 4 {
		t.Fatalf("expected 4 nodes (2 py modules + 2 go packages), got %d: %+v", len(graph.Nodes), graph.Nodes)
	}
	for _, n := range graph.Nodes {
		if n.ID == "a.go" {
			t.Fatalf("go file should not be a node in the packages view: %+v", n)
		}
	}

	var moduleEdge, pkgEdge bool
	for _, e := range graph.Edges {
		if e.Type == "imports_module" && e.Source == "proj/consumer.py" && e.Target == "proj/shapes.py" {
			moduleEdge = true
		}
		if e.Type == "imports_package" && e.Source == "pkg/a" && e.Target == "pkg/b" {
			pkgEdge = true
		}
	}
	if !moduleEdge {
		t.Errorf("expected file->file imports_module edge consumer.py -> shapes.py, got edges %+v", graph.Edges)
	}
	if !pkgEdge {
		t.Errorf("expected Go package->package imports_package edge pkg/a -> pkg/b, got edges %+v", graph.Edges)
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

func TestConfigNeedDescriptionAPI(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("MkdirAll db dir: %v", err)
	}
	if err := helper.WriteDb(&domain.Topology{}, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	server := httptest.NewServer(NewServer(dbPath))
	defer server.Close()

	var cfg helper.Config
	getJSON(t, server.URL+"/api/config", &cfg)
	if len(cfg.Descriptions.Kinds) != 4 || cfg.Descriptions.Kinds[0] != domain.ResourceFunction || cfg.Descriptions.Kinds[1] != domain.ResourceMethod {
		t.Fatalf("unexpected default descriptions.kinds: %+v", cfg.Descriptions.Kinds)
	}
	if got := cfg.AgentParam("claude_code", "descriptions-generation-executor", "max-batch-size", 0); got != helper.DefaultDescriptionBatchSize {
		t.Fatalf("unexpected default executor batch size: %d", got)
	}

	body := bytes.NewBufferString(`{"descriptions":{"kinds":["function","struct","function"]},"description_batch_size":3}`)
	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/config", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/config: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/config status = %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode PUT /api/config: %v", err)
	}
	if len(cfg.Descriptions.Kinds) != 2 || cfg.Descriptions.Kinds[0] != domain.ResourceFunction || cfg.Descriptions.Kinds[1] != domain.ResourceStruct {
		t.Fatalf("unexpected saved descriptions.kinds: %+v", cfg.Descriptions.Kinds)
	}
	if got := cfg.AgentParam("claude_code", "descriptions-generation-executor", "max-batch-size", 0); got != 3 {
		t.Fatalf("unexpected saved executor batch size: %d", got)
	}

	saved := helper.LoadConfig(helper.ConfigPath(dbPath))
	if len(saved.Descriptions.Kinds) != 2 || saved.Descriptions.Kinds[0] != domain.ResourceFunction || saved.Descriptions.Kinds[1] != domain.ResourceStruct {
		t.Fatalf("unexpected persisted descriptions.kinds: %+v", saved.Descriptions.Kinds)
	}
	if got := saved.AgentParam("claude_code", "descriptions-generation-executor", "max-batch-size", 0); got != 3 {
		t.Fatalf("unexpected persisted executor batch size: %d", got)
	}
}

// writeVizConfig writes a project config next to dbPath with features.bug_management set.
func writeVizConfig(t *testing.T, dbPath string, bugManagement bool) {
	t.Helper()
	cfg := helper.DefaultConfig()
	cfg.Features.BugManagement = bugManagement
	if err := helper.SaveConfig(cfg, helper.ConfigPath(dbPath)); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
}

// TestBugSurfaceIsGated pins the disabled half: the same database with the feature off
// reports no bugs anywhere and does not serve /api/bugs at all.
func TestBugSurfaceIsGated(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	topo := &domain.Topology{
		Root:     dir,
		Language: "go",
		Resources: map[string]domain.Resource{
			"pkg/a.Fn": {ID: "pkg/a.Fn", Kind: domain.ResourceFunction, Name: "Fn"},
		},
		Warnings: map[string]domain.TopologyWarning{},
	}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	if err := helper.CreateBug(dbPath, domain.KnownBug{ID: "b1", NodeID: "pkg/a.Fn", Description: "x", State: domain.BugPending}); err != nil {
		t.Fatalf("CreateBug: %v", err)
	}
	writeVizConfig(t, dbPath, false)

	server := httptest.NewServer(NewServer(dbPath))
	defer server.Close()

	var summary Summary
	getJSON(t, server.URL+"/api/summary", &summary)
	if summary.BugCount != 0 {
		t.Fatalf("bug management off: BugCount = %d, want 0", summary.BugCount)
	}

	resp, err := http.Get(server.URL + "/api/bugs")
	if err != nil {
		t.Fatalf("GET /api/bugs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("bug management off: /api/bugs must not be registered")
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

// writeChatConfig writes a config with only features.chat set, so a Chat assertion cannot
// accidentally pass because some unrelated default also happened to be on.
func writeChatConfig(t *testing.T, dbPath string, chat bool) {
	t.Helper()
	cfg := helper.DefaultConfig()
	cfg.Features.Chat = chat
	if err := helper.SaveConfig(cfg, helper.ConfigPath(dbPath)); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
}

func chatTestServer(t *testing.T, chat bool) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	topo := &domain.Topology{
		Root:     dir,
		Language: "go",
		Resources: map[string]domain.Resource{
			"pkg/a.Fn": {ID: "pkg/a.Fn", Kind: domain.ResourceFunction, Name: "Fn"},
		},
		Warnings: map[string]domain.TopologyWarning{},
	}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	writeChatConfig(t, dbPath, chat)
	server := httptest.NewServer(NewServer(dbPath))
	t.Cleanup(server.Close)
	return server
}

// TestChatSurfaceIsGated pins the disabled half. Chat is not part of 1.0, and unlike the
// bug pipeline it had no gate at all -- the routes were registered unconditionally, so the
// only way not to ship it was not to ship viz. /api/context-graph is included because it
// belongs to Chat: it derives its graph from a chat session's tool calls.
func TestChatSurfaceIsGated(t *testing.T) {
	server := chatTestServer(t, false)

	for _, path := range []string{"/api/chat", "/api/chat/", "/api/context-graph"} {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("chat off: %s must not be registered, got %d", path, resp.StatusCode)
		}
	}
}

// TestChatSurfaceEnabled is the other half: with the flag on the routes come back, so the
// gate is proven to be the flag and not something incidentally broken.
func TestChatSurfaceEnabled(t *testing.T) {
	server := chatTestServer(t, true)

	resp, err := http.Get(server.URL + "/api/context-graph")
	if err != nil {
		t.Fatalf("GET /api/context-graph: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("chat on: /api/context-graph must be registered, got 404")
	}
}

// TestConfigExposesFeatures pins the contract the SPA relies on to hide the Chat tab:
// /api/config must carry the features block. Without it applyFeatureGates() reads
// undefined and hides Chat even where it is turned on.
func TestConfigExposesFeatures(t *testing.T) {
	server := chatTestServer(t, true)

	var cfg struct {
		Features struct {
			Chat bool `json:"chat"`
		} `json:"features"`
	}
	getJSON(t, server.URL+"/api/config", &cfg)
	if !cfg.Features.Chat {
		t.Fatal("/api/config must expose features.chat so the SPA can gate the nav item")
	}
}
