package topology

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// TestSharedDependencyNodeMerges pins AR-11.
//
// Two languages naming the same external package -- Go's `path` and Node's `path`, JS's and
// TS's `react` -- collided on one id. mergeTopology recorded an error and DROPPED the second
// side, while removeLanguageResources already treated dependency nodes as language-neutral
// shared nodes. The two disagreed; merging decides it the way the rest of the code had.
func TestSharedDependencyNodeMerges(t *testing.T) {
	dst := &domain.Topology{
		Language: "go",
		Resources: map[string]domain.Resource{
			"path": {ID: "path", Kind: domain.ResourceDependency, Name: "path", Language: "go",
				Connections: map[string][]string{"used_by": {"a.go"}}},
		},
		Warnings: map[string]domain.TopologyWarning{},
		Errors:   map[string]string{},
	}
	src := &domain.Topology{
		Language: "javascript",
		Resources: map[string]domain.Resource{
			"path": {ID: "path", Kind: domain.ResourceDependency, Name: "path", Language: "javascript",
				Connections: map[string][]string{"used_by": {"b.cjs"}}},
		},
	}
	mergeTopology(dst, src)

	if len(dst.Errors) != 0 {
		t.Fatalf("a shared dependency is not a collision to report: %v", dst.Errors)
	}
	got := dst.Resources["path"].Connections["used_by"]
	if len(got) != 2 {
		t.Fatalf("expected both languages' edges to survive the merge, got %v", got)
	}
}

// TestGenuineKindCollisionStillReported: dropping the dependency error must not silence a real
// modelling conflict, where two languages mint the same id for different things.
func TestGenuineKindCollisionStillReported(t *testing.T) {
	dst := &domain.Topology{
		Language: "go",
		Resources: map[string]domain.Resource{
			"thing": {ID: "thing", Kind: domain.ResourceFile, Name: "thing", Language: "go"},
		},
		Warnings: map[string]domain.TopologyWarning{},
		Errors:   map[string]string{},
	}
	src := &domain.Topology{
		Language: "python",
		Resources: map[string]domain.Resource{
			"thing": {ID: "thing", Kind: domain.ResourceStruct, Name: "thing", Language: "python"},
		},
	}
	mergeTopology(dst, src)
	if len(dst.Errors) != 1 {
		t.Fatalf("a kind conflict must still be reported, got %v", dst.Errors)
	}
	for _, msg := range dst.Errors {
		if !strings.Contains(msg, "file") || !strings.Contains(msg, "struct") {
			t.Fatalf("the error should name both kinds, got %q", msg)
		}
	}
}
