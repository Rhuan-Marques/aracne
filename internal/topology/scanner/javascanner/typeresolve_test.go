package javascanner

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Type-name resolution (JV-1): a type name binds by Java's scoping rules, never
// to an unrelated internal type that merely shares its simple name.

// srcInternalList is an internal type sharing its simple name with java.util.List.
const srcInternalList = `package com.t.c;
public class List {
    public void add(Object o) {}
}
`

const srcInternalEntry = `package com.t.c;
public class Entry {
    public Object getKey() { return null; }
}
`

// noEdgeTo fails if id has any conn edge to target.
func noEdgeTo(t *testing.T, topo *domain.Topology, id, conn, target string) {
	t.Helper()
	mustRes(t, topo, id)
	if connHas(topo, id, conn, target) {
		t.Errorf("%s must not %s %s", id, conn, target)
	}
}

// wantEdge fails unless id has a conn edge to target.
func wantEdge(t *testing.T, topo *domain.Topology, id, conn, target string) {
	t.Helper()
	mustRes(t, topo, id)
	if !connHas(topo, id, conn, target) {
		t.Errorf("%s should %s %s; has %v", id, conn, target, topo.Resources[id].Connections)
	}
}

func TestExternalSingleTypeImportBindsNothingInternal(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/c/List.java": srcInternalList,
		"src/main/java/com/t/u/User.java": `package com.t.u;
import java.util.ArrayList;
import java.util.List;
public class User {
    public void run() {
        List<String> names = new ArrayList<>();
        names.add("x");
    }
}
`,
	}))
	const run = "com.t.u.User.run()"
	noEdgeTo(t, topo, run, "uses_struct", "com.t.c.List")
	noEdgeTo(t, topo, run, "calls", "com.t.c.List.add(Object)")
	// The external import still records its dependency.
	wantEdge(t, topo, run, "uses_dependency", "java.util")
}

func TestFullyQualifiedExternalBindsNothingInternal(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/c/List.java": srcInternalList,
		"src/main/java/com/t/u/Fq.java": `package com.t.u;
public class Fq {
    private java.util.List<String> field;
    public java.util.List<String> run(java.util.List<String> xs) {
        java.util.List<String> ys = xs;
        ys.add("x");
        return ys;
    }
}
`,
	}))
	const run = "com.t.u.Fq.run(List)"
	noEdgeTo(t, topo, run, "uses_struct", "com.t.c.List")
	noEdgeTo(t, topo, run, "calls", "com.t.c.List.add(Object)")
	noEdgeTo(t, topo, "com.t.u.Fq", "uses_struct", "com.t.c.List")
}

func TestDottedNameResolvesHeadFirst(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/c/Entry.java": srcInternalEntry,
		"src/main/java/com/t/u/Ent.java": `package com.t.u;
import java.util.Map;
public class Ent {
    public void run(Map<String, String> m) {
        for (Map.Entry<String, String> e : m.entrySet()) {
            e.getKey();
        }
        Map.Entry<String, String> f = null;
        f.getKey();
    }
}
`,
	}))
	const run = "com.t.u.Ent.run(Map)"
	noEdgeTo(t, topo, run, "uses_struct", "com.t.c.Entry")
	noEdgeTo(t, topo, run, "calls", "com.t.c.Entry.getKey()")
}

func TestQualifiedMemberTypeFollowsImportedOwner(t *testing.T) {
	realSrc := func(pkg string) string {
		return "package " + pkg + `;
public class Real {
    public void hello() {}
    public static class Deep {
        public void deep() {}
    }
}
`
	}
	topo := jScan(t, writeProj(t, map[string]string{
		// Lexically first: the old by-last-segment fallback picked this one.
		"src/main/java/com/t/aaa/Real.java": realSrc("com.t.aaa"),
		"src/main/java/com/t/pkg/Real.java": realSrc("com.t.pkg"),
		"src/main/java/com/t/other/Mis.java": `package com.t.other;
import com.t.pkg.Real;
public class Mis {
    public void call() {
        Real r = new Real();
        r.hello();
        Real.Deep d = new Real.Deep();
        d.deep();
    }
}
`,
	}))
	const call = "com.t.other.Mis.call()"
	wantEdge(t, topo, call, "uses_struct", "com.t.pkg.Real.Deep")
	wantEdge(t, topo, call, "calls", "com.t.pkg.Real.Deep.deep()")
	wantEdge(t, topo, call, "calls", "com.t.pkg.Real.hello()")
	noEdgeTo(t, topo, call, "uses_struct", "com.t.aaa.Real.Deep")
	noEdgeTo(t, topo, call, "calls", "com.t.aaa.Real.Deep.deep()")
}

