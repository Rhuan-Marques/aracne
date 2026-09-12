package rustscanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	rust "github.com/Rhuan-Marques/aracne/internal/topology/rust"
)

// callSite returns the one call site caller recorded for callee.
func callSite(t *testing.T, topo *domain.Topology, caller, callee string) contract.CallSite {
	t.Helper()
	sites := contract.CallSitesOf(topo.Resources[caller], callee)
	if len(sites) != 1 {
		t.Fatalf("%s: want one call site for %s, got %v (conns %v)", caller, callee, sites, topo.Resources[caller].Connections)
	}
	return sites[0]
}

// inputNames lists a function's declared parameter names, as the contract matcher reads them.
func inputNames(topo *domain.Topology, id string) []string {
	var out []string
	for _, p := range contract.Params(topo.Resources[id]) {
		out = append(out, p.Name)
	}
	return out
}

// RS-4: a receiver typed by a parameter or a `let` annotation resolves its method calls.
func TestAnnotatedReceiversResolve(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs": "pub mod shapes;\npub mod user;\n",
		"src/shapes.rs": "pub struct Circle { pub r: f64 }\nimpl Circle {\n" +
			"    pub fn area(&self) -> f64 { self.r }\n" +
			"    pub fn merge(&self, other: &Self) -> f64 { other.area() }\n}\n",
		"src/user.rs": `use crate::shapes::Circle;
pub fn by_ref(c: &Circle) -> f64 { c.area() }
pub fn by_mut(mut c: Circle) -> f64 { c.area() }
pub fn by_let() -> f64 {
    let c: Circle = Default::default();
    c.area()
}
pub fn untyped(n: u32) -> f64 {
    let c = unknown_source(n);
    c.area()
}
`,
	})
	topo := scan(t, dir)
	for _, w := range [][3]string{
		{"unit::user::by_ref", "calls", "unit::shapes::Circle::area"},
		{"unit::user::by_mut", "calls", "unit::shapes::Circle::area"},
		{"unit::user::by_let", "calls", "unit::shapes::Circle::area"},
		{"unit::user::by_let", "uses_struct", "unit::shapes::Circle"},
		{"unit::shapes::Circle::merge", "calls", "unit::shapes::Circle::area"},
	} {
		if !exactConn(topo, w[0], w[1], w[2]) {
			t.Errorf("%s should %s %s; has %v", w[0], w[1], w[2], topo.Resources[w[0]].Connections)
		}
	}
	// Nothing names the type of an inferred-from-nowhere binding: it stays unresolved.
	if calls := topo.Resources["unit::user::untyped"].Connections["calls"]; len(calls) != 0 {
		t.Errorf("an untyped binding must not resolve its method call: %v", calls)
	}
}

