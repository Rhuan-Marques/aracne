package viz

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func TestResourceGraphFiltersByLanguage(t *testing.T) {
	idx := &graphIndex{
		topo: &domain.Topology{
			Root:      ".",
			Language:  "multi",
			Languages: []string{"go", "python"},
			Resources: map[string]domain.Resource{
				"go.fn": {ID: "go.fn", Kind: domain.ResourceFunction, Name: "GoFn", Language: "go"},
				"py.fn": {ID: "py.fn", Kind: domain.ResourceFunction, Name: "PyFn", Language: "python"},
			},
		},
		incoming:      make(map[string][]GraphEdge),
		outgoing:      make(map[string][]GraphEdge),
		inDegree:      make(map[string]int),
		outDegree:     make(map[string]int),
		warningCounts: make(map[string]int),
		bugCounts:     make(map[string]int),
	}

	all := idx.resourceGraph("", nil, "", "", nil, 10, false)
	if len(all.Nodes) != 2 {
		t.Fatalf("expected all languages to return 2 nodes, got %d", len(all.Nodes))
	}
	goOnly := idx.resourceGraph("", nil, "", "go", nil, 10, false)
	if len(goOnly.Nodes) != 1 || goOnly.Nodes[0].Language != "go" {
		t.Fatalf("expected go-only graph, got %#v", goOnly.Nodes)
	}
	pythonOnly := idx.resourceGraph("", nil, "", "python", nil, 10, false)
	if len(pythonOnly.Nodes) != 1 || pythonOnly.Nodes[0].Language != "python" {
		t.Fatalf("expected python-only graph, got %#v", pythonOnly.Nodes)
	}
}
