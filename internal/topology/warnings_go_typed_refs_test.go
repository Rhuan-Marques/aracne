package topology_test

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// edgesOf returns one resource's edges of one kind, sorted, for a failure message that names
// what the graph actually holds.
func edgesOf(t *testing.T, dir, id, kind string) []string {
	t.Helper()
	topo, err := helper.ReadDb(filepath.Join(dir, "topology.db"))
	if err != nil {
		t.Fatal(err)
	}
	out := append([]string(nil), topo.Resources[id].Connections[kind]...)
	sort.Strings(out)
	return out
}

func hasEdge(t *testing.T, dir, id, kind, target string) bool {
	t.Helper()
	for _, e := range edgesOf(t, dir, id, kind) {
		if e == target {
			return true
		}
	}
	return false
}

// GO-05: `type Outer struct{ base.Base }` satisfies an interface through the methods promoted
// from Base, exactly as Go's method set does. Interface matching read the struct's OWN methods
// only, so the idiomatic mixin was missing from every implementer list -- and from the
// "Implemented By" an interface read prints. Call resolution already followed promotion
// (structMethodID), so the two disagreed about the same method.
func TestEmbeddedPromotionSatisfiesInterface(t *testing.T) {
	const (
		iface = "ex/mid.Greeter"
		outer = "ex/mid.Outer"
	)
	p := newLiveProj(t, map[string]string{
		"go.mod":       "module ex\n\ngo 1.21\n",
		"base/base.go": "package base\n\ntype Base struct{}\n\nfunc (b Base) Hello() string { return \"hi\" }\n",
		// The interface lives in its own file: a file that declares one always routes to the
		// full path, and the edit below is about the partial one.
		"mid/greeter.go": "package mid\n\ntype Greeter interface {\n\tHello() string\n}\n",
		"mid/outer.go":   "package mid\n\nimport \"ex/base\"\n\ntype Outer struct{ base.Base }\n",
	})
	implemented := func(when string) {
		t.Helper()
		if !hasEdge(t, p.dir, iface, "implemented_by", outer) {
			t.Fatalf("%s: %s is not implemented_by the embedding %s: %v",
				when, iface, outer, edgesOf(t, p.dir, iface, "implemented_by"))
		}
		if !hasEdge(t, p.dir, outer, "implements", iface) {
			t.Fatalf("%s: %s does not implement %s: %v",
				when, outer, iface, edgesOf(t, p.dir, outer, "implements"))
		}
	}
	implemented("cold scan")

	// The file declaring both re-parsed on its own -- the partial path -- must reach the same
	// answer, which means reaching an embedded type in a package it merely imports.
	before := topology.PartialIncrementalCount()
	p.edit("mid/outer.go", "package mid\n\nimport \"ex/base\"\n\n// Outer greets through Base.\ntype Outer struct{ base.Base }\n")
	if topology.PartialIncrementalCount() <= before {
		t.Fatal("the edit did not take the partial path this test is about")
	}
	implemented("incremental re-parse")
}

// GO-06: a non-struct named type has a method set and takes part in interface matching, but the
// body analyzer tracked struct- and interface-typed variables only, so a call through one
// recorded no calls edge -- and a signature change to the method warned nobody.
func TestNamedTypeMethodCallIsResolvedAndWarned(t *testing.T) {
	const (
		param  = "ex/b.Param"
		result = "ex/b.FromResult"
		method = "ex/a.(Celsius).String"
	)
	p := newLiveProj(t, map[string]string{
		"go.mod": "module ex\n\ngo 1.21\n",
		"a/a.go": "package a\n\ntype Celsius float64\n\nfunc (c Celsius) String() string { return \"c\" }\n\n" +
			"func Temp() Celsius { return Celsius(1) }\n",
		"b/b.go": "package b\n\nimport \"ex/a\"\n\nfunc Param(c a.Celsius) string { return c.String() }\n\n" +
			"func FromResult() string {\n\tt := a.Temp()\n\treturn t.String()\n}\n",
	})
	for _, caller := range []string{param, result} {
		if !hasEdge(t, p.dir, caller, "calls", method) {
			t.Fatalf("cold scan: %s does not call the named type's method %s: %v",
				caller, method, edgesOf(t, p.dir, caller, "calls"))
		}
	}

	p.edit("a/a.go", "package a\n\ntype Celsius float64\n\nfunc (c Celsius) String(prec int) string { return \"c\" }\n\n"+
		"func Temp() Celsius { return Celsius(1) }\n")
	for _, caller := range []string{param, result} {
		if got := p.stored(domain.WarnSignatureChanged, method, caller); len(got) == 0 {
			all, _ := p.mgr.ListWarnings("", "", "")
			t.Fatalf("String gained a parameter %s does not pass, and nothing warned; table: %+v", caller, all)
		}
	}
}

