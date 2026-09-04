package rustscanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// writeCrate writes a Cargo.toml (package "unit") plus the given src files into a
// temp dir and returns the crate root. Keys in files are paths relative to the
// crate root (e.g. "src/shapes.rs").
func writeCrate(t *testing.T, deps string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	cargo := "[package]\nname = \"unit\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"
	if deps != "" {
		cargo += "\n[dependencies]\n" + deps
	}
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func scan(t *testing.T, dir string) *domain.Topology {
	t.Helper()
	topo, err := NewRustScanner().Scan(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return topo
}

// resBySuffix returns the unique resource whose ID ends with suffix.
func resBySuffix(t *testing.T, topo *domain.Topology, suffix string) domain.Resource {
	t.Helper()
	var found []domain.Resource
	for id, r := range topo.Resources {
		if strings.HasSuffix(id, suffix) {
			found = append(found, r)
		}
	}
	if len(found) == 0 {
		t.Fatalf("no resource with ID suffix %q", suffix)
	}
	if len(found) > 1 {
		ids := make([]string, len(found))
		for i, r := range found {
			ids[i] = r.ID
		}
		t.Fatalf("ambiguous suffix %q: %v", suffix, ids)
	}
	return found[0]
}

func hasRes(topo *domain.Topology, suffix string) bool {
	for id := range topo.Resources {
		if strings.HasSuffix(id, suffix) {
			return true
		}
	}
	return false
}

// connHas reports whether the resource with idSuffix has a `conn` edge to a
// target whose ID ends with targetSuffix.
func connHas(topo *domain.Topology, idSuffix, conn, targetSuffix string) bool {
	for id, r := range topo.Resources {
		if !strings.HasSuffix(id, idSuffix) {
			continue
		}
		for _, tgt := range r.Connections[conn] {
			if strings.HasSuffix(tgt, targetSuffix) {
				return true
			}
		}
	}
	return false
}

func TestScannerMetadata(t *testing.T) {
	s := NewRustScanner()
	if s.Name() != "rust" {
		t.Errorf("Name() = %q", s.Name())
	}
	if len(s.Extensions()) != 1 || s.Extensions()[0] != ".rs" {
		t.Errorf("Extensions() = %v", s.Extensions())
	}
}

func TestDetect(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{"src/lib.rs": "pub fn f() {}\n"})
	if !NewRustScanner().Detect(dir) {
		t.Error("Detect should be true for a crate with Cargo.toml")
	}
	bare := t.TempDir()
	if NewRustScanner().Detect(bare) {
		t.Error("Detect should be false for an empty dir")
	}
	rsOnly := t.TempDir()
	os.WriteFile(filepath.Join(rsOnly, "main.rs"), []byte("fn main() {}\n"), 0o644)
	if !NewRustScanner().Detect(rsOnly) {
		t.Error("Detect should be true when a *.rs file is present")
	}
}

func TestResourceKinds(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs": "pub mod shapes;\n",
		"src/shapes.rs": `pub trait Shape { fn area(&self) -> f64; }
pub struct Circle { pub r: f64 }
pub enum Geometry { Round(Circle), Empty }
pub type Pair = (f64, f64);
pub const PI: f64 = 3.14;
pub fn free() -> f64 { 1.0 }
impl Circle {
    pub fn new(r: f64) -> Self { Circle { r } }
    pub fn area(&self) -> f64 { self.r }
}
`,
	})
	topo := scan(t, dir)

	if k := resBySuffix(t, topo, "::shapes::Shape").Kind; k != domain.ResourceInterface {
		t.Errorf("trait kind = %v, want interface", k)
	}
	if k := resBySuffix(t, topo, "::shapes::Circle").Kind; k != domain.ResourceStruct {
		t.Errorf("struct kind = %v, want struct", k)
	}
	geo := resBySuffix(t, topo, "::shapes::Geometry")
	if geo.Kind != domain.ResourceStruct {
		t.Errorf("enum kind = %v, want struct", geo.Kind)
	}
	if ie, _ := geo.Properties["is_enum"].(bool); !ie {
		t.Errorf("enum should have is_enum=true: %v", geo.Properties)
	}
	if k := resBySuffix(t, topo, "::shapes::Pair").Kind; k != domain.ResourceNamedType {
		t.Errorf("type alias kind = %v, want named_type", k)
	}
	if k := resBySuffix(t, topo, "::shapes::PI").Kind; k != domain.ResourceVariable {
		t.Errorf("const kind = %v, want variable", k)
	}
	if k := resBySuffix(t, topo, "::shapes::free").Kind; k != domain.ResourceFunction {
		t.Errorf("free fn kind = %v, want function", k)
	}
	if k := resBySuffix(t, topo, "::shapes::Circle::area").Kind; k != domain.ResourceMethod {
		t.Errorf("method kind = %v, want method", k)
	}
	// Constructor detection (stored as a property) + has_method.
	if ctor, _ := resBySuffix(t, topo, "::shapes::Circle").Properties["constructor"].(string); !strings.HasSuffix(ctor, "::Circle::new") {
		t.Errorf("Circle constructor property = %q, want ...::Circle::new", ctor)
	}
	if !connHas(topo, "::shapes::Circle", "methods", "::Circle::area") {
		t.Error("Circle should have_method Circle::area")
	}
}