// RS-5: only a cfg predicate that can hold in nothing but a test build drops an item.
func TestCfgTestPredicate(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs": `#[cfg(not(test))]
pub fn real_only() {}
#[cfg(feature = "contest")]
pub fn gated() {}
#[cfg(any(test, unix))]
pub fn unix_or_test() {}
#[cfg(test)]
pub fn test_only() {}
#[cfg(all(test, feature = "slow"))]
pub fn slow_test_only() {}
#[cfg(any(test))]
pub fn any_test_only() {}
`,
	})
	topo := scan(t, dir)
	for _, id := range []string{"unit::real_only", "unit::gated", "unit::unix_or_test"} {
		if !hasRes(topo, id) {
			t.Errorf("%s exists outside test builds and must be indexed", id)
		}
	}
	for _, id := range []string{"unit::test_only", "unit::slow_test_only", "unit::any_test_only"} {
		if hasRes(topo, id) {
			t.Errorf("%s exists only in test builds and must be skipped", id)
		}
	}
}

func TestCfgRequiresTestUnit(t *testing.T) {
	for pred, want := range map[string]bool{
		"(test)":                             true,
		"( test )":                           true,
		"(all(test, feature = \"x\"))":       true,
		"(all(unix, all(test)))":             true,
		"(any(test, all(test, unix)))":       true,
		"(not(test))":                        false,
		"(feature = \"contest\")":            false,
		"(feature = \"test\")":               false,
		"(any(test, unix))":                  false,
		"(all(unix, feature = \"a,test)\"))": false,
		"(testing)":                          false,
	} {
		if got := cfgRequiresTest(pred); got != want {
			t.Errorf("cfgRequiresTest(%s) = %v, want %v", pred, got, want)
		}
	}
}

// RS-6: a comment between an attribute and its item does not detach the attribute.
func TestCommentBetweenAttributeAndItem(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs": `pub trait Tagged {}

#[cfg(test)]
// the tests
mod tests {
    pub fn leaked() {}
}

#[derive(Tagged)]
/// A documented struct.
pub struct Z;

#[derive(Tagged)]
pub struct A;
// a comment between two items
pub struct B;
`,
	})
	topo := scan(t, dir)
	if hasRes(topo, "::leaked") {
		t.Error("#[cfg(test)] followed by a comment must still skip the test module")
	}
	if !exactConn(topo, "unit::Z", "implements", "unit::Tagged") {
		t.Error("#[derive(Tagged)] followed by a doc comment must still implement Tagged")
	}
	if !exactConn(topo, "unit::A", "implements", "unit::Tagged") {
		t.Error("A derives Tagged")
	}
	if exactConn(topo, "unit::B", "implements", "unit::Tagged") {
		t.Error("an attribute applies to the next item only, never to the one after it")
	}
}

// rs7Files: the file module and an inline module declare the same names.
func rs7Files() map[string]string {
	return map[string]string{
		"src/lib.rs": `pub mod shapes;
pub fn b() -> u32 { 1 }
pub struct Local;
impl Local { pub fn outer_only(&self) {} }
pub fn a2() -> u32 { b() }
pub mod inner {
    use super::shapes::Circle;
    pub fn b() -> u32 { 2 }
    pub fn a(c: &Circle) -> f64 { b(); c.area() }
    pub fn up() -> u32 { super::b() }
    pub struct Local;
    impl Local { pub fn inner_only(&self) {} }
}
`,
		"src/shapes.rs": "pub struct Circle { pub r: f64 }\nimpl Circle {\n    pub fn area(&self) -> f64 { self.r }\n}\n",
	}
}

// RS-7: code in an inline `mod` resolves in that module, with that module's imports.
func TestInlineModuleScope(t *testing.T) {
	dir := writeCrate(t, "", rs7Files())
	topo := scan(t, dir)
	for _, w := range [][3]string{
		{"unit::inner::a", "calls", "unit::inner::b"},
		{"unit::inner::a", "calls", "unit::shapes::Circle::area"},
		{"unit::inner::up", "calls", "unit::b"},
		{"unit::a2", "calls", "unit::b"},
		{"unit::inner::Local", "methods", "unit::inner::Local::inner_only"},
		{"unit::Local", "methods", "unit::Local::outer_only"},
	} {
		if !exactConn(topo, w[0], w[1], w[2]) {
			t.Errorf("%s should %s %s; has %v", w[0], w[1], w[2], topo.Resources[w[0]].Connections)
		}
	}
	if exactConn(topo, "unit::inner::a", "calls", "unit::b") {
		t.Error("inner::a calls its own module's b, not the file module's")
	}
	if hasRes(topo, "unit::Local::inner_only") {
		t.Error("the inline module's impl must attach to the inline module's Local")
	}

	// Updating either file reproduces the full scan's edges: the inline scope's imports
	// round-trip through the persisted use bindings.
	for _, rel := range []string{"src/shapes.rs", "src/lib.rs"} {
		if _, err := NewRustScanner().UpdateFile(topo, filepath.Join(dir, rel)); err != nil {
			t.Fatalf("UpdateFile(%s): %v", rel, err)
		}
		inc, fresh := edgeSet(topo, dir), edgeSet(scan(t, dir), dir)
		if strings.Join(inc, "\n") != strings.Join(fresh, "\n") {
			t.Errorf("after updating %s, incremental edges differ from a full scan:\n%s\n---\n%s", rel, strings.Join(inc, "\n"), strings.Join(fresh, "\n"))
		}
	}
}

// RS-8: a typed `self` is the receiver, not a parameter, and a path (UFCS) call's first
// argument is the receiver, so neither counts toward the arity a call is checked against.
func TestReceiverArity(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs": "pub mod fut;\npub mod user;\n",
		"src/fut.rs": `use std::pin::Pin;
pub struct Fut { pub v: u32 }
impl Fut {
    pub fn new(v: u32) -> Self { Fut { v } }
    pub fn poll(self: Pin<&mut Self>, n: u32) -> u32 { n }
    pub fn by_ref(self: &Self) -> u32 { self.v }
    pub fn by_mut(mut self: &mut Self, k: u32) -> u32 { k }
    pub fn boxed(self: Box<Self>) -> u32 { 0 }
    pub fn area(&self, k: u32) -> u32 { self.v * k }
    pub fn go(self: Pin<&mut Self>) -> u32 { self.poll(1) }
}
`,
		"src/user.rs": `use crate::fut::Fut;
pub fn direct(f: &Fut) -> u32 { Fut::area(f, 2) }
pub fn make() -> Fut { Fut::new(3) }
`,
	})
	topo := scan(t, dir)
	for id, want := range map[string]struct {
		params   string
		receiver string
	}{
		"unit::fut::Fut::poll":   {"n", "value"},
		"unit::fut::Fut::by_ref": {"", "ref"},
		"unit::fut::Fut::by_mut": {"k", "ref_mut"},
		"unit::fut::Fut::boxed":  {"", "value"},
		"unit::fut::Fut::area":   {"k", "ref"},
		"unit::fut::Fut::new":    {"v", ""},
	} {
		r := topo.Resources[id]
		if got := strings.Join(inputNames(topo, id), ","); got != want.params {
			t.Errorf("%s params = %q, want %q", id, got, want.params)
		}
		if got, _ := r.Properties["receiver"].(string); got != want.receiver {
			t.Errorf("%s receiver = %q, want %q", id, got, want.receiver)
		}
		if assoc, _ := r.Properties["is_associated"].(bool); assoc != (want.receiver == "") {
			t.Errorf("%s is_associated = %v", id, assoc)
		}
	}
	if s := callSite(t, topo, "unit::fut::Fut::go", "unit::fut::Fut::poll"); s.N != 1 {
		t.Errorf("self.poll(1) passes one argument, recorded %d", s.N)
	}
	if s := callSite(t, topo, "unit::user::direct", "unit::fut::Fut::area"); s.N != 1 || len(s.Types) != 1 {
		t.Errorf("Fut::area(f, 2) passes the receiver and one argument, recorded %+v", s)
	}
	// An associated function has no receiver: every argument counts.
	if s := callSite(t, topo, "unit::user::make", "unit::fut::Fut::new"); s.N != 1 {
		t.Errorf("Fut::new(3) passes one argument, recorded %d", s.N)
	}
}

// RS-9: src/lib.rs and src/main.rs are two crates, and each multi-file example is its own.
func TestBinaryAndExampleRoots(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs":          "pub fn helper() -> u32 { 1 }\npub fn run() {}\n",
		"src/main.rs":         "fn helper() -> u32 { 9 }\nfn main() { helper(); unit::run(); }\n",
		"examples/a/main.rs":  "fn main() {}\n",
		"examples/b/main.rs":  "fn main() {}\n",
		"examples/single.rs":  "fn main() {}\n",
		"examples/b/extra.rs": "pub fn more() {}\n",
	})
	topo := scan(t, dir)
	for id, file := range map[string]string{
		"unit::helper":       "src/lib.rs",
		"unit::main::helper": "src/main.rs",
		"unit::main::main":   "src/main.rs",
		"unit::a::main":      "examples/a/main.rs",
		"unit::b::main":      "examples/b/main.rs",
		"unit::single::main": "examples/single.rs",
		"unit::extra::more":  "examples/b/extra.rs",
	} {
		r, ok := topo.Resources[id]
		if !ok {
			t.Errorf("missing %s", id)
			continue
		}
		if !strings.HasSuffix(r.Location.Path, file) {
			t.Errorf("%s is declared in %s, located at %s", id, file, r.Location.Path)
		}
	}
	if !exactConn(topo, "unit::main::main", "calls", "unit::main::helper") {
		t.Error("main calls the binary's own helper")
	}
	if !exactConn(topo, "unit::main::main", "calls", "unit::run") {
		t.Error("main calls the library through the crate name")
	}

	// A binary-only package keeps its crate root: nothing collides, nothing moves.
	bin := writeCrate(t, "", map[string]string{
		"src/main.rs": "mod cli;\nfn main() { cli::go(); }\n",
		"src/cli.rs":  "pub fn go() {}\n",
	})
	btopo := scan(t, bin)
	if !hasRes(btopo, "unit::main") || hasRes(btopo, "unit::main::main") {
		t.Error("a package with only src/main.rs keeps main at the crate root")
	}
	if !exactConn(btopo, "unit::main", "calls", "unit::cli::go") {
		t.Error("the binary root resolves its modules")
	}
}

// RS-10: a comment after the package name is not part of the name.
func TestCargoInlineComments(t *testing.T) {
	dir := t.TempDir()
	cargo := "[package] # the package\nname = \"p8\" # the crate\nversion = \"0.1.0\"\n\n" +
		"[dependencies] # deps\nserde = \"1\" # serialization\n"
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
		t.Fatal(err)
	}
	name, deps, _, _, hasPkg := parseCargoToml(filepath.Join(dir, "Cargo.toml"))
	if name != "p8" || !hasPkg {
		t.Errorf("name = %q (hasPackage %v), want p8", name, hasPkg)
	}
	if strings.Join(deps, ",") != "serde" {
		t.Errorf("deps = %v, want [serde]", deps)
	}
	if got := stripTomlComment(`name = "a#b" # c`); strings.TrimSpace(got) != `name = "a#b"` {
		t.Errorf("a # inside a string is not a comment: %q", got)
	}
}

