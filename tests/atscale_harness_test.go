package tests_test

// At-scale topology-consistency harness.
//
// These tests copy the real testing_ground/ corpus into a temp dir, make a
// PREDICTABLE edit, and then assert the resulting topology is correct AND
// identical across all three scan modes:
//
//   incremental : `scan -default` after a baseline scan (IncrementalScan)
//   full        : a cold `scan -all`  of the mutated tree (FullReScan)
//   hard        : a cold `scan -hard` of the mutated tree (FullScan)
//
// A cold full and a cold hard scan of the same tree are authoritative and
// always agree; the mode that can drift is incremental. Per project decision
// the suite uses STRICT equality: if incremental diverges from the cold scans,
// the test fails (and the divergence is filed as a bug). Differences that are
// legitimately mode-specific are excluded from the comparison (see
// topoFingerprint).
//
// Helpers reused from the package: mustRun, writeFile, projectRoot
// (integration_test.go).

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

// ---------------------------------------------------------------------------
// Connection-kind / resource-ID constants used by the scenarios.
// ---------------------------------------------------------------------------

const (
	connCalls         = "calls"
	connUsesStruct    = "uses_struct"
	connImplBy        = "implemented_by"
	connImplements    = "implements"
	connUsesClass     = "uses_class"
	connInherits      = "inherits"
	connInheritedBy   = "inherited_by"
	connUsesInterface = "uses_interface"
	connUsesNamedType = "uses_named_type"
	connImportsDep    = "imports_dependency"
	connMethods       = "methods"
	connUsesExtvar    = "uses_extvar"
	connUsesPkg       = "uses_package"
)

// ---------------------------------------------------------------------------
// Corpus copy + mutation helpers.
// ---------------------------------------------------------------------------

// copyCorpus recursively copies <repo>/testing_ground into a fresh temp dir
// (as <tmp>/testing_ground) and drops a synthetic go.mod at <tmp> so the Go
// import paths `aracne/testing_ground/go/...` resolve exactly as in the repo.
// It returns the scan root (<tmp>). Build/cache dirs are skipped so the stale
// testing_ground/.aracne/topology.db is never copied.
func copyCorpus(t *testing.T) string {
	t.Helper()
	src := filepath.Join(projectRoot(), "testing_ground")
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("testing_ground corpus not found at %s: %v", src, err)
	}
	// The copy is rooted in a dir named "aracne" because the synthetic go.mod written
	// below declares `module aracne`, which is what pins the GO resource IDs.
	//
	// It used to be named that for a second reason — Python and JS/TS derived their ID
	// prefix from the scan-root leaf, so the directory name had to match the real repo's.
	// id-scheme 2 makes those module paths repo-relative, so that reason is gone; only the
	// Go module path still depends on this name.
	// The corpus is deliberately rooted UNDER A HIDDEN DIRECTORY. The ignore
	// rules used to be applied to the absolute path, so any checkout living
	// beneath a dot-named ancestor (~/.claude/scratch/app, a CI checkout under
	// /home/runner/.cache/...) indexed zero files, silently, and every later
	// edit treated its files as non-source and deleted their resources. Running
	// the whole at-scale matrix from here means all six languages and all three
	// scan modes stand guard against that.
	root := filepath.Join(t.TempDir(), ".dotroot", "aracne")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir corpus root: %v", err)
	}
	dstCorpus := filepath.Join(root, "testing_ground")

	skip := map[string]bool{".aracne": true, "node_modules": true, "__pycache__": true, ".git": true}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		for _, seg := range strings.Split(rel, string(os.PathSeparator)) {
			if skip[seg] {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		target := filepath.Join(dstCorpus, rel)
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
		t.Fatalf("copy corpus: %v", err)
	}
	writeFile(t, filepath.Join(root, "go.mod"), "module aracne\n\ngo 1.25\n")
	return root
}

// copyGoCorpus copies only <repo>/testing_ground/go into
// <tmp>/aracne/testing_ground/go and drops a synthetic go.mod, so a scan rooted
// at <tmp>/aracne yields the same "aracne/testing_ground/go/..." resource IDs as
// the full corpus — but WITHOUT the Python/JS/TS trees. The JS/TS scanner
// currently aborts the multi-language write (a pre-existing crash); scanning Go
// in isolation lets Go scenarios run regardless. Returns the scan root.
func copyGoCorpus(t *testing.T) string {
	t.Helper()
	src := filepath.Join(projectRoot(), "testing_ground", "go")
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("go corpus not found at %s: %v", src, err)
	}
	root := filepath.Join(t.TempDir(), "aracne")
	dst := filepath.Join(root, "testing_ground", "go")
	skip := map[string]bool{".aracne": true, ".git": true}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		for _, seg := range strings.Split(rel, string(os.PathSeparator)) {
			if skip[seg] {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy go corpus: %v", err)
	}
	writeFile(t, filepath.Join(root, "go.mod"), "module aracne\n\ngo 1.25\n")
	return root
}

