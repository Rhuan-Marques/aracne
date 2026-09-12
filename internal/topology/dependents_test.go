package topology

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// dependentFiles runs before every tool call, and every file it returns is re-parsed, so how
// little it returns matters as much as that it returns enough. These pin the bound: an edit
// that adds nothing re-resolves nothing; an added member reaches only the files that already
// depend on its module AND name it; a changed export surface reaches every dependent and
// nothing else; only a name no file could have resolved yet is looked for everywhere.

// dependentsFixture: a.js is the edited module. b.js and c.js depend on it (b.js mentions
// every name the cases add, c.js none); d.js has no edge to it but mentions them all too;
// e.go is another language that mentions them.
func dependentsFixture(t *testing.T) (dir string, before map[string]domain.Resource) {
	t.Helper()
	dir = t.TempDir()
	contents := map[string]string{
		"a.js": "export class C { one() {} }\n",
		"b.js": "import { C } from './a.js';\nexport function caller() { new C().two(); fresh(); }\n",
		"c.js": "import { C } from './a.js';\nexport function use() { new C().one(); }\n",
		"d.js": "export function solo() { two(); fresh(); }\n",
		"e.go": "package e\n\nfunc two() { fresh() }\n",
	}
	for name, content := range contents {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := func(name string) string { return filepath.Join(dir, name) }
	file := func(name, lang string) domain.Resource {
		return domain.Resource{ID: path(name), Kind: domain.ResourceFile, Name: name, Language: lang,
			Properties: map[string]any{}, Connections: map[string][]string{}}
	}
	decl := func(id, name, in string, props map[string]any, conns map[string][]string) domain.Resource {
		if props == nil {
			props = map[string]any{}
		}
		return domain.Resource{ID: id, Kind: domain.ResourceFunction, Name: name, Language: "javascript",
			Location: domain.Location{Path: path(in)}, Properties: props, Connections: conns}
	}
	before = map[string]domain.Resource{
		path("a.js"): file("a.js", "javascript"),
		path("b.js"): file("b.js", "javascript"),
		path("c.js"): file("c.js", "javascript"),
		path("d.js"): file("d.js", "javascript"),
		path("e.go"): file("e.go", "go"),
		"a.C": {ID: "a.C", Kind: domain.ResourceStruct, Name: "C", Language: "javascript",
			Location: domain.Location{Path: path("a.js")}},
		"a.C.one":  decl("a.C.one", "one", "a.js", map[string]any{"method_from": "a.C"}, nil),
		"b.caller": decl("b.caller", "caller", "b.js", nil, map[string][]string{"uses_class": {"a.C"}}),
		"c.use":    decl("c.use", "use", "c.js", nil, map[string][]string{"calls": {"a.C.one"}}),
		"d.solo":   decl("d.solo", "solo", "d.js", nil, nil),
	}
	for _, f := range []string{"b.js", "c.js"} {
		res := before[path(f)]
		res.Connections["imports_module"] = []string{path("a.js")}
		before[path(f)] = res
	}
	return dir, before
}

func dependentsOf(t *testing.T, dir string, before, after map[string]domain.Resource) []string {
	t.Helper()
	keys := make(map[string]string, len(before))
	for id, res := range before {
		keys[id] = resourceSignatureKey(res)
	}
	a := filepath.Join(dir, "a.js")
	got := dependentFiles(&domain.Topology{Resources: after}, before, keys,
		map[string]bool{a: true}, map[string]bool{a: true})
	var names []string
	for f := range got {
		names = append(names, filepath.Base(f))
	}
	sort.Strings(names)
	return names
}

func cloneResources(in map[string]domain.Resource) map[string]domain.Resource {
	out := make(map[string]domain.Resource, len(in))
	for id, res := range in {
		out[id] = cloneResource(res)
	}
	return out
}

func TestDependentFilesIsBounded(t *testing.T) {
	t.Run("an edit that adds nothing re-resolves nothing", func(t *testing.T) {
		dir, before := dependentsFixture(t)
		if got := dependentsOf(t, dir, before, cloneResources(before)); len(got) != 0 {
			t.Errorf("got %v, want none", got)
		}
	})

	t.Run("an added method reaches the dependents that name it", func(t *testing.T) {
		dir, before := dependentsFixture(t)
		after := cloneResources(before)
		after["a.C.two"] = domain.Resource{ID: "a.C.two", Kind: domain.ResourceFunction, Name: "two",
			Language: "javascript", Location: domain.Location{Path: filepath.Join(dir, "a.js")},
			Properties: map[string]any{"method_from": "a.C"}}
		got := dependentsOf(t, dir, before, after)
		// Not c.js (a dependent that never says `two`), not d.js (says it, depends on
		// nothing here), not e.go (another language).
		if len(got) != 1 || got[0] != "b.js" {
			t.Errorf("got %v, want [b.js]", got)
		}
	})

	t.Run("a changed export surface reaches every dependent", func(t *testing.T) {
		dir, before := dependentsFixture(t)
		after := cloneResources(before)
		a := filepath.Join(dir, "a.js")
		file := after[a]
		file.Properties = map[string]any{"default_export": "C"}
		after[a] = file
		got := dependentsOf(t, dir, before, after)
		if len(got) != 2 || got[0] != "b.js" || got[1] != "c.js" {
			t.Errorf("got %v, want [b.js c.js]", got)
		}
	})

	t.Run("a name nothing declared before is looked for in every file of the language", func(t *testing.T) {
		dir, before := dependentsFixture(t)
		after := cloneResources(before)
		after["a.fresh"] = domain.Resource{ID: "a.fresh", Kind: domain.ResourceFunction, Name: "fresh",
			Language: "javascript", Location: domain.Location{Path: filepath.Join(dir, "a.js")},
			Properties: map[string]any{}}
		got := dependentsOf(t, dir, before, after)
		// d.js has no edge into a.js but names it -- a dangling reference is exactly that.
		if len(got) != 2 || got[0] != "b.js" || got[1] != "d.js" {
			t.Errorf("got %v, want [b.js d.js]", got)
		}
	})
}

func TestContainsWordRespectsIdentifierBoundaries(t *testing.T) {
	for _, c := range []struct {
		text, word string
		want       bool
	}{
		{"later();", "later", true},
		{"import { later } from", "later", true},
		{"laterness()", "later", false},
		{"$later()", "later", false},
		{"my_later()", "later", false},
		{"x.later", "later", true},
		{"", "later", false},
	} {
		if got := containsWord([]byte(c.text), c.word); got != c.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v", c.text, c.word, got, c.want)
		}
	}
}
