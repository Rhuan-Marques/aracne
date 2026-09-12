package topology

import (
	"sort"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// structuralDependentFiles decides which surviving files are re-parsed after a removal, so it is
// pinned in both directions: every structural tie is found, and a plain caller -- which the
// removal already warns about and strips -- is not re-parsed for it.
func TestStructuralDependentFilesIsBounded(t *testing.T) {
	res := func(id, file string, props map[string]any, conns map[string][]string) domain.Resource {
		if props == nil {
			props = map[string]any{}
		}
		return domain.Resource{ID: id, Kind: domain.ResourceFunction, Name: id,
			Location: domain.Location{Path: file}, Properties: props, Connections: conns}
	}
	graph := map[string]domain.Resource{
		"/p/shapes.rs": {ID: "/p/shapes.rs", Kind: domain.ResourceFile},
		"/p/impls.rs": {ID: "/p/impls.rs", Kind: domain.ResourceFile, Connections: map[string][]string{
			"__impl_records": {"q::Circle=>>q::Shape"},
		}},
		"/p/Kid.java": {ID: "/p/Kid.java", Kind: domain.ResourceFile, Connections: map[string][]string{
			"__extends_records": {"com.Kid=>>com.Base:class"},
		}},
		"q::Circle": res("q::Circle", "/p/shapes.rs", nil, map[string][]string{"methods": {"q::Circle::area"}}),
		"q::Shape":  res("q::Shape", "/p/shapes.rs", nil, nil),
		"q::Circle::area": res("q::Circle::area", "/p/impls.rs",
			map[string]any{"method_from": "q::Circle"}, nil),
		"com.Base":   res("com.Base", "/p/Base.java", nil, nil),
		"com.Kid":    res("com.Kid", "/p/Kid.java", nil, nil),
		"app.caller": res("app.caller", "/p/app.rs", nil, map[string][]string{"calls": {"q::Circle::area"}}),
	}
	files := func(removed []string, wholeFile bool) []string {
		set := map[string]bool{}
		for _, id := range removed {
			set[id] = true
		}
		var out []string
		for f := range structuralDependentFiles(graph, set, wholeFile) {
			out = append(out, f)
		}
		sort.Strings(out)
		return out
	}
	same := func(got []string, want ...string) bool {
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

	// The impl file is deleted: the record tied two survivors, and Circle's method set shrank.
	if got := files([]string{"/p/impls.rs", "q::Circle::area"}, true); !same(got, "/p/shapes.rs") {
		t.Errorf("deleting the impl file: got %v, want [/p/shapes.rs]", got)
	}
	// The type is removed: the impl file attaches a method to it and records an impl of it.
	if got := files([]string{"q::Circle"}, false); !same(got, "/p/impls.rs") {
		t.Errorf("removing the type: got %v, want [/p/impls.rs]", got)
	}
	// A removed parent class: the child's record names it.
	if got := files([]string{"com.Base"}, false); !same(got, "/p/Kid.java") {
		t.Errorf("removing the parent class: got %v, want [/p/Kid.java]", got)
	}
	// A changed file is re-parsed anyway, so a method set it shrank is not a reason to re-parse
	// the type's file, and a caller is never a structural tie.
	if got := files([]string{"q::Circle::area"}, false); len(got) != 0 {
		t.Errorf("removing a method from a changed file: got %v, want none", got)
	}
	if got := files([]string{"app.caller"}, true); len(got) != 0 {
		t.Errorf("removing a plain function: got %v, want none", got)
	}
}