// copyJavaCorpus copies only <repo>/testing_ground/javafamily (the Maven layout:
// pom.xml + src/main/java/**) into <tmp>/aracne/testing_ground/javafamily, so a
// scan rooted at <tmp>/aracne sees ONLY the Java tree and sidesteps the
// pre-existing multi-language scan-write abort. Java symbol IDs are FQN-based
// (derived from each file's `package` decl), so they are PATH-INDEPENDENT and
// identical regardless of the temp location — the hardcoded FQN IDs in the
// scenarios stay stable. No go.mod is written: Java needs none, and an empty
// go.mod would make the Go scanner falsely detect an (empty) project. The
// edgecase-only fixture dirs (redprobes/arrays — exercised by the in-memory
// java_edgecases suite) are skipped so the at-scale graph is exactly the stable
// original corpus. Returns the scan root (<tmp>/aracne).
func copyJavaCorpus(t *testing.T) string {
	t.Helper()
	src := filepath.Join(projectRoot(), "testing_ground", "javafamily")
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("java corpus not found at %s: %v", src, err)
	}
	root := filepath.Join(t.TempDir(), "aracne")
	dst := filepath.Join(root, "testing_ground", "javafamily")
	skip := map[string]bool{
		".aracne": true, ".git": true, "target": true, "build": true,
		"redprobes": true, "arrays": true,
	}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		for _, seg := range strings.Split(rel, string(os.PathSeparator)) {
			if skip[seg] {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy java corpus: %v", err)
	}
	return root
}

// corpusFile resolves a path relative to the copied testing_ground root.
func corpusFile(root, rel string) string {
	return filepath.Join(root, "testing_ground", filepath.FromSlash(rel))
}

// touchFuture bumps a file's mtime safely past the baseline manifest time so the
// incremental change-detector (strict ModTime().After) always fires.
func touchFuture(t *testing.T, path string) {
	t.Helper()
	ft := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, ft, ft); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// writeCorpusFile writes (or overwrites) a corpus file and bumps its mtime.
func writeCorpusFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := corpusFile(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	writeFile(t, p, content)
	touchFuture(t, p)
}

// removeCorpusFile deletes a corpus file (deletion is detected by absence, so no
// mtime bump is needed).
func removeCorpusFile(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.Remove(corpusFile(root, rel)); err != nil {
		t.Fatalf("remove %s: %v", rel, err)
	}
}

// dropCorpusFiles deletes files from the pristine copy in a scenario's setup
// (before the baseline scan), so a mechanic can be tested in isolation. Used to
// remove the react-importing component files that otherwise trip the
// cross-language dependency bug on every JS/TS incremental scan.
func dropCorpusFiles(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if err := os.Remove(corpusFile(root, rel)); err != nil {
			t.Fatalf("drop %s: %v", rel, err)
		}
	}
}

// dropReactComponents removes both react-importing component files. Used as the
// setup for JS/TS *core* mechanic scenarios so the cross-language react
// dependency bug (filed as ..._4) does not mask the mechanic under test; the
// bug itself is owned by the dedicated xlang and JSX/TSX scenarios.
func dropReactComponents(t *testing.T, root string) {
	t.Helper()
	dropCorpusFiles(t, root, "jsfamily/components.jsx", "tsfamily/components.tsx")
}

// replaceInCorpusFile applies a set of literal old->new replacements to a corpus
// file and bumps its mtime. Each old anchor must exist or the test fails (so a
// drifted corpus is caught loudly rather than silently no-op'ing).
func replaceInCorpusFile(t *testing.T, root, rel string, pairs ...[2]string) {
	t.Helper()
	p := corpusFile(root, rel)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	s := string(data)
	for _, pr := range pairs {
		if !strings.Contains(s, pr[0]) {
			t.Fatalf("replaceInCorpusFile %s: anchor not found: %q", rel, pr[0])
		}
		s = strings.ReplaceAll(s, pr[0], pr[1])
	}
	writeFile(t, p, s)
	touchFuture(t, p)
}

// touchCorpusFile forces a file to be re-parsed by the incremental scan without
// changing its semantics, by appending a trailing comment. Used to model "the
// dependent file was also re-saved" so a scenario is a FAIR cross-mode compare.
func touchCorpusFile(t *testing.T, root, rel string) {
	t.Helper()
	p := corpusFile(root, rel)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	comment := "\n// aracne-test-touch\n"
	if strings.HasSuffix(rel, ".py") {
		comment = "\n# aracne-test-touch\n"
	}
	writeFile(t, p, string(data)+comment)
	touchFuture(t, p)
}