// Each caller FORWARDS a parameter of the type that changed, which is the one argument form
// whose type is known exactly (argToken), so the call-site contract rule can see the mismatch
// too rather than discharging a warning it has no argument type to judge.
//
// GO-07: a parameter's type was rendered lossily -- every func type collapsed to "func(...)",
// a channel lost its direction, a struct/interface literal lost its fields -- so the signature
// diff could not see a change inside one. All three edits below break every caller and used to
// report nothing at all.
func TestParameterTypeShapeChangeIsWarned(t *testing.T) {
	cases := []struct {
		name   string
		before string
		after  string
		call   string
	}{
		{
			name:   "func type",
			before: "func Walk(fn func(string) error) error { return fn(\"x\") }",
			after:  "func Walk(fn func(string, int) error) error { return fn(\"x\", 1) }",
			call:   "func Use(fn func(string) error) error { return lib.Walk(fn) }",
		},
		{
			name:   "channel direction",
			before: "func Feed(ch chan<- int) { ch <- 1 }",
			after:  "func Feed(ch <-chan int) { <-ch }",
			call:   "func Use(ch chan<- int) { lib.Feed(ch) }",
		},
		{
			name:   "struct literal type",
			before: "func Opts(o struct{ A int }) int { return o.A }",
			after:  "func Opts(o struct{ B string }) int { return len(o.B) }",
			call:   "func Use(o struct{ A int }) int { return lib.Opts(o) }",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newLiveProj(t, map[string]string{
				"go.mod":     "module ex\n\ngo 1.21\n",
				"lib/lib.go": "package lib\n\n" + c.before + "\n",
				"app/app.go": "package app\n\nimport \"ex/lib\"\n\n" + c.call + "\n",
			})
			p.edit("lib/lib.go", "package lib\n\n"+c.after+"\n")
			if got := p.stored(domain.WarnSignatureChanged, "", "ex/app.Use"); len(got) == 0 {
				all, _ := p.mgr.ListWarnings("", "", "")
				t.Fatalf("the %s changed shape and nothing warned its caller; table: %+v", c.name, all)
			}
		})
	}
}

// GO-07 control: re-rendering a type in more detail must not make an UNCHANGED signature
// compare unequal. A scan over a database written by an older build carries the old lossy
// strings, and warning about every function that takes a func or a struct literal would bury
// the real warnings.
func TestUnchangedShapedParameterDoesNotWarn(t *testing.T) {
	p := newLiveProj(t, map[string]string{
		"go.mod": "module ex\n\ngo 1.21\n",
		"lib/lib.go": "package lib\n\nfunc Walk(fn func(string) error) error { return fn(\"x\") }\n\n" +
			"func Feed(ch chan<- int) { ch <- 1 }\n",
		"app/app.go": "package app\n\nimport \"ex/lib\"\n\n" +
			"func Use() error { lib.Feed(make(chan int, 1)); return lib.Walk(func(s string) error { return nil }) }\n",
	})
	p.edit("lib/lib.go", "package lib\n\n// Walk walks.\nfunc Walk(fn func(string) error) error { return fn(\"x\") }\n\n"+
		"func Feed(ch chan<- int) { ch <- 1 }\n")
	if got := p.stored(domain.WarnSignatureChanged, "", "ex/app.Use"); len(got) != 0 {
		t.Fatalf("a comment above an unchanged signature warned its caller: %+v", got)
	}
}

// GO-08: deleting a package-level var left its cross-file users pointing at a node that is
// gone, with no warning -- the removal bookkeeping covered functions, structs and named types
// but not vars. The full path raised node_removed for the same edit, so the two update paths
// disagreed about a plain deletion.
func TestRemovedPackageVarWarnsItsUsers(t *testing.T) {
	// Both update paths, which is the point: the partial one used to leave the edge standing
	// and say nothing, while the two-phase one raised node_removed for the same edit.
	for _, mode := range []struct{ name, noPartial string }{
		{"partial fast path", ""},
		{"full two-phase path", "1"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv("ARAC_NO_PARTIAL", mode.noPartial)
			const (
				user    = "ex/a.Use"
				removed = "ex/a.Count"
			)
			p := newLiveProj(t, map[string]string{
				"go.mod":    "module ex\n\ngo 1.21\n",
				"a/vars.go": "package a\n\nvar Count = 0\n\nvar Other = 1\n",
				"a/use.go":  "package a\n\nfunc Use() int { return Count }\n",
			})
			if !hasEdge(t, p.dir, user, "uses_extvar", removed) {
				t.Fatalf("cold scan: %s does not use %s: %v", user, removed, edgesOf(t, p.dir, user, "uses_extvar"))
			}

			p.edit("a/vars.go", "package a\n\nvar Other = 1\n")
			if got := p.stored(domain.WarnNodeRemoved, "", removed); len(got) == 0 {
				all, _ := p.mgr.ListWarnings("", "", "")
				t.Fatalf("%s was deleted and its user was not warned; table: %+v", removed, all)
			}
			if hasEdge(t, p.dir, user, "uses_extvar", removed) {
				t.Errorf("%s still points at the deleted %s", user, removed)
			}
		})
	}
}

// GO-09: a reference to another package's var or const outside call position recorded nothing
// -- the selector walk descended into X only, and the ident branch looked in the caller's own
// package. The same reference written inside the declaring package recorded uses_extvar, so
// deleting the declaration warned the local users and silently broke the cross-package ones.
func TestCrossPackageVarReferenceRecordsEdge(t *testing.T) {
	const (
		user  = "ex/b.Use"
		count = "ex/a.Count"
		max   = "ex/a.MaxRetries"
	)
	p := newLiveProj(t, map[string]string{
		"go.mod":   "module ex\n\ngo 1.21\n",
		"a/a.go":   "package a\n\nvar Count = 0\n\nconst MaxRetries = 3\n",
		"b/use.go": "package b\n\nimport \"ex/a\"\n\nfunc Use() int {\n\ta.Count++\n\treturn a.MaxRetries\n}\n",
	})
	for _, target := range []string{count, max} {
		if !hasEdge(t, p.dir, user, "uses_extvar", target) {
			t.Fatalf("cold scan: %s does not use %s: %v", user, target, edgesOf(t, p.dir, user, "uses_extvar"))
		}
	}

	p.edit("a/a.go", "package a\n\nvar Count = 0\n")
	if got := p.stored(domain.WarnNodeRemoved, "", max); len(got) == 0 {
		all, _ := p.mgr.ListWarnings("", "", "")
		t.Fatalf("%s was deleted and its cross-package user was not warned; table: %+v", max, all)
	}
}
