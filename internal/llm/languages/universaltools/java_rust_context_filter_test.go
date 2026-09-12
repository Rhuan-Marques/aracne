package universaltools_test

// RD-9, Java and Rust half: read.context_filter decides per neighbour whether a read's
// "# CONTEXT:" block hides it, names it ("id: description") or shows its source. The Go, Python
// and JS managers applied it; the Java and Rust ones did not, so under "normal" every undescribed
// member printed as "## id: no description" and "full" showed no source cuts and no
// "# USED BY:". The relationship lines -- constructor, extends/implements, supertypes,
// implementors, enum variants, record components -- are structure and render regardless, as a
// Python base class or a Go struct's interfaces do.

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/lazydesc"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/java"
	"github.com/Rhuan-Marques/aracne/internal/topology/rust"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/javascanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/rustscanner"
)

var ctxJavaFiles = map[string]string{
	"pom.xml":                            "<project></project>\n",
	"src/main/java/com/ex/Labelled.java": "package com.ex;\n\ninterface Labelled {\n    String label();\n}\n",
	"src/main/java/com/ex/Named.java":    "package com.ex;\n\ninterface Named extends Labelled {\n    String name();\n}\n",
	"src/main/java/com/ex/Marker.java":   "package com.ex;\n\n@interface Marker {\n}\n",
	"src/main/java/com/ex/Base.java":     "package com.ex;\n\nclass Base {\n    int base() { return 0; }\n}\n",
	"src/main/java/com/ex/Helper.java": "package com.ex;\n\nclass Helper {\n" +
		"    int small() { return 1; }\n\n" +
		"    int big() {\n        int x = 1;\n        x++;\n        x++;\n        x++;\n        return x;\n    }\n}\n",
	"src/main/java/com/ex/Thing.java": "package com.ex;\n\npublic class Thing extends Base implements Named {\n" +
		"    Helper h;\n\n" +
		"    public Thing() { h = new Helper(); }\n\n" +
		"    public String name() { return \"t\"; }\n\n" +
		"    public String label() { return \"l\"; }\n\n" +
		"    @Marker\n" +
		"    int run(Named n) {\n        Helper h2 = new Helper();\n        return h2.small() + h2.big() + tiny() + n.name().length();\n    }\n\n" +
		"    static int tiny() { return 1; }\n}\n",
	"src/main/java/com/ex/Op.java":    "package com.ex;\n\nenum Op {\n    PLUS, MINUS;\n\n    int sign() { return 1; }\n}\n",
	"src/main/java/com/ex/Point.java": "package com.ex;\n\nrecord Point(int x, int y) {\n}\n",
}

var ctxRustFiles = map[string]string{
	"Cargo.toml": "[package]\nname = \"refcrate\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
	"src/lib.rs": `pub const LIMIT: i32 = 3;

pub trait Base {
    fn base(&self) -> i32;
}

pub trait Named: Base {
    fn name(&self) -> String;
}

pub enum Shape {
    Round,
    Rect,
}

pub struct Helper {
    pub n: i32,
}

impl Helper {
    pub fn small(&self) -> i32 { self.n }

    pub fn big(&self) -> i32 {
        let mut x = self.n;
        x += 1;
        x += 1;
        x += 1;
        x
    }
}

pub struct Thing {
    pub h: Helper,
}

impl Thing {
    pub fn new() -> Thing {
        Thing { h: Helper { n: 1 } }
    }

    pub fn run(&self) -> i32 {
        let h = Helper { n: 2 };
        h.small() + h.big() + tiny() + LIMIT
    }
}

impl Base for Thing {
    fn base(&self) -> i32 { 0 }
}

impl Named for Thing {
    fn name(&self) -> String { "t".into() }
}

pub fn tiny() -> i32 { 1 }
`,
}

// scanJavaRust writes files into a fresh project and full-scans it with the Java and Rust
// scanners.
func scanJavaRust(t *testing.T, files map[string]string) (*topology.TopologyManager, *scanner.Registry) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return scanDir(t, dir)
}

// scanDir full-scans an existing project directory into a database under it.
func scanDir(t *testing.T, dir string) (*topology.TopologyManager, *scanner.Registry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".aracne"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(filepath.Join(dir, ".aracne", "topology.db")); err != nil {
		t.Fatal(err)
	}
	reg := scanner.NewRegistry()
	reg.Register(javascanner.NewJavaScanner())
	reg.Register(rustscanner.NewRustScanner())
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	return mgr, reg
}

func describe(t *testing.T, mgr *topology.TopologyManager, kind domain.ResourceKind, id, text string) {
	t.Helper()
	if err := helper.UpdateDescription(mgr.DbPath(), kind, id, text); err != nil {
		t.Fatalf("describe %s: %v", id, err)
	}
}

