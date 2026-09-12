package rustscanner

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// writeTree writes files (paths relative to a fresh temp dir) and returns the dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// exactConn reports whether resource id has a conn edge to exactly target.
func exactConn(topo *domain.Topology, id, conn, target string) bool {
	for _, tgt := range topo.Resources[id].Connections[conn] {
		if tgt == target {
			return true
		}
	}
	return false
}

// edgeSet renders every resource's edges as sorted "src -kind-> dst" lines, with the
// temp root stripped, for comparing two scans of the same tree.
func edgeSet(topo *domain.Topology, root string) []string {
	var out []string
	for id, r := range topo.Resources {
		for kind, tgts := range r.Connections {
			for _, tgt := range tgts {
				out = append(out, strings.ReplaceAll(id+" -"+kind+"-> "+tgt, root, "<R>"))
			}
		}
	}
	sort.Strings(out)
	return out
}

// RS-1: `members = ["crates/*"]` must register every member crate. Before the fix the
// glob was joined literally, no crate was found, and every file fell back to the
// nameless `crate::<file>` root, so each member's lib.rs collapsed into `crate::lib`.
func TestWorkspaceGlobMembers(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"Cargo.toml": "[workspace]\nmembers = [\n    \"crates/*\",\n]\nexclude = [\"crates/skipped\"]\n",
		"crates/alpha/Cargo.toml": "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\n\n" +
			"[dependencies]\nbeta-core = { path = \"../beta-core\" }\n",
		"crates/alpha/src/lib.rs":     "use beta_core::Engine;\npub fn run() -> u32 {\n    let e = Engine::new();\n    e.spin()\n}\n",
		"crates/beta-core/Cargo.toml": "[package]\nname = \"beta-core\"\nversion = \"0.1.0\"\n",
		"crates/beta-core/src/lib.rs": "pub mod net;\npub struct Engine;\nimpl Engine {\n" +
			"    pub fn new() -> Self { Engine }\n    pub fn spin(&self) -> u32 { 1 }\n}\n",
		"crates/beta-core/src/net.rs":  "pub fn connect() {}\n",
		"crates/beta-core/src/main.rs": "fn main() { beta_core::net::connect(); }\n",
		// A directory the glob matches but that is no crate, and an excluded crate.
		"crates/notacrate/README.md": "not a crate\n",
		"crates/skipped/Cargo.toml":  "[package]\nname = \"skipped\"\nversion = \"0.1.0\"\n",
		"crates/skipped/src/lib.rs":  "pub fn hidden() {}\n",
	})
	topo := scan(t, dir)

	for _, id := range []string{
		"alpha::run",
		"beta_core::Engine", "beta_core::Engine::new", "beta_core::Engine::spin",
		// beta-core has both src/lib.rs and src/main.rs: the binary is a second crate,
		// rooted at `beta_core::main` so its items never collide with the library's (RS-9).
		"beta_core::net::connect", "beta_core::main::main",
	} {
		if _, ok := topo.Resources[id]; !ok {
			t.Errorf("missing resource %s", id)
		}
	}
	for id, r := range topo.Resources {
		member := strings.Contains(r.Location.Path, "/crates/alpha/") || strings.Contains(r.Location.Path, "/crates/beta-core/")
		switch {
		case member && strings.HasPrefix(id, "crate::"):
			t.Errorf("member file collapsed into the nameless crate root: %s", id)
		case strings.HasPrefix(id, "skipped::"):
			t.Errorf("excluded crate registered: %s", id)
		case strings.HasSuffix(id, "::hidden") && (strings.HasPrefix(id, "alpha::") || strings.HasPrefix(id, "beta_core::")):
			t.Errorf("a file under no member is filed under a member crate: %s", id)
		case strings.Contains(id, "beta-core::"):
			t.Errorf("crate root keeps the Cargo dash, source can never write it: %s", id)
		}
	}
	// The sibling crate is internal: an imports_module edge, never a dependency.
	if !connHas(topo, "crates/alpha/src/lib.rs", "imports_module", "crates/beta-core/src/lib.rs") {
		t.Error("alpha's lib.rs should imports_module beta-core's lib.rs")
	}
	if _, ok := topo.Resources["beta_core"]; ok {
		t.Error("a workspace member must not become an external dependency node")
	}
	if deps := topo.Resources["beta_core::main::main"].Connections["uses_dependency"]; len(deps) != 0 {
		t.Errorf("a crate never depends on itself: %v", deps)
	}
	// Cross-crate resolution works through the normalised name.
	if !exactConn(topo, "alpha::run", "calls", "beta_core::Engine::new") {
		t.Error("alpha::run should call beta_core::Engine::new")
	}
	if !exactConn(topo, "alpha::run", "calls", "beta_core::Engine::spin") {
		t.Error("alpha::run should call beta_core::Engine::spin via the ctor's Self return")
	}
	if !exactConn(topo, "beta_core::main::main", "calls", "beta_core::net::connect") {
		t.Error("beta_core::main::main should call beta_core::net::connect through the crate-name path")
	}
}