// RS-12: `[target.<cfg>.dependencies]` declares dependencies like `[dependencies]` does.
func TestTargetDependencies(t *testing.T) {
	dir := t.TempDir()
	cargo := "[package]\nname = \"unit\"\nversion = \"0.1.0\"\n\n" +
		"[target.'cfg(unix)'.dependencies]\nlibc = \"0.2\"\n\n" +
		"[target.\"cfg(windows)\".dev-dependencies]\nwinapi = \"0.3\"\n\n" +
		"[target.x86_64-pc-windows-gnu.build-dependencies.cc]\nversion = \"1\"\n\n" +
		"[target.'cfg(unix)'.metadata]\nnot_a_dep = \"x\"\n"
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "use libc::getpid;\npub fn pid() { getpid(); }\n"
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, deps, _, _, _ := parseCargoToml(filepath.Join(dir, "Cargo.toml"))
	if got := strings.Join(deps, ","); got != "libc,winapi,cc" {
		t.Errorf("deps = %q, want libc,winapi,cc", got)
	}
	topo := scan(t, dir)
	if !connHas(topo, "src/lib.rs", "imports_dependency", "libc") {
		t.Error("lib.rs should imports_dependency libc")
	}
	if !exactConn(topo, "unit::pid", "uses_dependency", "libc") {
		t.Error("pid should uses_dependency libc")
	}
}