func readWith(t *testing.T, mgr *topology.TopologyManager, reg *scanner.Registry, filter string, ids ...string) string {
	t.Helper()
	out, err := universaltools.NewRead(mgr, contextConfig(filter), false, reg).
		ReadIDs(ids, universaltools.ReadIDsOptions{Kinds: helper.AllReadKinds()})
	if err != nil {
		t.Fatalf("read %v: %v", ids, err)
	}
	if strings.Contains(out, "# UNRESOLVED") {
		t.Fatalf("fixture drifted: %v did not resolve:\n%s", ids, out)
	}
	return out
}

func wantIn(t *testing.T, out string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(out, n) {
			t.Errorf("want %q in:\n%s", n, out)
		}
	}
}

func wantNotIn(t *testing.T, out string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if strings.Contains(out, n) {
			t.Errorf("do not want %q in:\n%s", n, out)
		}
	}
}

// normal: an undescribed neighbour is omitted, a described one is named with its description.
func TestJavaReadNormalHidesUndescribedNeighbours(t *testing.T) {
	mgr, reg := scanJavaRust(t, ctxJavaFiles)

	class := readWith(t, mgr, reg, "normal", "com.ex.Thing")
	wantNotIn(t, class, "com.ex.Thing.name(): no description", "com.ex.Thing.run(Named): no description",
		"com.ex.Thing.tiny(): no description", "com.ex.Helper: no description")
	fn := readWith(t, mgr, reg, "normal", "com.ex.Thing.run(Named)")
	wantNotIn(t, fn, "# CONTEXT:")

	describe(t, mgr, domain.ResourceMethod, "com.ex.Thing.name()", "Names the thing.")
	describe(t, mgr, domain.ResourceStruct, "com.ex.Helper", "Does the arithmetic.")
	describe(t, mgr, domain.ResourceMethod, "com.ex.Helper.big()", "Counts up to five.")
	describe(t, mgr, domain.ResourceInterface, "com.ex.Marker", "Marks a method.")

	class = readWith(t, mgr, reg, "normal", "com.ex.Thing")
	wantIn(t, class, "## com.ex.Thing.name(): Names the thing.\n", "## com.ex.Helper: Does the arithmetic.\n",
		"\tcom.ex.Helper.big(): Counts up to five.\n")
	wantNotIn(t, class, "com.ex.Thing.run(Named)", "com.ex.Thing.tiny()", "com.ex.Helper.small()")

	fn = readWith(t, mgr, reg, "normal", "com.ex.Thing.run(Named)")
	wantIn(t, fn, "## com.ex.Helper: Does the arithmetic.\n", "\tcom.ex.Helper.big(): Counts up to five.\n",
		"## com.ex.Marker (interface): Marks a method.\n")
	wantNotIn(t, fn, "no description")
}

// full: small members render as source cuts, and undescribed neighbours are kept.
func TestJavaReadFullShowsSourceCuts(t *testing.T) {
	mgr, reg := scanJavaRust(t, ctxJavaFiles)

	fn := readWith(t, mgr, reg, "full", "com.ex.Thing.run(Named)")
	wantIn(t, fn,
		"## com.ex.Helper.small(): no description\n```java\n    int small() { return 1; }\n```\n",
		"## com.ex.Thing.tiny(): no description\n```java\n    static int tiny() { return 1; }\n```\n",
		// Over the small-function threshold: named, and kept although undescribed.
		"\tcom.ex.Helper.big(): no description\n",
		"## com.ex.Marker (interface): no description\n")

	class := readWith(t, mgr, reg, "full", "com.ex.Thing")
	wantIn(t, class, "## com.ex.Thing.name(): no description\n```java\n    public String name() { return \"t\"; }\n```\n")
}

// include_incoming ("full") adds "# USED BY:", which Java reads never had; "normal" does not.
func TestJavaReadIncludeIncoming(t *testing.T) {
	mgr, reg := scanJavaRust(t, ctxJavaFiles)

	wantIn(t, readWith(t, mgr, reg, "full", "com.ex.Helper"),
		"# USED BY:\n", "## com.ex.Thing.run(Named) (method): no description\n")
	wantIn(t, readWith(t, mgr, reg, "full", "com.ex.Helper.small()"),
		"# USED BY:\n## com.ex.Thing.run(Named) (method): no description\n")
	wantIn(t, readWith(t, mgr, reg, "full", "com.ex.Marker"),
		"# USED BY:\n## com.ex.Thing.run(Named) (method): no description\n")
	wantNotIn(t, readWith(t, mgr, reg, "normal", "com.ex.Helper", "com.ex.Marker"), "# USED BY:")
}