// RS-1: `**` matches any depth, and a matched directory without a Cargo.toml is skipped.
func TestWorkspaceRecursiveGlob(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"Cargo.toml":                "[workspace]\nmembers = [\"libs/**\"]\n",
		"libs/x/Cargo.toml":         "[package]\nname = \"x\"\nversion = \"0.1.0\"\n",
		"libs/x/src/lib.rs":         "pub fn fx() {}\n",
		"libs/group/y/Cargo.toml":   "[package]\nname = \"y\"\nversion = \"0.1.0\"\n",
		"libs/group/y/src/lib.rs":   "pub fn fy() {}\n",
		"libs/group/y/src/extra.rs": "pub fn fe() {}\n",
	})
	topo := scan(t, dir)
	for _, id := range []string{"x::fx", "y::fy", "y::extra::fe"} {
		if _, ok := topo.Resources[id]; !ok {
			t.Errorf("missing resource %s", id)
		}
	}
}

// rs2Files: two modules declare the same struct and trait names; c.rs imports b's.
func rs2Files() map[string]string {
	return map[string]string{
		"src/lib.rs": "pub mod a;\npub mod b;\npub mod c;\npub mod d;\npub mod e;\npub mod f;\n",
		"src/a.rs":   "pub struct Thing;\npub trait Named { fn name(&self) -> String; }\n",
		"src/b.rs":   "pub struct Thing;\npub trait Named { fn name(&self) -> String; }\n",
		"src/c.rs": `use crate::b::Thing;
use crate::b::Named;
impl Thing {
    pub fn only_on_b(&self) {}
}
impl Named for Thing {
    fn name(&self) -> String { String::new() }
}
pub fn make() -> Thing { Thing }
pub fn user() {
    let t = make();
    t.only_on_b();
}
`,
		// No import and no local Thing: two candidates, so the impl stays unresolved.
		// Solo is unique, so the by-name fallback still applies to it.
		"src/d.rs": "pub struct Other;\nimpl Thing {\n    pub fn orphan(&self) {}\n}\nimpl Solo for Other {}\n",
		"src/e.rs": "use crate::b::Named;\npub trait Loud: Named {}\n#[derive(Named)]\npub struct Z;\n",
		"src/f.rs": "pub trait Solo {}\n",
	}
}

