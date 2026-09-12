package javascanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	java "github.com/Rhuan-Marques/aracne/internal/topology/java"
)

// JV-2: a C-style array parameter (`String s[]`) is an array, and its ID says so.
func TestCStyleArrayParamsKeepDimensions(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/a/Arr.java": `package com.t.a;
public class Arr {
    public void f(String s) {}
    public void f(String s[]) {}
    public static void main(String args[]) {}
    public void g(int m[][], String t) {}
    public void h(String[] s[]) {}
    public void k(String[] s) {}
}
`,
	}))
	for _, id := range []string{
		"com.t.a.Arr.f(String)",
		"com.t.a.Arr.f(String[])",
		"com.t.a.Arr.main(String[])",
		"com.t.a.Arr.g(int[][],String)",
		"com.t.a.Arr.h(String[][])",
		"com.t.a.Arr.k(String[])",
	} {
		mustRes(t, topo, id)
	}
	for _, id := range []string{"com.t.a.Arr.main(String)", "com.t.a.Arr.g(int,String)"} {
		if hasID(topo, id) {
			t.Errorf("%s: the declarator's dimensions were dropped", id)
		}
	}
	// The overloads keep their own bodies: f(String) is line 3, f(String[]) line 4.
	if got := mustRes(t, topo, "com.t.a.Arr.f(String)").Location.StartsAt; got != 3 {
		t.Errorf("f(String) starts at %d, want 3", got)
	}
	if got := mustRes(t, topo, "com.t.a.Arr.f(String[])").Location.StartsAt; got != 4 {
		t.Errorf("f(String[]) starts at %d, want 4", got)
	}
	// The parameter's recorded type carries the dimensions too.
	if in, ok := mustRes(t, topo, "com.t.a.Arr.main(String[])").Properties["input"].([]java.VariableDefinition); !ok || len(in) != 1 || in[0].Typing != "String[]" {
		t.Errorf("main's input = %#v, want one String[] parameter", mustRes(t, topo, "com.t.a.Arr.main(String[])").Properties["input"])
	}
}

// JV-3: a call through a static import binds to the imported method, and the
// enclosing class's own member still shadows it.
func TestStaticImportCalls(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/a/Util.java": `package com.t.a;
public class Util {
    public static int helper(int x) { return x; }
    public static int other() { return 1; }
    public static int shadowed() { return 1; }
}
`,
		"src/main/java/com/t/a/More.java": `package com.t.a;
public class More {
    public static int extra() { return 1; }
    public static int blocked() { return 1; }
}
`,
		"src/main/java/com/t/b/User.java": `package com.t.b;
import static com.t.a.Util.helper;
import static com.t.a.Util.shadowed;
import static com.t.a.More.*;
import static org.ext.Lib.blocked;
public class User {
    public int shadowed() { return 0; }
    public int run() {
        return helper(3) + extra() + shadowed() + blocked();
    }
}
`,
	}))
	const run = "com.t.b.User.run()"
	wantEdge(t, topo, run, "calls", "com.t.a.Util.helper(int)")
	wantEdge(t, topo, run, "calls", "com.t.a.More.extra()")
	// A method of the enclosing class shadows a static import of the same name.
	wantEdge(t, topo, run, "calls", "com.t.b.User.shadowed()")
	noEdgeTo(t, topo, run, "calls", "com.t.a.Util.shadowed()")
	// A single-static import shadows the on-demand one, even when it is external.
	noEdgeTo(t, topo, run, "calls", "com.t.a.More.blocked()")
	// Only imported names bind: other() is not imported.
	noEdgeTo(t, topo, run, "calls", "com.t.a.Util.other()")
}

// JV-4: a receiver typed by a method, constructor, catch or lambda parameter
// resolves the call.
func TestParameterTypedReceivers(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/a/Circle.java": `package com.t.a;
public class Circle {
    public double area() { return 1; }
}
`,
		"src/main/java/com/t/a/Oops.java": `package com.t.a;
public class Oops extends RuntimeException {
    public int code() { return 1; }
}
`,
		"src/main/java/com/t/b/User.java": `package com.t.b;
import com.t.a.Circle;
import com.t.a.Oops;
import java.util.function.Function;
public class User {
    private Circle c;
    public User(Circle k) { k.area(); }
    public double useParam(Circle c) { return c.area(); }
    public int shadow(String c) { return c.length(); }
    public void onError() {
        try { } catch (Oops e) { e.code(); }
    }
    public Function<Circle, Double> lam() {
        return (Circle x) -> x.area();
    }
}
`,
	}))
	wantEdge(t, topo, "com.t.b.User.useParam(Circle)", "calls", "com.t.a.Circle.area()")
	wantEdge(t, topo, "com.t.b.User.<init>(Circle)", "calls", "com.t.a.Circle.area()")
	wantEdge(t, topo, "com.t.b.User.onError()", "calls", "com.t.a.Oops.code()")
	wantEdge(t, topo, "com.t.b.User.lam()", "calls", "com.t.a.Circle.area()")
	// A parameter shadows the Circle field of the same name: String.length is external.
	if calls := topo.Resources["com.t.b.User.shadow(String)"].Connections["calls"]; len(calls) != 0 {
		t.Errorf("shadow(String) must not resolve through the field it shadows; calls %v", calls)
	}
	// A parameter types its receiver; it records no uses edge of its own.
	noEdgeTo(t, topo, "com.t.b.User.onError()", "uses_struct", "com.t.a.Oops")
}

// JV-5: `import static T.*` imports T's members, not a package named T, and the
// pom's own coordinates are not a dependency.
func TestStaticWildcardIsAMemberImport(t *testing.T) {
	dir := writeProj(t, map[string]string{
		"src/main/java/com/t/a/Util.java": `package com.t.a;
public class Util {
    public static int helper() { return 1; }
}
`,
		"src/main/java/com/t/b/User.java": `package com.t.b;
import static com.t.a.Util.*;
import org.example.gen.Generated;
public class User {
    public int run() { return helper(); }
}
`,
	})
	topo := jScan(t, dir)
	file := idEndingWith(t, topo, "com/t/b/User.java")
	if !connHasSuffix(topo, file, "imports_module", "com/t/a/Util.java") {
		t.Errorf("a static wildcard of an internal type must import its module; has %v", topo.Resources[file].Connections)
	}
	for _, dep := range topo.Resources[file].Connections["imports_dependency"] {
		// pomXML declares the project as org.example:unit.
		if dep == "org.example:unit" || dep == "com.t.a.Util" || dep == "com.t.a" {
			t.Errorf("spurious dependency %q", dep)
		}
	}
	if hasID(topo, "org.example:unit") {
		t.Error("the project's own coordinates became a dependency node")
	}
	wantEdge(t, topo, "com.t.b.User.run()", "calls", "com.t.a.Util.helper()")
}

// TestPomDependencyCoordinates pins which pom pairs are dependencies.
func TestPomDependencyCoordinates(t *testing.T) {
	ctx := buildJavaModel(t.TempDir())
	parsePomDeps(`<project>
  <parent>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-parent</artifactId>
  </parent>
  <groupId>com.ex</groupId>
  <artifactId>demo</artifactId>
  <dependencies>
    <dependency>
      <groupId>com.google.guava</groupId>
      <artifactId>guava</artifactId>
      <exclusions>
        <exclusion>
          <groupId>com.google.code.findbugs</groupId>
          <artifactId>jsr305</artifactId>
        </exclusion>
      </exclusions>
    </dependency>
    <dependency>
      <artifactId>jackson-databind</artifactId>
      <groupId>com.fasterxml.jackson.core</groupId>
    </dependency>
  </dependencies>
  <build><plugins><plugin>
    <groupId>org.apache.maven.plugins</groupId>
    <artifactId>maven-compiler-plugin</artifactId>
  </plugin></plugins></build>
</project>
`, ctx)
	want := map[string]string{
		"com.google.guava":           "com.google.guava:guava",
		"com.fasterxml.jackson.core": "com.fasterxml.jackson.core:jackson-databind",
	}
	if len(ctx.depCoord) != len(want) {
		t.Errorf("depCoord = %v, want %v", ctx.depCoord, want)
	}
	for g, c := range want {
		if ctx.depCoord[g] != c {
			t.Errorf("depCoord[%q] = %q, want %q", g, ctx.depCoord[g], c)
		}
	}
	if got := ctx.depFor("com.google.common.base.Strings"); got != "com.google.common" {
		t.Errorf("depFor fell back wrongly: %q", got)
	}
	if got := ctx.depFor("com.google.guava.X"); got != "com.google.guava:guava" {
		t.Errorf("depFor(guava) = %q", got)
	}
}

// JV-7: an unqualified or this-qualified call binds to the nearest type in the
// hierarchy declaring the method, so an override shadows what it overrides.
func TestInheritedMethodCalls(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/h/Base.java": `package com.t.h;
public class Base implements Greeter {
    public void helper() {}
    public void over() {}
    public void arity(int x) {}
    public void sup() {}
    protected void viaThis() {}
    protected void viaThisRef() {}
    protected void viaSuper() {}
    protected void viaSuperRef() {}
}
`,
		"src/main/java/com/t/h/Greeter.java": `package com.t.h;
public interface Greeter {
    default void greet() {}
}
`,
		"src/main/java/com/t/h/Mid.java": `package com.t.h;
public class Mid extends Base {
    @Override public void over() {}
    @Override public void sup() {}
    public void arity() {}
}
`,
		"src/main/java/com/t/h/Leaf.java": `package com.t.h;
public class Leaf extends Mid {
    @Override public void sup() {}
    public void go() {
        helper();
        over();
        arity(1);
        greet();
        this.viaThis();
        super.viaSuper();
        super.sup();
        Runnable r = this::viaThisRef;
        Runnable q = super::viaSuperRef;
    }
    class Inner {
        void in() { helper(); }
    }
}
`,
	}))
	const goID = "com.t.h.Leaf.go()"
	wantEdge(t, topo, goID, "calls", "com.t.h.Base.helper()")   // declared two classes up
	wantEdge(t, topo, goID, "calls", "com.t.h.Mid.over()")      // the nearest override...
	noEdgeTo(t, topo, goID, "calls", "com.t.h.Base.over()")     // ...shadows what it overrides
	wantEdge(t, topo, goID, "calls", "com.t.h.Base.arity(int)") // the arity reaches past Mid.arity()
	noEdgeTo(t, topo, goID, "calls", "com.t.h.Mid.arity()")
	wantEdge(t, topo, goID, "calls", "com.t.h.Greeter.greet()") // an interface default
	wantEdge(t, topo, goID, "calls", "com.t.h.Base.viaThis()")
	wantEdge(t, topo, goID, "calls", "com.t.h.Base.viaThisRef()") // this::m
	wantEdge(t, topo, goID, "calls", "com.t.h.Base.viaSuper()")
	wantEdge(t, topo, goID, "calls", "com.t.h.Base.viaSuperRef()") // super::m
	// super.m() starts above the class: Mid's override, not Leaf's own nor Base's.
	wantEdge(t, topo, goID, "calls", "com.t.h.Mid.sup()")
	noEdgeTo(t, topo, goID, "calls", "com.t.h.Leaf.sup()")
	noEdgeTo(t, topo, goID, "calls", "com.t.h.Base.sup()")
	// An inner class reaches the enclosing class's inherited members.
	wantEdge(t, topo, "com.t.h.Leaf.Inner.in()", "calls", "com.t.h.Base.helper()")
}

// JV-7: double-brace initialization is an instance initializer of the anonymous
// class, and an unqualified call in it reaches the enclosing class's members.
func TestAnonymousClassInitBlock(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/h/Base.java": `package com.t.h;
public class Base {
    public void helper() {}
}
`,
		"src/main/java/com/t/h/Kid.java": `package com.t.h;
import java.util.ArrayList;
public class Kid extends Base {
    public void go() {
        ArrayList<String> l = new ArrayList<>() {{ add("a"); helper(); }};
    }
    static class Nest {
        void mine() {}
        void make() {
            Runnable r = new Runnable() {
                public void run() { mine(); }
            };
        }
    }
    void mine() {}
}
`,
	}))
	const init = "com.t.h.Kid$anon1.<instance-init>()"
	mustRes(t, topo, init)
	wantEdge(t, topo, init, "calls", "com.t.h.Base.helper()")
	if hasConnKind(topo, init, "calls", "add") {
		t.Errorf("add() is inherited from the external ArrayList and must not resolve")
	}
	// The anonymous Runnable sits in Nest: mine() is Nest's, not Kid's.
	wantEdge(t, topo, "com.t.h.Kid$anon2.run()", "calls", "com.t.h.Kid.Nest.mine()")
	noEdgeTo(t, topo, "com.t.h.Kid$anon2.run()", "calls", "com.t.h.Kid.mine()")
}

// hasConnKind reports whether id has a conn edge to any method named name.
func hasConnKind(topo *domain.Topology, id, conn, name string) bool {
	for _, tgt := range topo.Resources[id].Connections[conn] {
		if r, ok := topo.Resources[tgt]; ok && r.Name == name {
			return true
		}
	}
	return false
}

// RD-3: a synthesized record accessor is located at the record header, not the
// whole record.
func TestRecordAccessorLocatedAtHeader(t *testing.T) {
	topo := jScan(t, writeProj(t, map[string]string{
		"src/main/java/com/t/r/P.java": `package com.t.r;

public record P(int x,
                int y) {

    public int sum() {
        return x + y;
    }
}
`,
	}))
	for _, id := range []string{"com.t.r.P.x()", "com.t.r.P.y()"} {
		loc := mustRes(t, topo, id).Location
		if loc.StartsAt != 3 || loc.EndsAt != 4 {
			t.Errorf("%s at %d-%d, want the header 3-4", id, loc.StartsAt, loc.EndsAt)
		}
	}
	if loc := mustRes(t, topo, "com.t.r.P").Location; loc.StartsAt != 3 || loc.EndsAt != 9 {
		t.Errorf("record at %d-%d, want 3-9", loc.StartsAt, loc.EndsAt)
	}
}

// JV-6: one FQN declared by two source sets. The graph keeps the copy of the last
// file in path order, and re-parsing either file must leave it there.
func TestSameFQNInTwoSourceSets(t *testing.T) {
	main := "src/main/java/com/t/a/A.java"
	j11 := "src/main/java11/com/t/a/A.java"
	dir := writeProj(t, map[string]string{
		main:                           "package com.t.a;\npublic class A {\n    public int m() { return 1; }\n    public int onlyMain() { return 1; }\n}\n",
		j11:                            "package com.t.a;\n\npublic class A {\n    public int m() { return 11; }\n    public int only11() { return 11; }\n}\n",
		"src/main/java/com/t/b/U.java": "package com.t.b;\nimport com.t.a.A;\npublic class U {\n    public int use(A a) { return a.m(); }\n}\n",
	})
	full := jScan(t, dir)
	if got := mustRes(t, full, "com.t.a.A").Location.Path; got != filepath.Join(dir, j11) {
		t.Fatalf("A located in %s, want the java11 copy", got)
	}
	for _, edited := range []string{main, j11} {
		inc := jScan(t, dir)
		if _, err := NewJavaScanner().UpdateFile(inc, filepath.Join(dir, edited)); err != nil {
			t.Fatal(err)
		}
		if a, b := canonTopo(full), canonTopo(inc); a != b {
			t.Errorf("re-parsing %s: incremental differs from full:\n--- full ---\n%s\n--- incremental ---\n%s", edited, a, b)
		}
		if got := mustRes(t, inc, "com.t.a.A").Location.Path; got != filepath.Join(dir, j11) {
			t.Errorf("re-parsing %s moved A to %s", edited, got)
		}
	}

	// Once the java11 copy is gone, re-parsing the survivor takes A over before the
	// removal, as a full scan of the remaining tree has it.
	inc := jScan(t, dir)
	if err := os.Remove(filepath.Join(dir, j11)); err != nil {
		t.Fatal(err)
	}
	if _, err := NewJavaScanner().UpdateFile(inc, filepath.Join(dir, main)); err != nil {
		t.Fatal(err)
	}
	if got := mustRes(t, inc, "com.t.a.A").Location.Path; got != filepath.Join(dir, main) {
		t.Errorf("after the java11 copy is deleted A is located in %s, want the survivor", got)
	}
	wantEdge(t, inc, "com.t.b.U.use(A)", "calls", "com.t.a.A.m()")
}

// Incremental re-resolution of the new call edges matches a full scan.
func TestCallResolutionIncrementalMatchesFull(t *testing.T) {
	dir := writeProj(t, map[string]string{
		"src/main/java/com/t/h/Base.java": "package com.t.h;\npublic class Base {\n    public void helper() {}\n}\n",
		"src/main/java/com/t/h/Leaf.java": "package com.t.h;\nimport static com.t.u.Util.*;\nimport com.t.u.Util;\npublic class Leaf extends Base {\n    public void go(Util u) {\n        helper();\n        stat();\n        u.inst();\n        new Object() {{ helper(); }};\n    }\n}\n",
		"src/main/java/com/t/u/Util.java": "package com.t.u;\npublic class Util {\n    public static void stat() {}\n    public void inst() {}\n}\n",
	})
	full := jScan(t, dir)
	for _, f := range []string{"src/main/java/com/t/h/Leaf.java", "src/main/java/com/t/h/Base.java", "src/main/java/com/t/u/Util.java"} {
		inc := jScan(t, dir)
		if _, err := NewJavaScanner().UpdateFile(inc, filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
		if a, b := canonTopo(full), canonTopo(inc); a != b {
			t.Errorf("re-parsing %s: incremental differs from full:\n--- full ---\n%s\n--- incremental ---\n%s", f, a, b)
		}
	}
	const goID = "com.t.h.Leaf.go(Util)"
	wantEdge(t, full, goID, "calls", "com.t.h.Base.helper()")
	wantEdge(t, full, goID, "calls", "com.t.u.Util.stat()")
	wantEdge(t, full, goID, "calls", "com.t.u.Util.inst()")
	wantEdge(t, full, "com.t.h.Leaf$anon1.<instance-init>()", "calls", "com.t.h.Base.helper()")
}