func TestCrossFileImplAndTrait(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs":    "pub mod shapes;\npub mod ext;\n",
		"src/shapes.rs": "pub trait Shape { fn area(&self) -> f64; }\npub struct Circle { pub r: f64 }\n",
		// impl lives in a DIFFERENT file than the struct.
		"src/ext.rs": `use crate::shapes::{Shape, Circle};
impl Shape for Circle {
    fn area(&self) -> f64 { self.r * self.r }
}
impl Circle {
    pub fn scaled(&self) -> f64 { self.r * 2.0 }
}
`,
	})
	topo := scan(t, dir)

	// Cross-file method attaches to the struct's stable ID.
	if !hasRes(topo, "::shapes::Circle::area") {
		t.Error("cross-file impl method Circle::area should exist")
	}
	if !connHas(topo, "::shapes::Circle", "methods", "::shapes::Circle::area") {
		t.Error("Circle.has_method should include the cross-file area")
	}
	if !connHas(topo, "::shapes::Circle", "methods", "::shapes::Circle::scaled") {
		t.Error("Circle.has_method should include the cross-file scaled")
	}
	// impl Trait for Type → implements / implemented_by, even cross-file.
	if !connHas(topo, "::shapes::Circle", "implements", "::shapes::Shape") {
		t.Error("Circle should implement Shape (cross-file impl)")
	}
	if !connHas(topo, "::shapes::Shape", "implemented_by", "::shapes::Circle") {
		t.Error("Shape should be implemented_by Circle")
	}
}

func TestUseImportsAndDependency(t *testing.T) {
	dir := writeCrate(t, "serde = \"1.0\"\n", map[string]string{
		"src/lib.rs":    "pub mod shapes;\npub mod consumer;\n",
		"src/shapes.rs": "pub struct Circle { pub r: f64 }\n",
		"src/consumer.rs": `use crate::shapes::Circle;
use serde::Serialize;
pub fn make() -> Circle { Circle { r: 1.0 } }
`,
	})
	topo := scan(t, dir)

	// Internal use → imports_module (file → file).
	if !connHas(topo, "consumer.rs", "imports_module", "shapes.rs") {
		t.Error("consumer.rs should import_module shapes.rs")
	}
	// External crate use → imports_dependency on the module + a dependency node.
	if !hasRes(topo, "serde") {
		t.Error("serde dependency node should exist")
	}
	if !connHas(topo, "consumer.rs", "imports_dependency", "serde") {
		t.Error("consumer.rs should imports_dependency serde")
	}
	// No false internal edge for the external crate.
	if connHas(topo, "consumer.rs", "imports_module", "serde") {
		t.Error("serde must not be an internal imports_module edge")
	}
}

