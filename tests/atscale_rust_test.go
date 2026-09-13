package tests_test

// At-scale topology-consistency scenarios for the Rust scanner. Reuses the
// shared harness (runScenarioWith / assertSameGraph3 / assert* helpers from
// atscale_harness_test.go) but over an ISOLATED copy of testing_ground/rustfamily.
//
// Rust resource IDs are rooted at the Cargo `[package].name` ("rustfamily"), so
// the crate is scanned with its own dir as the root (Cargo.toml at root) and the
// IDs stay stable regardless of the temp location — unlike Go/JS/Python whose IDs
// derive from the scan-root leaf. Scenarios mutate src/*.rs and assert the graph
// is correct AND identical across incremental / full / hard scans.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Stable resource IDs (rooted at the crate package name "rustfamily").
const (
	rsShape         = "rustfamily::shapes::Shape"
	rsCircle        = "rustfamily::shapes::Circle"
	rsCircleNew     = "rustfamily::shapes::Circle::new"
	rsCircleArea    = "rustfamily::shapes::Circle::area"
	rsCircleDiam    = "rustfamily::shapes::Circle::diameter"
	rsOrigin        = "rustfamily::shapes::Origin"
	rsReport        = "rustfamily::consumer::report"
	rsGeometry      = "rustfamily::geometry::Geometry"
	rsMakeCircle    = "rustfamily::factory::make_circle"
	rsDrawable      = "rustfamily::traits::Drawable"
	connImportsMod  = "imports_module"
	connConstructor = "constructor"
)

// copyRustCorpus copies <repo>/testing_ground/rustfamily into a fresh temp dir as
// <tmp>/rustfamily (the crate dir, with Cargo.toml at its root) and returns it as
// the scan root. Build/cache dirs are skipped.
func copyRustCorpus(t *testing.T) string {
	t.Helper()
	src := filepath.Join(projectRoot(), "testing_ground", "rustfamily")
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("rustfamily corpus not found at %s: %v", src, err)
	}
	root := filepath.Join(t.TempDir(), "rustfamily")
	skip := map[string]bool{".aracne": true, "target": true, ".git": true}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return os.MkdirAll(root, 0o755)
		}
		for _, seg := range strings.Split(rel, string(os.PathSeparator)) {
			if skip[seg] {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		target := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy rust corpus: %v", err)
	}
	return root
}

