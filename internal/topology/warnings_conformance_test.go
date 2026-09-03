package topology_test

import (
	"os"
	"path/filepath"
	"testing"

	"aracne/internal/topology"
	"aracne/internal/topology/domain"
)

// Declared-conformance warnings: a type that promises an interface and does not deliver.
//
// This is the other half of what a signature change breaks. The call-site rule catches a
// CALLER passing the wrong arguments; nothing caught the implementer that promised a method
// and stopped providing it. Widening one interface method silently unhooks every
// implementer, and the only sign was a build failure somewhere else.
//
// The check runs only where the declaration is a CLAIM THAT CAN BE WRONG -- verified by
// scanning a deliberately broken fixture in each language and confirming the implements
// edge survives. Go and Python's Protocol are structural: their edge is DERIVED from
// satisfaction, so a broken type simply has no edge and there is no claim to check.
//
// Every false-positive class below was found by running this against the repository's own
// testing_ground corpus, which went from 7 conflicts to 1 -- the one it documents as
// deliberately incomplete.

type conformanceCase struct {
	name  string
	files map[string]string
	// how many interface_conflict warnings the project must produce, and a substring the
	// message must contain when it produces one.
	want    int
	message string
}

func scanConformance(t *testing.T, files map[string]string) []domain.TopologyWarning {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, contractRegistry()); err != nil {
		t.Fatalf("FullScan: %v", err)
	}
	w, err := mgr.ListWarnings("", "", domain.WarnInterfaceConflict)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func runConformanceCases(t *testing.T, cases []conformanceCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scanConformance(t, c.files)
			if len(got) != c.want {
				t.Fatalf("got %d conflict(s), want %d: %+v", len(got), c.want, got)
			}
			if c.message == "" {
				return
			}
			for _, w := range got {
				if contains(w.Message, c.message) {
					return
				}
			}
			t.Errorf("no message mentioned %q; got %+v", c.message, got)
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func rustProj(lib string) map[string]string {
	return map[string]string{
		"Cargo.toml": "[package]\nname = \"cf\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"src/lib.rs": lib,
	}
}

func javaProj(files map[string]string) map[string]string {
	out := map[string]string{
		"pom.xml": `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.cf</groupId><artifactId>cf</artifactId><version>1.0.0</version>
</project>
`,
	}
	for k, v := range files {
		out["src/main/java/com/cf/"+k] = v
	}
	return out
}

func tsProj(files map[string]string) map[string]string {
	out := map[string]string{
		"package.json":  `{ "name": "cf", "version": "1.0.0" }` + "\n",
		"tsconfig.json": `{ "compilerOptions": { "target": "ES2020" } }` + "\n",
	}
	for k, v := range files {
		out[k] = v
	}
	return out
}

// --- Rust: exact match required ---------------------------------------------

func TestConformanceRust(t *testing.T) {
	runConformanceCases(t, []conformanceCase{
		{
			name: "an impl that no longer matches the trait",
			files: rustProj(`pub trait Draw { fn draw(&self, scale: i32, extra: i32) -> i32; }
pub struct Circle;
impl Draw for Circle { fn draw(&self, scale: i32) -> i32 { scale } }
`),
			want: 1, message: "does not satisfy Draw.draw",
		},
		{
			name: "a matching impl",
			files: rustProj(`pub trait Draw { fn draw(&self, scale: i32, extra: i32) -> i32; }
pub struct Circle;
impl Draw for Circle { fn draw(&self, scale: i32, extra: i32) -> i32 { scale + extra } }
`),
			want: 0,
		},
		{
			// A trait supplying a body is asking nothing of its implementers.
			name: "a defaulted trait method need not be provided",
			files: rustProj(`pub trait Draw {
    fn draw(&self) -> i32;
    fn label(&self) -> i32 { 0 }
}
pub struct Circle;
impl Draw for Circle { fn draw(&self) -> i32 { 1 } }
`),
			want: 0,
		},
		{
			// FALSE POSITIVE FOUND ON testing_ground: `trait Drawable: Shape` propagates
			// Shape's requirements to Drawable's implementers. Drawable is not itself an
			// implementer and has no methods to provide.
			name: "a trait extending a trait is not an implementer",
			files: rustProj(`pub trait Shape { fn area(&self) -> i32; }
pub trait Drawable: Shape { fn render(&self) -> i32; }
pub struct Circle;
impl Shape for Circle { fn area(&self) -> i32 { 1 } }
impl Drawable for Circle { fn render(&self) -> i32 { 2 } }
`),
			want: 0,
		},
		{
			// FALSE POSITIVE FOUND ON testing_ground: the compiler writes a derived impl,
			// so its methods exist in the built crate and nowhere in the source.
			name: "a derived impl has no source to check",
			files: rustProj(`pub trait Tagged { fn tag(&self) -> i32; }
#[derive(Tagged)]
pub struct Label { pub text: i32 }
`),
			want: 0,
		},
	})
}

// --- Java: exact match required, and overloads are ordinary ------------------

func TestConformanceJava(t *testing.T) {
	runConformanceCases(t, []conformanceCase{
		{
			name: "a class that no longer matches the interface",
			files: javaProj(map[string]string{
				"R.java": "package com.cf;\npublic interface R { int run(int a, int b); }\n",
				"T.java": "package com.cf;\npublic class T implements R { public int run(int a) { return a; } }\n",
			}),
			want: 1, message: "does not satisfy R.run",
		},
		{
			name: "a matching class",
			files: javaProj(map[string]string{
				"R.java": "package com.cf;\npublic interface R { int run(int a, int b); }\n",
				"T.java": "package com.cf;\npublic class T implements R { public int run(int a, int b) { return a; } }\n",
			}),
			want: 0,
		},
		{
			// FALSE POSITIVE FOUND ON testing_ground: Circle declared both area() and
			// area(int). Keying the implementer's methods by name alone let the overload
			// stand in for the override, so the verdict depended on map iteration order.
			name: "an overload alongside the override",
			files: javaProj(map[string]string{
				"R.java": "package com.cf;\npublic interface R { int run(); }\n",
				"T.java": `package com.cf;
public class T implements R {
    public int run() { return 1; }
    public int run(int scale) { return scale; }
}
`,
			}),
			want: 0,
		},
		{
			name: "a class missing the method entirely",
			files: javaProj(map[string]string{
				"R.java": "package com.cf;\npublic interface R { int run(); int stop(); }\n",
				"T.java": "package com.cf;\npublic class T implements R { public int run() { return 1; } }\n",
			}),
			want: 1, message: "does not provide stop",
		},
	})
}

// --- TypeScript: an implementation may ignore trailing parameters ------------

func TestConformanceTypeScript(t *testing.T) {
	runConformanceCases(t, []conformanceCase{
		{
			// Ordinary, idiomatic TypeScript: a handler that ignores its second argument
			// declares one parameter. Demanding equal arity here would warn about correct
			// code, which is the failure this whole mechanism exists to remove.
			name: "an implementation with FEWER parameters is legal",
			files: tsProj(map[string]string{
				"a.ts": `export interface Shape { area(scale: number, extra: number): number; }
export class Sq implements Shape { area(scale: number): number { return scale; } }
`,
			}),
			want: 0,
		},
		{
			name: "an implementation requiring MORE than the interface supplies",
			files: tsProj(map[string]string{
				"a.ts": `export interface Shape { area(scale: number): number; }
export class Sq implements Shape { area(scale: number, extra: number): number { return scale; } }
`,
			}),
			want: 1, message: "requires 2 parameter(s)",
		},
		{
			// An extra parameter that is optional demands nothing.
			name: "an optional extra parameter is fine",
			files: tsProj(map[string]string{
				"a.ts": `export interface Shape { area(scale: number): number; }
export class Sq implements Shape { area(scale: number, extra?: number): number { return scale; } }
`,
			}),
			want: 0,
		},
		{
			name: "a class missing the method entirely",
			files: tsProj(map[string]string{
				"a.ts": `export interface Shape { area(): number; describe(): string; }
export class Sq implements Shape { area(): number { return 1; } }
`,
			}),
			want: 1, message: "does not provide describe",
		},
		{
			// FALSE POSITIVE FOUND ON testing_ground: `interface Solid extends Shape`
			// propagates the requirement rather than fulfilling it.
			name: "an interface extending an interface is not an implementer",
			files: tsProj(map[string]string{
				"a.ts": `export interface Shape { area(): number; }
export interface Solid extends Shape { volume(): number; }
export class Cube implements Solid { area(): number { return 1; } volume(): number { return 1; } }
`,
			}),
			want: 0,
		},
	})
}

// --- Python: an ABC requires the NAME, and nothing more ----------------------

func TestConformancePython(t *testing.T) {
	runConformanceCases(t, []conformanceCase{
		{
			name: "an abstract method that is never implemented",
			files: map[string]string{"a.py": `from abc import ABC, abstractmethod


class Base(ABC):
    @abstractmethod
    def run(self, a: int) -> int: ...

    @abstractmethod
    def stop(self) -> int: ...


class Impl(Base):
    def run(self, a: int) -> int:
        return a
`},
			want: 1, message: "does not provide stop",
		},
		{
			name: "every abstract method implemented",
			files: map[string]string{"a.py": `from abc import ABC, abstractmethod


class Base(ABC):
    @abstractmethod
    def run(self, a: int) -> int: ...


class Impl(Base):
    def run(self, a: int) -> int:
        return a
`},
			want: 0,
		},
		{
			// Python enforces nothing about an override's signature -- it is legal, and the
			// real breakage (a caller passing what the override no longer accepts) is
			// caught by the call-site rule instead, which is the right place for it.
			name: "a differing override signature is legal",
			files: map[string]string{"a.py": `from abc import ABC, abstractmethod


class Base(ABC):
    @abstractmethod
    def run(self, a: int, b: int) -> int: ...


class Impl(Base):
    def run(self, a: int) -> int:
        return a
`},
			want: 0,
		},
		{
			// Leaving requirements unmet is what makes a class abstract.
			name: "an intermediate abstract class may leave things unmet",
			files: map[string]string{"a.py": `from abc import ABC, abstractmethod


class Base(ABC):
    @abstractmethod
    def run(self) -> int: ...

    @abstractmethod
    def stop(self) -> int: ...


class Middle(Base):
    def run(self) -> int:
        return 1

    @abstractmethod
    def extra(self) -> int: ...
`},
			want: 0,
		},
		{
			// A concrete method inherited from an intermediate class satisfies the
			// grandparent's abstract one; the abstract declaration itself never does.
			name: "a concrete method inherited from a base satisfies the requirement",
			files: map[string]string{"a.py": `from abc import ABC, abstractmethod


class Base(ABC):
    @abstractmethod
    def run(self) -> int: ...


class Middle(Base):
    def run(self) -> int:
        return 1


class Leaf(Middle):
    def other(self) -> int:
        return 2
`},
			want: 0,
		},
	})
}

// TestConformanceSkipsStructuralLanguages pins the deliberate exclusion.
//
// Go conformance is structural and its implements edge is DERIVED from satisfaction --
// matchStructsToInterfaces only creates it when the type already satisfies the interface.
// A broken type therefore has no edge and there is no claim to check. Reporting one anyway
// would mean guessing which types were "meant" to implement which interfaces, and a guess
// here is a false warning about correct code.
func TestConformanceSkipsStructuralLanguages(t *testing.T) {
	got := scanConformance(t, map[string]string{
		"go.mod": "module tp\n\ngo 1.21\n",
		"a.go": `package tp

type Shape interface{ Area(scale int) int }

type Sq struct{}

func (s Sq) Area() int { return 1 }

func Use(sh Shape) int { return sh.Area(2) }
`,
	})
	if len(got) != 0 {
		t.Errorf("Go conformance is structural and must produce no conflict, got %+v", got)
	}
}

// TestConformanceClearsWhenFixed pins that the warning is derived from current state, not
// remembered from an event -- the same property that makes the call-site rule immune to a
// revert. Nothing is stored, so nothing has to be cleared.
func TestConformanceClearsWhenFixed(t *testing.T) {
	c := contractLang{
		files: map[string]string{
			"Cargo.toml": "[package]\nname = \"cf\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
			"src/lib.rs": `pub trait Draw { fn draw(&self, scale: i32) -> i32; }
pub struct Circle;
impl Draw for Circle { fn draw(&self, scale: i32) -> i32 { scale } }
`,
		},
	}
	p := newContractProj(t, c)
	conflicts := func() int {
		t.Helper()
		w, err := p.mgr.ListWarnings("", "", domain.WarnInterfaceConflict)
		if err != nil {
			t.Fatal(err)
		}
		return len(w)
	}
	if n := conflicts(); n != 0 {
		t.Fatalf("a correct project must be clean, got %d", n)
	}

	// Widen the trait method; the impl no longer satisfies it.
	p.edit("src/lib.rs", `pub trait Draw { fn draw(&self, scale: i32, extra: i32) -> i32; }
pub struct Circle;
impl Draw for Circle { fn draw(&self, scale: i32) -> i32 { scale } }
`)
	if n := conflicts(); n != 1 {
		t.Fatalf("widening the trait method must report the implementer, got %d", n)
	}

	// Fix the impl.
	p.edit("src/lib.rs", `pub trait Draw { fn draw(&self, scale: i32, extra: i32) -> i32; }
pub struct Circle;
impl Draw for Circle { fn draw(&self, scale: i32, extra: i32) -> i32 { scale + extra } }
`)
	if n := conflicts(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", domain.WarnInterfaceConflict)
		t.Errorf("fixing the impl must clear the conflict, %d left: %+v", n, w)
	}

	// And reverting the trait clears it just as well, from the other side.
	p.edit("src/lib.rs", `pub trait Draw { fn draw(&self, scale: i32, extra: i32) -> i32; }
pub struct Circle;
impl Draw for Circle { fn draw(&self, scale: i32) -> i32 { scale } }
`)
	if n := conflicts(); n != 1 {
		t.Fatalf("expected the conflict back, got %d", n)
	}
	p.edit("src/lib.rs", `pub trait Draw { fn draw(&self, scale: i32) -> i32; }
pub struct Circle;
impl Draw for Circle { fn draw(&self, scale: i32) -> i32 { scale } }
`)
	if n := conflicts(); n != 0 {
		t.Errorf("narrowing the trait back must clear it too, got %d", n)
	}
}
