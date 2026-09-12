package viz

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// VIZ-01. A caller stores what it passes to each callee under the private "__call_sites"
// connection (contract/record.go: "It cannot reach the model"), and Rust adds
// "__use_bindings". Their targets are ENCODED RECORDS -- `pkg.Second=>>{"n":0}` -- not
// resources. The viz index counted every connection kind, so those records became graph edges:
// inflated degrees (which drive node size and the degree-based optimization rules), an
// "__call_sites" entry in the Custom-mode edge-type picker, and Inspector rows that answer 400
// when clicked.
func privateEdgeTopology(t *testing.T, root string) *domain.Topology {
	t.Helper()
	return &domain.Topology{
		Root:     root,
		Language: "go",
		Resources: map[string]domain.Resource{
			"pkg/a.First": {
				ID:       "pkg/a.First",
				Kind:     domain.ResourceFunction,
				Name:     "First",
				Location: domain.Location{Path: "a.go", StartsAt: 1, EndsAt: 4},
				Connections: map[string][]string{
					"calls":                {"pkg/b.Second"},
					contract.CallSitesConn: {`pkg/b.Second=>>{"n":0}`},
					"__use_bindings":       {`pkg/b.Second=>>{"x":1}`},
				},
			},
			"pkg/b.Second": {
				ID:       "pkg/b.Second",
				Kind:     domain.ResourceFunction,
				Name:     "Second",
				Location: domain.Location{Path: "b.go", StartsAt: 5, EndsAt: 8},
			},
		},
		Warnings: map[string]domain.TopologyWarning{},
		Errors:   map[string]string{},
	}
}

func TestVIZ01_PrivateRecordsAreNotGraphEdges(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	if err := helper.WriteDb(privateEdgeTopology(t, dir), dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	server := httptest.NewServer(NewServer(dbPath))
	defer server.Close()

	var summary Summary
	getJSON(t, server.URL+"/api/summary", &summary)
	for kind := range summary.EdgeTypes {
		if strings.HasPrefix(kind, "__") {
			t.Errorf("summary offers the private record %q as an edge type", kind)
		}
	}
	if summary.EdgeCount != 1 {
		t.Errorf("summary edge_count = %d, want 1 (only the calls edge is a real edge)", summary.EdgeCount)
	}

	var node struct {
		Node struct {
			OutDegree int `json:"out_degree"`
			InDegree  int `json:"in_degree"`
		} `json:"node"`
		Outgoing []GraphEdge `json:"outgoing"`
		Incoming []GraphEdge `json:"incoming"`
	}
	getJSON(t, server.URL+"/api/node/pkg%2Fa.First", &node)
	if node.Node.OutDegree != 1 {
		t.Errorf("out_degree = %d, want 1: the private records are not edges", node.Node.OutDegree)
	}
	for _, e := range node.Outgoing {
		if strings.HasPrefix(e.Type, "__") {
			t.Errorf("the Inspector is given a private record as a relationship: %+v", e)
		}
	}

	getJSON(t, server.URL+"/api/node/pkg%2Fb.Second", &node)
	if node.Node.InDegree != 1 {
		t.Errorf("in_degree = %d, want 1: the private records are not edges", node.Node.InDegree)
	}
	for _, e := range node.Incoming {
		if strings.HasPrefix(e.Type, "__") {
			t.Errorf("the Inspector is given a private record as a relationship: %+v", e)
		}
	}
}