// The relationship lines are structure the body does not show, and render undescribed under
// every filter that renders context at all.
func TestJavaRelationshipLinesSurviveTheFilter(t *testing.T) {
	mgr, reg := scanJavaRust(t, ctxJavaFiles)
	for _, filter := range []string{"normal", "full"} {
		wantIn(t, readWith(t, mgr, reg, filter, "com.ex.Thing"),
			"## com.ex.Thing.<init>() (constructor): no description\n",
			"## com.ex.Base (extends): no description\n",
			"## com.ex.Named (implements): no description\n")
		wantIn(t, readWith(t, mgr, reg, filter, "com.ex.Named"),
			"## com.ex.Labelled (supertype): no description\n",
			"## Implemented By\n\tcom.ex.Thing: no description\n")
		wantIn(t, readWith(t, mgr, reg, filter, "com.ex.Op"), "## variants: PLUS, MINUS\n")
		wantIn(t, readWith(t, mgr, reg, filter, "com.ex.Point"), "## components: int x, int y\n")
	}
}

func TestRustReadNormalHidesUndescribedNeighbours(t *testing.T) {
	mgr, reg := scanJavaRust(t, ctxRustFiles)

	st := readWith(t, mgr, reg, "normal", "refcrate::Thing")
	wantNotIn(t, st, "refcrate::Thing::run: no description", "refcrate::Thing::name: no description",
		"refcrate::Helper: no description")
	fn := readWith(t, mgr, reg, "normal", "refcrate::Thing::run")
	wantNotIn(t, fn, "refcrate::Helper", "refcrate::tiny")
	// A constant whose value is shown counts as described, as it does for Go.
	wantIn(t, fn, "## refcrate::LIMIT = \"3\": no description\n")

	describe(t, mgr, domain.ResourceMethod, "refcrate::Thing::run", "Runs the thing.")
	describe(t, mgr, domain.ResourceFunction, "refcrate::tiny", "Returns one.")
	describe(t, mgr, domain.ResourceMethod, "refcrate::Helper::big", "Counts up to five.")

	st = readWith(t, mgr, reg, "normal", "refcrate::Thing")
	wantIn(t, st, "## refcrate::Thing::run: Runs the thing.\n")
	wantNotIn(t, st, "refcrate::Thing::name")

	fn = readWith(t, mgr, reg, "normal", "refcrate::Thing::run")
	wantIn(t, fn, "## refcrate::tiny: Returns one.\n",
		// The type itself is undescribed, but a described method keeps it on the list.
		"## refcrate::Helper: no description\n\trefcrate::Helper::big: Counts up to five.\n")
	wantNotIn(t, fn, "refcrate::Helper::small")
}

func TestRustReadFullShowsSourceCuts(t *testing.T) {
	mgr, reg := scanJavaRust(t, ctxRustFiles)

	fn := readWith(t, mgr, reg, "full", "refcrate::Thing::run")
	wantIn(t, fn,
		"## refcrate::tiny: no description\n```rust\npub fn tiny() -> i32 { 1 }\n```\n",
		"    pub fn small(&self) -> i32 { self.n }\n```\n",
		"\trefcrate::Helper::big: no description\n",
		"## refcrate::LIMIT: no description\n```rust\npub const LIMIT: i32 = 3;\n```\n")

	st := readWith(t, mgr, reg, "full", "refcrate::Thing")
	wantIn(t, st, "## refcrate::Thing::run: no description\n```rust\n")
}

func TestRustReadIncludeIncoming(t *testing.T) {
	mgr, reg := scanJavaRust(t, ctxRustFiles)

	wantIn(t, readWith(t, mgr, reg, "full", "refcrate::Helper"),
		"# USED BY:\n", "## refcrate::Thing::run (method): no description\n")
	wantIn(t, readWith(t, mgr, reg, "full", "refcrate::tiny"),
		"# USED BY:\n## refcrate::Thing::run (method): no description\n")
	wantNotIn(t, readWith(t, mgr, reg, "normal", "refcrate::Helper", "refcrate::tiny"), "# USED BY:")
}

func TestRustRelationshipLinesSurviveTheFilter(t *testing.T) {
	mgr, reg := scanJavaRust(t, ctxRustFiles)
	for _, filter := range []string{"normal", "full"} {
		wantIn(t, readWith(t, mgr, reg, filter, "refcrate::Thing"),
			"## refcrate::Thing::new (constructor): no description\n",
			"## refcrate::Named (implements): no description\n",
			"## refcrate::Base (implements): no description\n")
		wantIn(t, readWith(t, mgr, reg, filter, "refcrate::Named"),
			"## refcrate::Base (supertrait): no description\n",
			"## refcrate::Thing (implements): no description\n")
		wantIn(t, readWith(t, mgr, reg, filter, "refcrate::Shape"), "## variants: Round, Rect\n")
	}
}