func TestConstructorReturnInference(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs":    "pub mod shapes;\npub mod consumer;\n",
		"src/shapes.rs": "pub struct Circle { pub r: f64 }\nimpl Circle { pub fn new(r: f64) -> Self { Circle { r } } pub fn area(&self) -> f64 { self.r } }\n",
		"src/consumer.rs": `use crate::shapes::Circle;
pub fn report() -> f64 {
    let c = Circle::new(2.0);
    c.area()
}
`,
	})
	topo := scan(t, dir)

	// report() calls the associated fn and the method resolved via Self return type.
	if !connHas(topo, "::consumer::report", "calls", "::shapes::Circle::new") {
		t.Error("report should call Circle::new")
	}
	if !connHas(topo, "::consumer::report", "calls", "::shapes::Circle::area") {
		t.Error("report should resolve c.area() via Self/constructor return inference")
	}
	if !connHas(topo, "::consumer::report", "uses_struct", "::shapes::Circle") {
		t.Error("report should use_struct Circle")
	}
}

func TestSkipTestCode(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs": `pub fn real() -> i32 { 1 }

#[cfg(test)]
mod tests {
    pub fn helper_should_be_skipped() -> i32 { 2 }
}

#[test]
fn a_test_fn_should_be_skipped() {}
`,
	})
	topo := scan(t, dir)
	if !hasRes(topo, "::real") {
		t.Error("real() should be indexed")
	}
	if hasRes(topo, "helper_should_be_skipped") {
		t.Error("#[cfg(test)] module contents should be skipped")
	}
	if hasRes(topo, "a_test_fn_should_be_skipped") {
		t.Error("#[test] functions should be skipped")
	}
}

func TestVisibility(t *testing.T) {
	dir := writeCrate(t, "", map[string]string{
		"src/lib.rs": "pub fn exported() {}\nfn private() {}\npub(crate) fn cratevis() {}\n",
	})
	topo := scan(t, dir)
	if v, _ := resBySuffix(t, topo, "::exported").Properties["visibility"].(string); v != "public" {
		t.Errorf("exported visibility = %q, want public", v)
	}
	if ex, _ := resBySuffix(t, topo, "::exported").Properties["exported"].(bool); !ex {
		t.Error("exported fn should have exported=true")
	}
	if v, _ := resBySuffix(t, topo, "::private").Properties["visibility"].(string); v != "private" {
		t.Errorf("private visibility = %q, want private", v)
	}
	if v, _ := resBySuffix(t, topo, "::cratevis").Properties["visibility"].(string); v != "crate" {
		t.Errorf("pub(crate) visibility = %q, want crate", v)
	}
}

// Incremental UpdateFile must keep the graph current, preserve descriptions, and
// emit a warning when a signature changes; and match a fresh full scan.
func TestUpdateFileIncremental(t *testing.T) {
	files := map[string]string{
		"src/lib.rs":      "pub mod shapes;\npub mod consumer;\n",
		"src/shapes.rs":   "pub struct Circle { pub r: f64 }\nimpl Circle { pub fn new(r: f64) -> Self { Circle { r } } pub fn area(&self) -> f64 { self.r } }\n",
		"src/consumer.rs": "use crate::shapes::Circle;\npub fn report() -> f64 { let c = Circle::new(1.0); c.area() }\n",
	}
	dir := writeCrate(t, "", files)
	topo := scan(t, dir)

	// Edit shapes.rs: add a new method and change area's signature.
	newShapes := "pub struct Circle { pub r: f64 }\nimpl Circle { pub fn new(r: f64) -> Self { Circle { r } } pub fn area(&self, scale: f64) -> f64 { self.r * scale } pub fn extra(&self) -> f64 { 0.0 } }\n"
	path := filepath.Join(dir, "src/shapes.rs")
	if err := os.WriteFile(path, []byte(newShapes), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings, err := NewRustScanner().UpdateFile(topo, path)
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if !hasRes(topo, "::shapes::Circle::extra") {
		t.Error("new method extra should be present after UpdateFile")
	}
	// A signature change should surface at least one warning.
	if len(warnings) == 0 {
		t.Error("expected a signature-change warning for Circle::area")
	}

	// Incremental result must match a fresh full scan of the edited tree.
	full := scan(t, dir)
	if len(full.Resources) != len(topo.Resources) {
		t.Errorf("incremental resource count %d != full %d", len(topo.Resources), len(full.Resources))
	}
	for id, fr := range full.Resources {
		ir, ok := topo.Resources[id]
		if !ok {
			t.Errorf("resource %s missing after incremental", id)
			continue
		}
		if ir.Kind != fr.Kind {
			t.Errorf("resource %s kind incremental=%v full=%v", id, ir.Kind, fr.Kind)
		}
	}
}