// rustFile resolves a path relative to the copied crate root (e.g. "src/shapes.rs").
func rustFile(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

func writeRustFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := rustFile(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	writeFile(t, p, content)
	touchFuture(t, p)
}

// replaceInRustFile applies literal old->new replacements and bumps mtime. Each
// anchor must exist, so a drifted corpus is caught loudly.
func replaceInRustFile(t *testing.T, root, rel string, pairs ...[2]string) {
	t.Helper()
	p := rustFile(root, rel)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	s := string(data)
	for _, pr := range pairs {
		if !strings.Contains(s, pr[0]) {
			t.Fatalf("replaceInRustFile %s: anchor not found: %q", rel, pr[0])
		}
		s = strings.ReplaceAll(s, pr[0], pr[1])
	}
	writeFile(t, p, s)
	touchFuture(t, p)
}

const originImpl = "use crate::shapes::{Shape, Origin};\n\nimpl Shape for Origin {\n    fn area(&self) -> f64 {\n        0.0\n    }\n}\n"

// addExtraModule (setup/mutate helper) writes src/extra.rs with `impl Shape for
// Origin` and registers it in lib.rs.
func addExtraModule(t *testing.T, root string) {
	writeRustFile(t, root, "src/extra.rs", originImpl)
	replaceInRustFile(t, root, "src/lib.rs", [2]string{"pub mod redprobes;", "pub mod redprobes;\npub mod extra;"})
}

func TestAtScaleRust(t *testing.T) {
	scenarios := []scenario{
		{
			// No-op reparse: appending a comment to consumer.rs must not change
			// its resolved edges, and incremental must equal cold scans.
			name: "noop_reparse_keeps_flagship_edges",
			mutate: func(t *testing.T, root string) {
				replaceInRustFile(t, root, "src/consumer.rs",
					[2]string{"pub fn report() -> f64 {", "// touched\npub fn report() -> f64 {"})
			},
			assert: func(t *testing.T, topo *domain.Topology, mode string) {
				assertHasConn(t, topo, mode, rsReport, connCalls, rsCircleNew)
				assertHasConn(t, topo, mode, rsReport, connCalls, rsMakeCircle)
				assertHasConn(t, topo, mode, rsReport, connCalls, rsCircleArea)
				assertHasConn(t, topo, mode, rsReport, connUsesStruct, rsCircle)
			},
		},
		{
			// Rename a method used cross-file (consumer calls s.diameter()).
			name: "rename_method_across_files",
			mutate: func(t *testing.T, root string) {
				replaceInRustFile(t, root, "src/shapes.rs",
					[2]string{"pub fn diameter(&self) -> f64 {", "pub fn girth(&self) -> f64 {"})
				replaceInRustFile(t, root, "src/consumer.rs",
					[2]string{"s.diameter()", "s.girth()"})
			},
			assert: func(t *testing.T, topo *domain.Topology, mode string) {
				assertResAbsent(t, topo, mode, rsCircleDiam)
				assertResPresent(t, topo, mode, "rustfamily::shapes::Circle::girth")
				assertHasConn(t, topo, mode, rsCircle, connMethods, "rustfamily::shapes::Circle::girth")
				assertHasConn(t, topo, mode, rsReport, connCalls, "rustfamily::shapes::Circle::girth")
				assertNoReferences(t, topo, mode, rsCircleDiam)
			},
		},
		{
			// Add a new inherent method.
			name: "add_method",
			mutate: func(t *testing.T, root string) {
				replaceInRustFile(t, root, "src/shapes.rs",
					[2]string{"    pub fn diameter(&self) -> f64 {\n        self.radius * 2.0\n    }",
						"    pub fn diameter(&self) -> f64 {\n        self.radius * 2.0\n    }\n\n    pub fn scaled(&self) -> f64 {\n        self.radius * 3.0\n    }"})
			},
			assert: func(t *testing.T, topo *domain.Topology, mode string) {
				assertResPresent(t, topo, mode, "rustfamily::shapes::Circle::scaled")
				assertHasConn(t, topo, mode, rsCircle, connMethods, "rustfamily::shapes::Circle::scaled")
			},
		},
		{
			// Remove a SAME-FILE trait impl (impl Shape for Geometry, in geometry.rs).
			name: "remove_samefile_trait_impl",
			mutate: func(t *testing.T, root string) {
				replaceInRustFile(t, root, "src/geometry.rs",
					[2]string{"impl Shape for Geometry {\n    fn area(&self) -> f64 {\n        self.measure()\n    }\n\n    fn name(&self) -> &str {\n        \"geometry\"\n    }\n}\n", ""})
			},
			assert: func(t *testing.T, topo *domain.Topology, mode string) {
				assertNoConn(t, topo, mode, rsGeometry, connImplements, rsShape)
				assertNoConn(t, topo, mode, rsShape, connImplBy, rsGeometry)
				// Circle still implements Shape (untouched file).
				assertHasConn(t, topo, mode, rsShape, connImplBy, rsCircle)
			},
		},
		{
			// Add a CROSS-FILE trait impl (impl Shape for Origin, in a new file).
			name: "add_crossfile_trait_impl",
			mutate: func(t *testing.T, root string) {
				addExtraModule(t, root)
			},
			assert: func(t *testing.T, topo *domain.Topology, mode string) {
				assertHasConn(t, topo, mode, rsOrigin, connImplements, rsShape)
				assertHasConn(t, topo, mode, rsShape, connImplBy, rsOrigin)
				assertResPresent(t, topo, mode, "rustfamily::shapes::Origin::area")
			},
		},
		{
			// Remove a CROSS-FILE trait impl: the impl lives in extra.rs but its
			// target struct Origin lives in shapes.rs. The struct's stale
			// implements edge must be cleared even though shapes.rs wasn't
			// re-parsed (the incremental-correctness fix).
			name: "remove_crossfile_trait_impl",
			setup: func(t *testing.T, root string) {
				addExtraModule(t, root)
			},
			mutate: func(t *testing.T, root string) {
				replaceInRustFile(t, root, "src/extra.rs",
					[2]string{"impl Shape for Origin {\n    fn area(&self) -> f64 {\n        0.0\n    }\n}\n", "// impl removed\n"})
			},
			assert: func(t *testing.T, topo *domain.Topology, mode string) {
				assertNoConn(t, topo, mode, rsOrigin, connImplements, rsShape)
				assertNoConn(t, topo, mode, rsShape, connImplBy, rsOrigin)
				assertResAbsent(t, topo, mode, "rustfamily::shapes::Origin::area")
			},
		},
		{
			// Change an import: drop the make_circle import + its use, so report
			// no longer calls it. Tests imports_module/edge cleanup consistency.
			name: "remove_import_and_call",
			mutate: func(t *testing.T, root string) {
				replaceInRustFile(t, root, "src/consumer.rs",
					[2]string{"use crate::factory::make_circle;\n", ""},
					[2]string{"    let s = make_circle(1.0);\n    let _ = s.diameter();\n    a + s.area()", "    a + a"})
			},
			assert: func(t *testing.T, topo *domain.Topology, mode string) {
				assertNoConn(t, topo, mode, rsReport, connCalls, rsMakeCircle)
				// Still resolves the locally-built circle.
				assertHasConn(t, topo, mode, rsReport, connCalls, rsCircleNew)
			},
		},
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			runScenarioWith(t, sc, copyRustCorpus)
		})
	}
}
