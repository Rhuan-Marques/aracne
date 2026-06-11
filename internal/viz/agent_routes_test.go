package viz

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

func TestAgentRouteGraphShowsKnownNodesAndEdges(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "topology.db")
	topo := &domain.Topology{
		Root:     ".",
		Language: "go",
		Resources: map[string]domain.Resource{
			"f1": {
				ID:   "f1",
				Kind: domain.ResourceFunction,
				Name: "One",
				Connections: map[string][]string{
					"calls": {"f2"},
				},
			},
			"f2": {ID: "f2", Kind: domain.ResourceFunction, Name: "Two", Connections: map[string][]string{}},
			"f3": {ID: "f3", Kind: domain.ResourceFunction, Name: "Three", Connections: map[string][]string{}},
		},
		Warnings: map[string]domain.TopologyWarning{},
		Errors:   map[string]string{},
	}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("write db: %v", err)
	}
	if err := helper.RecordAgentRouteAccess(dbPath, "s1", "claude", "session", helper.AgentRouteAccessFullCut, []string{"f1"}, 10*time.Minute); err != nil {
		t.Fatalf("record full cut: %v", err)
	}
	if err := helper.RecordAgentRouteAccess(dbPath, "s1", "claude", "session", helper.AgentRouteAccessDescription, []string{"f2"}, 10*time.Minute); err != nil {
		t.Fatalf("record description: %v", err)
	}

	server := httptest.NewServer(NewServer(dbPath))
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/graph?agent_route=s1")
	if err != nil {
		t.Fatalf("get graph: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var graph GraphResponse
	if err := json.NewDecoder(resp.Body).Decode(&graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want f1 and f2", graph.Nodes)
	}
	access := map[string]string{}
	for _, node := range graph.Nodes {
		access[node.ID] = node.AgentRouteAccess
		if node.ID == "f3" {
			t.Fatalf("route graph included unknown node f3")
		}
	}
	if access["f1"] != helper.AgentRouteAccessFullCut || access["f2"] != helper.AgentRouteAccessDescription {
		t.Fatalf("unexpected access map: %+v", access)
	}
	if len(graph.Edges) != 1 || graph.Edges[0].Source != "f1" || graph.Edges[0].Target != "f2" {
		t.Fatalf("unexpected edges: %+v", graph.Edges)
	}
}