// ---------------------------------------------------------------------------
// Scan driver + scenario runner.
// ---------------------------------------------------------------------------

type scenario struct {
	name string
	// setup runs on the pristine copy BEFORE the baseline scan (e.g. to seed a
	// file whose later deletion is under test). May be nil.
	setup func(t *testing.T, root string)
	// mutate applies the predictable edit AFTER the baseline scan.
	mutate func(t *testing.T, root string)
	// assert verifies the expected post-edit topology. It runs once per mode
	// ("incremental"/"full"/"hard"); mode is supplied for clearer messages.
	assert func(t *testing.T, topo *domain.Topology, mode string)
}

func readTopo(t *testing.T, db string) *domain.Topology {
	t.Helper()
	topo, err := helper.ReadDb(db)
	if err != nil {
		t.Fatalf("read db %s: %v", db, err)
	}
	return topo
}

// runScenario copies the FULL corpus, runs setup, takes a baseline scan, applies
// the mutation, then produces the incremental / cold-full / cold-hard
// topologies, runs the per-mode assertions, and enforces strict cross-mode
// equality.
func runScenario(t *testing.T, sc scenario) {
	runScenarioWith(t, sc, copyCorpus)
}

// runGoScenario is runScenario over a Go-ONLY corpus copy (copyGoCorpus). The
// Go resource IDs are identical, but the other language subtrees are excluded so
// the scenario sidesteps the pre-existing JS/TS multi-language scan crash. Use
// it for Go edge-case scenarios that must run independently of that crash.
func runGoScenario(t *testing.T, sc scenario) {
	runScenarioWith(t, sc, copyGoCorpus)
}

// runJavaScenario is runScenario over a Java-ONLY corpus copy (copyJavaCorpus),
// so Java scenarios run independently of the pre-existing multi-language
// scan-write abort, mirroring the python-/go-only isolation trick.
func runJavaScenario(t *testing.T, sc scenario) {
	runScenarioWith(t, sc, copyJavaCorpus)
}

// runScenarioWith is runScenario parameterized by the corpus-copy function, so a
// language-focused suite can scan a single-language subtree while reusing the
// shared mutate / assert / cross-mode-equality machinery.
func runScenarioWith(t *testing.T, sc scenario, copyCorpusFn func(*testing.T) string) {
	t.Helper()
	t.Parallel()

	root := copyCorpusFn(t)
	if sc.setup != nil {
		sc.setup(t, root)
	}

	dbDir := t.TempDir()
	incrDB := filepath.Join(dbDir, "incr.db")
	fullDB := filepath.Join(dbDir, "full.db")
	hardDB := filepath.Join(dbDir, "hard.db")

	// Baseline cold scan establishes the manifest for the incremental run.
	mustRun(t, root, "scan", "-default", "-root", root, "-output", incrDB)

	sc.mutate(t, root)

	// Incremental update of the same DB.
	mustRun(t, root, "scan", "-default", "-root", root, "-output", incrDB)
	// Authoritative cold scans of the mutated tree into fresh DBs.
	mustRun(t, root, "scan", "-all", "-root", root, "-output", fullDB)
	mustRun(t, root, "scan", "-hard", "-root", root, "-output", hardDB)

	incr := readTopo(t, incrDB)
	full := readTopo(t, fullDB)
	hard := readTopo(t, hardDB)

	if sc.assert != nil {
		sc.assert(t, incr, "incremental")
		sc.assert(t, full, "full")
		sc.assert(t, hard, "hard")
	}

	assertSameGraph3(t, incr, full, hard)
}

// ---------------------------------------------------------------------------
// Cross-mode comparison.
// ---------------------------------------------------------------------------

// topoFingerprint canonicalizes a resource's place in the graph for cross-mode
// comparison: kind, name, language, file path, properties (which carry the
// Input/Output VariableDefinitions incl. resolved TypingIDs -> this is how
// variable-type tracking is checked), and the full connection set with targets
// sorted so ordering never matters.
//
// Deliberately EXCLUDED: Description (hard wipes preserved descriptions; the
// others keep them -> not a topology inconsistency), Location line numbers (an
// edit shifts the lines of resources below it; the path is kept), warnings and
// bugs (derived differently per mode by design).
func topoFingerprint(res domain.Resource) string {
	var b strings.Builder
	b.WriteString(string(res.Kind))
	b.WriteByte('|')
	b.WriteString(res.Name)
	b.WriteByte('|')
	b.WriteString(res.Language)
	b.WriteByte('|')
	b.WriteString(res.Location.Path)
	b.WriteByte('|')
	propsJSON, _ := json.Marshal(res.Properties)
	b.Write(propsJSON)
	b.WriteByte('|')

	connTypes := make([]string, 0, len(res.Connections))
	for ct := range res.Connections {
		connTypes = append(connTypes, ct)
	}
	sort.Strings(connTypes)
	for _, ct := range connTypes {
		targets := append([]string(nil), res.Connections[ct]...)
		sort.Strings(targets)
		b.WriteString(ct)
		b.WriteByte('=')
		b.WriteString(strings.Join(targets, ","))
		b.WriteByte(';')
	}
	return b.String()
}