func TestExternalOnDemandImportBlocksFallback(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/c/List.java": srcInternalList,
		"src/main/java/com/t/u/Wild.java": `package com.t.u;
import java.util.*;
public class Wild {
    public void run() {
        List<String> l = new ArrayList<>();
        l.add("x");
    }
}
`,
	}))
	const run = "com.t.u.Wild.run()"
	noEdgeTo(t, topo, run, "uses_struct", "com.t.c.List")
	noEdgeTo(t, topo, run, "calls", "com.t.c.List.add(Object)")
}

func TestImplicitJavaLangBindsBeforeFallback(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/m/Exception.java": `package com.t.m;
public class Exception {
    public void report() {}
}
`,
		"src/main/java/com/t/m/Local.java": `package com.t.m;
public class Local {
    public void run() {
        Exception e = new Exception();
        e.report();
    }
}
`,
		"src/main/java/com/t/u/Remote.java": `package com.t.u;
public class Remote {
    public void run() {
        Exception e = new Exception();
        e.getMessage();
    }
}
`,
	}))
	// Another package: the name is java.lang.Exception.
	noEdgeTo(t, topo, "com.t.u.Remote.run()", "uses_struct", "com.t.m.Exception")
	// Same package shadows java.lang.
	wantEdge(t, topo, "com.t.m.Local.run()", "uses_struct", "com.t.m.Exception")
	wantEdge(t, topo, "com.t.m.Local.run()", "calls", "com.t.m.Exception.report()")
}

// TestResolutionOrder locks Java's precedence: single-type import over same
// package, same package over on-demand imports, and two on-demand imports that
// both supply a name bind nothing.
func TestResolutionOrder(t *testing.T) {
	item := func(pkg string) string {
		return "package " + pkg + `;
public class Item {
    public void use() {}
}
`
	}
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/a/Item.java": item("com.t.a"),
		"src/main/java/com/t/b/Item.java": item("com.t.b"),
		"src/main/java/com/t/c/Item.java": item("com.t.c"),
		"src/main/java/com/t/a/Single.java": `package com.t.a;
import com.t.b.Item;
public class Single {
    public void run() { new Item().use(); }
}
`,
		"src/main/java/com/t/a/Pkg.java": `package com.t.a;
import com.t.b.*;
public class Pkg {
    public void run() { new Item().use(); }
}
`,
		"src/main/java/com/t/d/Demand.java": `package com.t.d;
import com.t.b.*;
public class Demand {
    public void run() { new Item().use(); }
}
`,
		"src/main/java/com/t/d/Clash.java": `package com.t.d;
import com.t.b.*;
import com.t.c.*;
public class Clash {
    public void run(Item i) { i.use(); new Item(); }
}
`,
	}))
	wantEdge(t, topo, "com.t.a.Single.run()", "uses_struct", "com.t.b.Item")
	noEdgeTo(t, topo, "com.t.a.Single.run()", "uses_struct", "com.t.a.Item")

	wantEdge(t, topo, "com.t.a.Pkg.run()", "uses_struct", "com.t.a.Item")
	noEdgeTo(t, topo, "com.t.a.Pkg.run()", "uses_struct", "com.t.b.Item")

	wantEdge(t, topo, "com.t.d.Demand.run()", "uses_struct", "com.t.b.Item")

	for _, pkg := range []string{"com.t.a", "com.t.b", "com.t.c"} {
		noEdgeTo(t, topo, "com.t.d.Clash.run(Item)", "uses_struct", pkg+".Item")
	}
}

// TestEnclosingScopeResolution locks the scope-first lookup: a member type
// shadows a same-named single-type import, and same-named nested types in two
// outers each resolve inside their own outer.
func TestEnclosingScopeResolution(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/s/Shadow.java": `package com.t.s;
import java.util.List;
public class Shadow {
    static class List {
        void add(Object o) {}
    }
    void run() {
        List l = new List();
        l.add(1);
    }
}
`,
		"src/main/java/com/t/s/Trees.java": `package com.t.s;
public class Trees {
    static class A {
        static class Node { void a() {} }
        void run() { Node n = new Node(); n.a(); }
    }
    static class B {
        static class Node { void b() {} }
        void run() { Node n = new Node(); n.b(); }
    }
}
`,
	}))
	wantEdge(t, topo, "com.t.s.Shadow.run()", "uses_struct", "com.t.s.Shadow.List")
	wantEdge(t, topo, "com.t.s.Shadow.run()", "calls", "com.t.s.Shadow.List.add(Object)")

	wantEdge(t, topo, "com.t.s.Trees.A.run()", "uses_struct", "com.t.s.Trees.A.Node")
	noEdgeTo(t, topo, "com.t.s.Trees.A.run()", "uses_struct", "com.t.s.Trees.B.Node")
	wantEdge(t, topo, "com.t.s.Trees.B.run()", "uses_struct", "com.t.s.Trees.B.Node")
	wantEdge(t, topo, "com.t.s.Trees.B.run()", "calls", "com.t.s.Trees.B.Node.b()")
	noEdgeTo(t, topo, "com.t.s.Trees.B.run()", "uses_struct", "com.t.s.Trees.A.Node")
}

// TestSimpleNameFallbackOnlyWhenUnique: a name nothing binds (here an inherited
// member type) falls back to the by-name index only with exactly one candidate,
// and the outcome never depends on map order.
func TestSimpleNameFallbackOnlyWhenUnique(t *testing.T) {
	base := `package com.t.w;
public class Base {
    public static class Helper {
        public void help() {}
    }
}
`
	sub := `package com.t.v;
public class Sub extends com.t.w.Base {
    public void run() {
        Helper h = new Helper();
        h.help();
    }
}
`
	unique := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/w/Base.java": base,
		"src/main/java/com/t/v/Sub.java":  sub,
	}))
	wantEdge(t, unique, "com.t.v.Sub.run()", "uses_struct", "com.t.w.Base.Helper")
	wantEdge(t, unique, "com.t.v.Sub.run()", "calls", "com.t.w.Base.Helper.help()")

	files := map[string]string{
		"src/main/java/com/t/w/Base.java": base,
		"src/main/java/com/t/v/Sub.java":  sub,
		"src/main/java/com/t/a/Helper.java": `package com.t.a;
public class Helper {
    public void help() {}
}
`,
	}
	dir := writeProj(t, files)
	first := canonTopo(jScan(t, dir))
	for i := 0; i < 10; i++ {
		topo := jScan(t, dir)
		for _, target := range []string{"com.t.w.Base.Helper", "com.t.a.Helper"} {
			noEdgeTo(t, topo, "com.t.v.Sub.run()", "uses_struct", target)
		}
		if got := canonTopo(topo); got != first {
			t.Fatalf("scan %d differs from scan 0", i+1)
		}
	}
}

// TestTypeResolutionIncrementalMatchesFull re-parses the consumer through
// UpdateFile and requires the same graph a full scan builds.
func TestTypeResolutionIncrementalMatchesFull(t *testing.T) {
	dir := writeProj(t, map[string]string{
		"src/main/java/com/t/c/List.java":  srcInternalList,
		"src/main/java/com/t/c/Entry.java": srcInternalEntry,
		"src/main/java/com/t/u/User.java": `package com.t.u;
import java.util.List;
import java.util.Map;
public class User {
    public void run(Map<String, String> m) {
        List<String> names = null;
        names.add("x");
        Map.Entry<String, String> e = null;
        e.getKey();
    }
}
`,
	})
	full := jScan(t, dir)
	inc := jScan(t, dir)
	if _, err := NewJavaScanner().UpdateFile(inc, dir+"/src/main/java/com/t/u/User.java"); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if a, b := canonTopo(full), canonTopo(inc); a != b {
		t.Errorf("incremental differs from full:\n--- full ---\n%s\n--- incremental ---\n%s", a, b)
	}
	const run = "com.t.u.User.run(Map)"
	noEdgeTo(t, inc, run, "calls", "com.t.c.List.add(Object)")
	noEdgeTo(t, inc, run, "calls", "com.t.c.Entry.getKey()")
}