// RS-12: `pub` on a tuple field is that field's visibility, not a field of its own.
func TestTupleFieldVisibility(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs": "pub struct Meters(pub f64);\npub struct Pair(pub(crate) u8, #[allow(dead_code)] String);\n",
	})
	topo := scan(t, dir)
	for id, want := range map[string]string{"unit::Meters": "0:f64", "unit::Pair": "0:u8,1:String"} {
		fields, _ := topo.Resources[id].Properties["fields"].([]rust.VariableDefinition)
		var got []string
		for _, f := range fields {
			got = append(got, f.Name+":"+f.Typing)
		}
		if strings.Join(got, ",") != want {
			t.Errorf("%s fields = %v, want %s", id, got, want)
		}
	}
}

// RS-12: a call written with explicit type arguments (turbofish) is still a call.
func TestTurbofishCalls(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs": `pub struct Circle;
impl Circle {
    pub fn make<T>() -> Self { Circle }
    pub fn area<T>(&self) -> u32 { 0 }
}
pub fn generic_fn<T>() -> u32 { 0 }
pub fn turbo() -> u32 {
    let c = Circle::make::<u8>();
    generic_fn::<i32>() + c.area::<u16>()
}
`,
	})
	topo := scan(t, dir)
	for _, callee := range []string{"unit::generic_fn", "unit::Circle::make", "unit::Circle::area"} {
		if !exactConn(topo, "unit::turbo", "calls", callee) {
			t.Errorf("turbo should call %s; has %v", callee, topo.Resources["unit::turbo"].Connections)
		}
	}
}