// RS-2: an impl's target type and trait resolve through the file's imports, and the
// result is the same on every scan. Before the fix the first by-name hit in map order
// won, so only_on_b landed on a::Thing most of the time.
func TestImplResolvesThroughImports(t *testing.T) {
	dir := writeCrate(t, "", rs2Files())
	for i := 0; i < 10; i++ {
		topo := scan(t, dir)
		if !hasRes(topo, "unit::b::Thing::only_on_b") || hasRes(topo, "unit::a::Thing::only_on_b") {
			t.Fatalf("scan %d: only_on_b must attach to b::Thing (the imported one)", i)
		}
		if !exactConn(topo, "unit::b::Thing", "implements", "unit::b::Named") {
			t.Fatalf("scan %d: b::Thing should implement b::Named", i)
		}
		if exactConn(topo, "unit::a::Thing", "implements", "unit::a::Named") ||
			exactConn(topo, "unit::a::Thing", "implements", "unit::b::Named") {
			t.Fatalf("scan %d: a::Thing implements nothing", i)
		}
		if !exactConn(topo, "unit::c::user", "calls", "unit::b::Thing::only_on_b") {
			t.Fatalf("scan %d: user should call b::Thing::only_on_b via make()'s imported return type", i)
		}
		if hasRes(topo, "::orphan") {
			t.Fatalf("scan %d: an impl on an ambiguous, unimported name must stay unresolved", i)
		}
		if !exactConn(topo, "unit::d::Other", "implements", "unit::f::Solo") {
			t.Fatalf("scan %d: a unique trait name still resolves by name", i)
		}
		// Derives and supertraits resolve in the declaring file's scope too.
		if !exactConn(topo, "unit::e::Loud", "inherits", "unit::b::Named") || exactConn(topo, "unit::e::Loud", "inherits", "unit::a::Named") {
			t.Fatalf("scan %d: Loud: Named should inherit only the imported b::Named", i)
		}
		if !exactConn(topo, "unit::e::Z", "implements", "unit::b::Named") || exactConn(topo, "unit::e::Z", "implements", "unit::a::Named") {
			t.Fatalf("scan %d: #[derive(Named)] should implement only the imported b::Named", i)
		}
	}
}

// RS-2: the whole-graph passes (derives, supertraits) run on every incremental update,
// including ones that never re-parse the declaring file. They resolve through that
// file's persisted use bindings, so an update elsewhere keeps the same edges, and the
// incremental graph equals a fresh full scan.
func TestImportResolutionSurvivesIncrementalUpdate(t *testing.T) {
	dir := writeCrate(t, "", rs2Files())
	topo := scan(t, dir)
	path := filepath.Join(dir, "src/a.rs")
	if err := os.WriteFile(path, []byte("pub struct Thing;\npub trait Named { fn name(&self) -> String; }\npub fn more() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRustScanner().UpdateFile(topo, path); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if !exactConn(topo, "unit::e::Loud", "inherits", "unit::b::Named") || exactConn(topo, "unit::e::Loud", "inherits", "unit::a::Named") {
		t.Error("after an update to a.rs, Loud should still inherit only b::Named")
	}
	if !exactConn(topo, "unit::e::Z", "implements", "unit::b::Named") || exactConn(topo, "unit::e::Z", "implements", "unit::a::Named") {
		t.Error("after an update to a.rs, Z should still implement only b::Named")
	}
	full := scan(t, dir)
	inc, fresh := edgeSet(topo, dir), edgeSet(full, dir)
	if strings.Join(inc, "\n") != strings.Join(fresh, "\n") {
		t.Errorf("incremental edges differ from a full scan:\nincremental:\n%s\nfull:\n%s", strings.Join(inc, "\n"), strings.Join(fresh, "\n"))
	}
}

// RS-3: a module-qualified or glob-imported free-function call resolves, the return
// type types the local, and a glob `use` records imports_module.
func TestQualifiedAndGlobCalls(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs":    "pub mod factory;\npub mod shapes;\npub mod consumer;\npub mod consumer2;\npub mod util;\n",
		"src/shapes.rs": "pub struct Circle { pub r: f64 }\nimpl Circle {\n    pub fn area(&self) -> f64 { self.r }\n}\npub const UNIT: f64 = 1.0;\n",
		"src/factory.rs": "use crate::shapes::Circle;\npub fn make_circle(r: f64) -> Circle { Circle { r } }\n" +
			"pub fn make_default() -> Circle { make_circle(1.0) }\n",
		"src/consumer.rs": `use crate::factory;
use crate::shapes::{self, Circle as Disc};
pub fn qualified() -> f64 {
    let c = factory::make_circle(2.0);
    c.area()
}
pub fn fully_qualified() -> f64 {
    let c = crate::factory::make_circle(2.0);
    c.area()
}
pub fn via_self_alias() -> f64 {
    let d = Disc { r: 1.0 };
    d.area() + shapes::UNIT
}
pub fn external() -> u32 {
    std::cmp::max(1, 2)
}
`,
		"src/consumer2.rs":  "use crate::factory::*;\npub fn via_glob() -> f64 {\n    let c = make_circle(3.0);\n    c.area()\n}\n",
		"src/util/mod.rs":   "pub mod inner;\npub fn helper() -> u32 { 1 }\n",
		"src/util/inner.rs": "pub fn go() -> u32 {\n    super::helper()\n}\n",
	})
	topo := scan(t, dir)

	want := [][3]string{
		{"unit::consumer::qualified", "calls", "unit::factory::make_circle"},
		{"unit::consumer::qualified", "calls", "unit::shapes::Circle::area"},
		{"unit::consumer::fully_qualified", "calls", "unit::factory::make_circle"},
		{"unit::consumer::fully_qualified", "calls", "unit::shapes::Circle::area"},
		{"unit::consumer::via_self_alias", "uses_struct", "unit::shapes::Circle"},
		{"unit::consumer::via_self_alias", "calls", "unit::shapes::Circle::area"},
		{"unit::consumer::via_self_alias", "uses_extvar", "unit::shapes::UNIT"},
		{"unit::consumer2::via_glob", "calls", "unit::factory::make_circle"},
		{"unit::consumer2::via_glob", "calls", "unit::shapes::Circle::area"},
		{"unit::util::inner::go", "calls", "unit::util::helper"},
	}
	for _, w := range want {
		if !exactConn(topo, w[0], w[1], w[2]) {
			t.Errorf("%s should %s %s; has %v", w[0], w[1], w[2], topo.Resources[w[0]].Connections)
		}
	}
	if calls := topo.Resources["unit::consumer::external"].Connections["calls"]; len(calls) != 0 {
		t.Errorf("an external qualified call must not resolve internally: %v", calls)
	}
	if !connHas(topo, "consumer2.rs", "imports_module", "factory.rs") {
		t.Error("a glob `use crate::factory::*` should record imports_module factory.rs")
	}
}