func fingerprintMap(topo *domain.Topology) map[string]string {
	m := make(map[string]string, len(topo.Resources))
	for id, r := range topo.Resources {
		m[id] = topoFingerprint(r)
	}
	return m
}

// diffTopos returns human-readable differences between two topologies' graphs.
func diffTopos(nameA string, a *domain.Topology, nameB string, b *domain.Topology) []string {
	fa, fb := fingerprintMap(a), fingerprintMap(b)
	ids := make(map[string]bool, len(fa)+len(fb))
	for id := range fa {
		ids[id] = true
	}
	for id := range fb {
		ids[id] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)

	var diffs []string
	for _, id := range sorted {
		va, oka := fa[id]
		vb, okb := fb[id]
		switch {
		case oka && !okb:
			diffs = append(diffs, fmt.Sprintf("  only in %s: %s", nameA, id))
		case !oka && okb:
			diffs = append(diffs, fmt.Sprintf("  only in %s: %s", nameB, id))
		case va != vb:
			diffs = append(diffs, fmt.Sprintf("  differ %s:\n      %s: %s\n      %s: %s", id, nameA, va, nameB, vb))
		}
	}
	return diffs
}

// assertSameGraph3 enforces strict equality of the resource/connection graphs of
// the three scan modes. full vs hard is a sanity check (should always agree);
// the incremental comparisons are the real test.
func assertSameGraph3(t *testing.T, incr, full, hard *domain.Topology) {
	t.Helper()
	pairs := []struct {
		na string
		a  *domain.Topology
		nb string
		b  *domain.Topology
	}{
		{"full", full, "hard", hard},
		{"full", full, "incremental", incr},
		{"hard", hard, "incremental", incr},
	}
	for _, p := range pairs {
		d := diffTopos(p.na, p.a, p.nb, p.b)
		if len(d) == 0 {
			continue
		}
		const cap = 60
		shown := d
		truncated := ""
		if len(d) > cap {
			shown = d[:cap]
			truncated = fmt.Sprintf("\n  ... (%d more)", len(d)-cap)
		}
		t.Errorf("topology mismatch %s vs %s (%d diff(s)):\n%s%s",
			p.na, p.nb, len(d), strings.Join(shown, "\n"), truncated)
	}
}

// ---------------------------------------------------------------------------
// Targeted-assertion helpers.
// ---------------------------------------------------------------------------

func mustResource(t *testing.T, topo *domain.Topology, mode, id string) domain.Resource {
	t.Helper()
	r, ok := topo.Resources[id]
	if !ok {
		t.Fatalf("[%s] resource %q not found in topology", mode, id)
	}
	return r
}

func hasTarget(res domain.Resource, connType, target string) bool {
	for _, tg := range res.Connections[connType] {
		if tg == target {
			return true
		}
	}
	return false
}

func assertHasConn(t *testing.T, topo *domain.Topology, mode, id, connType, target string) {
	t.Helper()
	r := mustResource(t, topo, mode, id)
	if !hasTarget(r, connType, target) {
		t.Errorf("[%s] %s: expected %s edge -> %q, got %v", mode, id, connType, target, r.Connections[connType])
	}
}

func assertNoConn(t *testing.T, topo *domain.Topology, mode, id, connType, target string) {
	t.Helper()
	r, ok := topo.Resources[id]
	if !ok {
		return // absent resource has no such edge
	}
	if hasTarget(r, connType, target) {
		t.Errorf("[%s] %s: unexpected %s edge -> %q", mode, id, connType, target)
	}
}

func assertResPresent(t *testing.T, topo *domain.Topology, mode, id string) {
	t.Helper()
	if _, ok := topo.Resources[id]; !ok {
		t.Errorf("[%s] expected resource %q to be present", mode, id)
	}
}

func assertResAbsent(t *testing.T, topo *domain.Topology, mode, id string) {
	t.Helper()
	if _, ok := topo.Resources[id]; ok {
		t.Errorf("[%s] expected resource %q to be absent", mode, id)
	}
}

// assertNoReferences fails if any resource still has a connection targeting the
// given (removed/renamed) id — the "old symbol fully gone" check.
func assertNoReferences(t *testing.T, topo *domain.Topology, mode, target string) {
	t.Helper()
	for id, r := range topo.Resources {
		for ct, targets := range r.Connections {
			for _, tg := range targets {
				if tg == target {
					t.Errorf("[%s] %s still has %s edge -> removed %q", mode, id, ct, target)
				}
			}
		}
	}
}
