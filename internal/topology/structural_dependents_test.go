package topology

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// structuralDependentFiles decides which surviving files are re-parsed after a removal, so it is
// pinned in both directions: every structural tie is found, and a plain caller -- which the
// removal already warns about and strips -- is not re-parsed for it.
func TestStructuralDependentFilesIsBounded(t *testing.T) {
	// An absolute root valid on every platform. structuralDependentFiles reports each file
	// through filepath.Abs, and "/p/x.rs" is rooted but carries no VOLUME: on Windows Abs
	// qualifies it with the current drive, so the answers came back as `D:\p\x.rs` and matched
	// none of the literals this fixture was written with.
	root, err := filepath.Abs(filepath.FromSlash("/p"))
	if err != nil {
		t.Fatal(err)
	}
	f := func(name string) string { return filepath.Join(root, name) }
	res := func(id, file string, props map[string]any, conns map[string][]string) domain.Resource {
		if props == nil {
			props = map[string]any{}
		}
		return domain.Resource{ID: id, Kind: domain.ResourceFunction, Name: id,
			Location: domain.Location{Path: file}, Properties: props, Connections: conns}
	}
	graph := map[string]domain.Resource{
		f("shapes.rs"): {ID: f("shapes.rs"), Kind: domain.ResourceFile},
		f("impls.rs"): {ID: f("impls.rs"), Kind: domain.ResourceFile, Connections: map[string][]string{
			"__impl_records": {"q::Circle=>>q::Shape"},
		}},
		f("Kid.java"): {ID: f("Kid.java"), Kind: domain.ResourceFile, Connections: map[string][]string{
			"__extends_records": {"com.Kid=>>com.Base:class"},
		}},
		"q::Circle": res("q::Circle", f("shapes.rs"), nil, map[string][]string{"methods": {"q::Circle::area"}}),
		"q::Shape":  res("q::Shape", f("shapes.rs"), nil, nil),
		"q::Circle::area": res("q::Circle::area", f("impls.rs"),
			map[string]any{"method_from": "q::Circle"}, nil),
		"com.Base":   res("com.Base", f("Base.java"), nil, nil),
		"com.Kid":    res("com.Kid", f("Kid.java"), nil, nil),
		"app.caller": res("app.caller", f("app.rs"), nil, map[string][]string{"calls": {"q::Circle::area"}}),
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
	if got := files([]string{f("impls.rs"), "q::Circle::area"}, true); !same(got, f("shapes.rs")) {
		t.Errorf("deleting the impl file: got %v, want [%s]", got, f("shapes.rs"))
	}
	// The type is removed: the impl file attaches a method to it and records an impl of it.
	if got := files([]string{"q::Circle"}, false); !same(got, f("impls.rs")) {
		t.Errorf("removing the type: got %v, want [%s]", got, f("impls.rs"))
	}
	// A removed parent class: the child's record names it.
	if got := files([]string{"com.Base"}, false); !same(got, f("Kid.java")) {
		t.Errorf("removing the parent class: got %v, want [%s]", got, f("Kid.java"))
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