// implMoveFiles is a crate whose `impl Shape for Foo` lives in its own file, apart from
// both the struct and the caller -- the shape a moved impl has to survive.
func implMoveFiles(implInB, implInD string) map[string]string {
	return map[string]string{
		"src/lib.rs": "pub mod a;\npub mod b;\npub mod c;\npub mod d;\n",
		"src/a.rs":   "pub trait Shape { fn area(&self) -> i32; }\npub struct Foo;\n",
		"src/b.rs":   implInB,
		"src/c.rs":   "use crate::a::Foo;\npub fn caller(f: &Foo) -> i32 { f.area() }\n",
		"src/d.rs":   implInD,
	}
}

// RS-01: an impl method's ID is `<Type>::<name>`, which does not name the file the impl
// was written in, so while an `impl` block is moved from one file to another both files
// own the same ID. Removing the file it left must not take the live copy in the file it
// moved to: nothing re-parses that file, so the method, its callers' edges and its trait
// obligation would all vanish until a `--hard` scan.
func TestImplMovedToAnotherFile(t *testing.T) {
	const implSrc = "use crate::a::{Foo, Shape};\nimpl Shape for Foo {\n    fn area(&self) -> i32 { 1 }\n}\n"
	dir := writeCrate(t, "", implMoveFiles(implSrc, "// no impl yet\n"))
	topo := scan(t, dir)
	if !hasRes(topo, "unit::a::Foo::area") {
		t.Fatal("setup: Foo::area should exist before the move")
	}

	s := NewRustScanner()
	// Step 1: the impl is copied into d.rs (both files now own `unit::a::Foo::area`).
	dPath := filepath.Join(dir, "src", "d.rs")
	if err := os.WriteFile(dPath, []byte(implSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateFile(topo, dPath); err != nil {
		t.Fatalf("UpdateFile(d.rs): %v", err)
	}
	// Step 2: it is deleted from b.rs, completing the move.
	bPath := filepath.Join(dir, "src", "b.rs")
	if err := os.WriteFile(bPath, []byte("// moved to d.rs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateFile(topo, bPath); err != nil {
		t.Fatalf("UpdateFile(b.rs): %v", err)
	}

	if !hasRes(topo, "unit::a::Foo::area") {
		t.Error("Foo::area must survive the move; the impl still exists, in d.rs")
	}
	if loc := topo.Resources["unit::a::Foo::area"].Location.Path; loc != dPath {
		t.Errorf("Foo::area located at %q, want %q", loc, dPath)
	}
	if !exactConn(topo, "unit::c::caller", "calls", "unit::a::Foo::area") {
		t.Error("caller should still call Foo::area after the move")
	}
	if !exactConn(topo, "unit::a::Foo", "implements", "unit::a::Shape") {
		t.Error("Foo should still implement Shape after the move")
	}

	// The incremental graph must equal a cold scan of the moved tree.
	full := scan(t, dir)
	if len(full.Resources) != len(topo.Resources) {
		t.Errorf("incremental resource count %d != full %d", len(topo.Resources), len(full.Resources))
	}
	inc, fresh := edgeSet(topo, dir), edgeSet(full, dir)
	if strings.Join(inc, "\n") != strings.Join(fresh, "\n") {
		t.Errorf("incremental edges differ from a full scan:\nincremental:\n%s\nfull:\n%s", strings.Join(inc, "\n"), strings.Join(fresh, "\n"))
	}
}

// RS-02a: the crate does not have to sit at the scan root. A repository whose Rust lives
// in a subdirectory (no root Cargo.toml at all) must still be rooted at that package's
// name, with directory modules intact -- not flattened into the nameless `crate` root
// with only the file stem for a module path.
func TestSubdirectoryCratePackage(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"README.md":         "not a crate\n",
		"rust/Cargo.toml":   "[package]\nname = \"sub\"\nversion = \"0.1.0\"\n",
		"rust/src/lib.rs":   "pub mod a;\npub fn root_fn() -> i32 { crate::a::b::leaf() }\n",
		"rust/src/a/mod.rs": "pub mod b;\n",
		"rust/src/a/b.rs":   "pub fn leaf() -> i32 { 1 }\n",
	})
	topo := scan(t, dir)

	for _, id := range []string{"sub::root_fn", "sub::a::b::leaf"} {
		if _, ok := topo.Resources[id]; !ok {
			t.Errorf("missing resource %s", id)
		}
	}
	for id := range topo.Resources {
		if strings.HasPrefix(id, "crate::") {
			t.Errorf("file under the `sub` package collapsed into the nameless crate root: %s", id)
		}
	}
	if !exactConn(topo, "sub::root_fn", "calls", "sub::a::b::leaf") {
		t.Error("sub::root_fn should call sub::a::b::leaf")
	}
}

// RS-02b: the Tauri layout -- a JS project at the root with the Rust package under
// src-tauri/. Two modules with the same file stem (net/util.rs and db/util.rs) must keep
// distinct IDs; flattening both to `crate::util` made one silently overwrite the other.
func TestSameStemModulesInSubdirectoryCrate(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"package.json":              "{\"name\": \"app\"}\n",
		"src/index.ts":              "export const x = 1;\n",
		"src-tauri/Cargo.toml":      "[package]\nname = \"app-core\"\nversion = \"0.1.0\"\n",
		"src-tauri/src/main.rs":     "mod net;\nmod db;\nfn main() { crate::net::util::shared(); crate::db::util::shared(); }\n",
		"src-tauri/src/net/mod.rs":  "pub mod util;\n",
		"src-tauri/src/net/util.rs": "pub fn shared() -> i32 { 1 }\n",
		"src-tauri/src/db/mod.rs":   "pub mod util;\n",
		"src-tauri/src/db/util.rs":  "pub fn shared() -> i32 { 2 }\n",
	})
	topo := scan(t, dir)

	for _, id := range []string{"app_core::net::util::shared", "app_core::db::util::shared", "app_core::main"} {
		if _, ok := topo.Resources[id]; !ok {
			t.Errorf("missing resource %s", id)
		}
	}
	if !exactConn(topo, "app_core::main", "calls", "app_core::net::util::shared") ||
		!exactConn(topo, "app_core::main", "calls", "app_core::db::util::shared") {
		t.Errorf("main should call both same-stem `shared` functions, got %v", topo.Resources["app_core::main"].Connections["calls"])
	}
}
