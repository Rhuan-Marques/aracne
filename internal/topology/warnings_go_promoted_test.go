package topology_test

import (
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// WN-7: a call to a method promoted from an embedded struct recorded no calls edge and no call
// site, so changing the method's signature warned nobody. The embedded struct here lives in a
// package the caller never imports -- reaching it takes the embedded field's type id, on the
// cold scan and on the incremental one alike.
func TestPromotedMethodCallIsWarned(t *testing.T) {
	const (
		caller = "ex/app.Use"
		callee = "ex/base.(Base).Hello"
	)
	base := "package base\n\ntype Base struct{}\n\nfunc (b *Base) Hello(n int) {}\n"
	app := "package app\n\nimport \"ex/mid\"\n\nfunc Use() {\n\to := mid.Outer{}\n\to.Hello(1)\n}\n"
	p := newLiveProj(t, map[string]string{
		"go.mod":       "module ex\n\ngo 1.21\n",
		"base/base.go": base,
		"mid/mid.go":   "package mid\n\nimport \"ex/base\"\n\ntype Outer struct{ base.Base }\n",
		"app/app.go":   app,
	})
	callsCallee := func(when string) {
		t.Helper()
		topo, err := helper.ReadDb(filepath.Join(p.dir, "topology.db"))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range topo.Resources[caller].Connections["calls"] {
			if c == callee {
				return
			}
		}
		t.Fatalf("%s: %s has no calls edge to the promoted %s: %v", when, caller, callee, topo.Resources[caller].Connections)
	}
	callsCallee("cold scan")

	// The caller re-parsed on its own -- the partial path, which loads only the packages it
	// can reach -- must resolve the promoted call exactly as the cold scan did.
	before := topology.PartialIncrementalCount()
	p.edit("app/app.go", "package app\n\nimport \"ex/mid\"\n\n// Use says hello.\nfunc Use() {\n\to := mid.Outer{}\n\to.Hello(1)\n}\n")
	if topology.PartialIncrementalCount() <= before {
		t.Fatal("the caller edit did not take the partial path this test is about")
	}
	callsCallee("incremental re-parse of the caller")

	p.edit("base/base.go", "package base\n\ntype Base struct{}\n\nfunc (b *Base) Hello(n, m int) {}\n")
	if got := p.stored(domain.WarnSignatureChanged, "", caller); len(got) == 0 {
		all, _ := p.mgr.ListWarnings("", "", "")
		t.Fatalf("Hello gained a parameter its promoted caller does not pass, and nothing warned; table: %+v", all)
	}

	p.edit("app/app.go", "package app\n\nimport \"ex/mid\"\n\nfunc Use() {\n\to := mid.Outer{}\n\to.Hello(1, 2)\n}\n")
	if left := p.stored(domain.WarnSignatureChanged, "", caller); len(left) != 0 {
		t.Errorf("fixing the caller must clear the warning, %d left: %+v", len(left), left)
	}
	callsCallee("after fixing the caller")
}