// copyCorpus copies a testing_ground corpus into a temp dir: a scan writes its database and
// manifests under the root, and testing_ground must never be written to.
func copyCorpus(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("..", "..", "..", "..", "testing_ground", name)
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// neighbourIDs is every top-level neighbour a Java or Rust read context lists, by list.
func neighbourIDs(t *testing.T, mgr *topology.TopologyManager, res domain.Resource, f domain.ContextFilter) map[string]bool {
	t.Helper()
	opt := topology.WithContextFilter(f)
	out := map[string]bool{}
	add := func(ids ...string) {
		for _, id := range ids {
			out[id] = true
		}
	}
	var err error
	switch {
	case res.Language == "java" && (res.Kind == domain.ResourceFunction || res.Kind == domain.ResourceMethod):
		var c *java.JavaFunctionContext
		if c, err = java.NewJavaManager(mgr).ReadFunction(res.ID, opt); err == nil {
			for _, x := range c.CalledFunctions {
				add(x.ID)
			}
			for _, x := range c.StructsUsed {
				add(x.ID)
			}
			for _, x := range c.InterfacesUsed {
				add(x.ID)
			}
		}
	case res.Language == "java" && res.Kind == domain.ResourceStruct:
		var c *java.JavaStructContext
		if c, err = java.NewJavaManager(mgr).ReadStruct(res.ID, opt); err == nil {
			for _, x := range c.Methods {
				add(x.ID)
			}
			for _, x := range c.StructsUsed {
				add(x.ID)
			}
		}
	case res.Language == "rust" && (res.Kind == domain.ResourceFunction || res.Kind == domain.ResourceMethod):
		var c *rust.RustFunctionContext
		if c, err = rust.NewRustManager(mgr).ReadFunction(res.ID, opt); err == nil {
			for _, x := range c.CalledFunctions {
				add(x.ID)
			}
			for _, x := range c.StructsUsed {
				add(x.ID)
			}
			for _, x := range c.TraitsUsed {
				add(x.ID)
			}
			for _, x := range c.NamedTypesUsed {
				add(x.ID)
			}
			for _, x := range c.VarsUsed {
				add(x.ID)
			}
		}
	case res.Language == "rust" && res.Kind == domain.ResourceStruct:
		var c *rust.RustStructContext
		if c, err = rust.NewRustManager(mgr).ReadStruct(res.ID, opt); err == nil {
			for _, x := range c.Methods {
				add(x.ID)
			}
			for _, x := range c.StructsUsed {
				add(x.ID)
			}
			for _, x := range c.NamedTypesUsed {
				add(x.ID)
			}
		}
	}
	if err != nil {
		t.Fatalf("read %s: %v", res.ID, err)
	}
	return out
}

// The lazy planner describes what a read is about to name. A neighbour the Java/Rust renderer
// now hides until it is described must therefore be one the planner would describe, or it could
// never appear. Checked over every function, method and class of both corpora, undescribed.
func TestJavaRustHiddenNeighboursAreOnesTheLazyPlannerDescribes(t *testing.T) {
	cfg := helper.DefaultConfig()
	filter := cfg.EffectiveContextFilter()
	keepAll := filter
	keepAll.HideNoDescription = false
	plan := lazydesc.PlanOptions{Targets: cfg.Descriptions.Kinds, Filter: filter,
		IncludeNotVisible: cfg.Descriptions.IncludeNotVisible}

	for _, corpus := range []string{"javafamily", "rustfamily"} {
		t.Run(corpus, func(t *testing.T) {
			mgr, _ := scanDir(t, copyCorpus(t, corpus))
			topo, err := mgr.ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(topo.Resources))
			for id := range topo.Resources {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			checked := 0
			for _, id := range ids {
				res := topo.Resources[id]
				all := neighbourIDs(t, mgr, res, keepAll)
				shown := neighbourIDs(t, mgr, res, filter)
				planned := map[string]bool{}
				for _, target := range lazydesc.ReadTargets(topo, []string{id}, plan) {
					planned[target.ID] = true
				}
				for n := range all {
					if shown[n] {
						continue
					}
					checked++
					if !planned[n] {
						t.Errorf("reading %s hides undescribed %s, but the lazy planner would never describe it", id, n)
					}
				}
			}
			if checked == 0 {
				t.Fatalf("fixture drifted: no hidden neighbour in %s to check", corpus)
			}
		})
	}
}
