package tests_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readProject scans a two-file Go project whose members reference each other across files, so
// the batching, grouping and context-exclusion rules all have something to bite on.
func readProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "pkg", "shapes.go"), `package pkg

import "fmt"

// Shape is anything with an area.
type Shape interface {
	Area() float64
}

// Circle is a round shape.
type Circle struct {
	R float64
}

// Area returns the circle's area.
func (c Circle) Area() float64 { return 3.14 * c.R * c.R }

// Describe renders a shape as text.
func Describe(s Shape) string {
	return fmt.Sprintf("area=%v", s.Area())
}

// Total sums the areas of many shapes.
func Total(shapes []Shape) float64 {
	sum := 0.0
	for _, s := range shapes {
		sum += s.Area()
	}
	return sum
}
`)
	writeFile(t, filepath.Join(dir, "pkg", "report.go"), `package pkg

import "strings"

// Report joins descriptions of every shape.
func Report(shapes []Shape) string {
	var out []string
	for _, s := range shapes {
		out = append(out, Describe(s))
	}
	return strings.Join(out, ", ")
}
`)
	mustRun(t, dir, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	return dir
}

func TestReadGroupsBatchByFileWithOneContext(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "demo/pkg.Describe", "demo/pkg.Total", "demo/pkg.Report")

	// One fenced group per declaring file, labelled with the path relative to the root.
	if got := strings.Count(out, "```pkg/shapes.go"); got != 1 {
		t.Fatalf("expected one shapes.go group, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "```pkg/report.go"); got != 1 {
		t.Fatalf("expected one report.go group, got %d:\n%s", got, out)
	}
	// Same-file resources share a group rather than repeating the header.
	if strings.Index(out, "func Describe") > strings.Index(out, "func Total") {
		t.Fatalf("group members should be in source order:\n%s", out)
	}
	// Exactly one context section for the whole batch.
	if got := strings.Count(out, "# CONTEXT:"); got != 1 {
		t.Fatalf("expected one CONTEXT section, got %d:\n%s", got, out)
	}
	// Describe is rendered as source, so Report's call to it must not be described again.
	if strings.Contains(out, "## demo/pkg.Describe") {
		t.Fatalf("a resource shown as source must not appear in CONTEXT:\n%s", out)
	}
}

func TestReadBatchCostsLessThanSeparateReads(t *testing.T) {
	dir := readProject(t)

	separate := len(mustRun(t, dir, "read", "demo/pkg.Describe")) +
		len(mustRun(t, dir, "read", "demo/pkg.Total")) +
		len(mustRun(t, dir, "read", "demo/pkg.Report"))
	batched := len(mustRun(t, dir, "read", "demo/pkg.Describe", "demo/pkg.Total", "demo/pkg.Report"))

	// Batching is the point of the list argument: it saves the turns AND the repeated headers
	// and shared context entries. If it ever costs more, the grouping has regressed.
	if batched >= separate {
		t.Fatalf("batched read (%d bytes) should beat %d bytes of separate reads", batched, separate)
	}
	t.Logf("batched %d bytes vs %d separate (%.0f%% saved)", batched, separate,
		100*(1-float64(batched)/float64(separate)))
}

func TestReadFileDoesNotRepeatItsOwnMembers(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "pkg/shapes.go")

	// The whole file is the body, so everything it declares is already shown. Listing those
	// under CONTEXT was an index of the fence directly above it.
	for _, member := range []string{"demo/pkg.Describe", "demo/pkg.Total", "demo/pkg.Circle", "demo/pkg.Shape"} {
		if strings.Contains(out, "## "+member) {
			t.Fatalf("file read repeats its own member %q in CONTEXT:\n%s", member, out)
		}
	}
	if !strings.Contains(out, "func Describe") {
		t.Fatalf("file read should contain the file source:\n%s", out)
	}
}

func TestReadFileShowsWhatItReachesOutside(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "pkg/report.go")

	// report.go calls Describe, which lives in shapes.go: that IS context, and is what the
	// section should have been showing all along.
	if !strings.Contains(out, "demo/pkg.Describe") {
		t.Fatalf("file read should surface neighbours outside the file:\n%s", out)
	}
}

func TestReadFileAbsorbsAMemberAskedForSeparately(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "pkg/shapes.go", "demo/pkg.Describe")

	if got := strings.Count(out, "func Describe"); got != 1 {
		t.Fatalf("Describe rendered %d times, want 1:\n%s", got, out)
	}
	if got := strings.Count(out, "```pkg/shapes.go"); got != 1 {
		t.Fatalf("expected a single group, got %d:\n%s", got, out)
	}
}

func TestReadDedupesRepeatedIDs(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "demo/pkg.Describe", "demo/pkg.Describe")

	if got := strings.Count(out, "func Describe"); got != 1 {
		t.Fatalf("duplicate ids rendered %d times, want 1:\n%s", got, out)
	}
}

func TestReadInlinesASmallReceiverButNotABigClass(t *testing.T) {
	dir := readProject(t)

	// A Go struct declaration is short, so a method still arrives with its receiver type.
	out := mustRun(t, dir, "read", "demo/pkg.(Circle).Area")
	if !strings.Contains(out, "type Circle struct") {
		t.Fatalf("a small receiver type should be inlined above the method:\n%s", out)
	}
	if got := strings.Count(out, "func (c Circle) Area"); got != 1 {
		t.Fatalf("method rendered %d times, want 1:\n%s", got, out)
	}
}

func TestReadReportsBadIDsWithoutLosingGoodOnes(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "demo/pkg.Describe", "demo/pkg.Describ")

	if !strings.Contains(out, "func Describe") {
		t.Fatalf("a bad id must not throw away the good results:\n%s", out)
	}
	if !strings.Contains(out, "# UNRESOLVED:") {
		t.Fatalf("expected an UNRESOLVED section:\n%s", out)
	}
	// A miss costs a correction, not an exploration turn: the near-match is named.
	if !strings.Contains(out, "demo/pkg.Describe (function)") {
		t.Fatalf("expected the resolver's candidate hint:\n%s", out)
	}
}

// multiMethodProject declares one struct with several methods, plus a second struct, so a
// batch can ask for siblings that share a receiver.
func multiMethodProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "pkg", "conn.go"), `package pkg

// Conn is a connection with a good deal of state worth not repeating.
type Conn struct {
	Target   string
	Authority string
	Closed   bool
	Retries  int
}

// Close marks the connection closed.
func (c *Conn) Close() error {
	c.Closed = true
	return nil
}

// Reset clears the retry counter.
func (c *Conn) Reset() {
	c.Retries = 0
}

// Bump increments the retry counter.
func (c *Conn) Bump() {
	c.Retries++
}
`)
	mustRun(t, dir, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	return dir
}

// Reading several methods of one struct used to emit the receiver's declaration once per
// method: the grpc/grpc-go run asked for five ClientConn methods in one call and got five
// copies of the struct. Bodies are built before the render state exists, so the dedup that
// already protected the CONTEXT section never reached them.
func TestReadBatchInlinesEnclosingTypeOnce(t *testing.T) {
	dir := multiMethodProject(t)

	out := mustRun(t, dir, "read", "demo/pkg.(Conn).Close", "demo/pkg.(Conn).Reset", "demo/pkg.(Conn).Bump")

	if got := strings.Count(out, "type Conn struct"); got != 1 {
		t.Fatalf("enclosing type should be inlined once, got %d:\n%s", got, out)
	}
	// Eliding the receiver must not elide the method the caller actually asked for.
	for _, m := range []string{"func (c *Conn) Close", "func (c *Conn) Reset", "func (c *Conn) Bump"} {
		if !strings.Contains(out, m) {
			t.Fatalf("batch dropped %q:\n%s", m, out)
		}
	}
}

// The marker replacing a repeated receiver must not claim a position. Bodies are built in the
// caller's request order but rendered sorted by (path, line), so "shown above" would be a lie
// whenever the two disagree -- as they do here.
func TestReadBatchElisionMarkerIsOrderIndependent(t *testing.T) {
	dir := multiMethodProject(t)

	out := mustRun(t, dir, "read", "demo/pkg.(Conn).Bump", "demo/pkg.(Conn).Close")

	if got := strings.Count(out, "type Conn struct"); got != 1 {
		t.Fatalf("enclosing type should be inlined once, got %d:\n%s", got, out)
	}
	if strings.Contains(out, "shown above") || strings.Contains(out, "shown below") {
		t.Fatalf("elision marker must not assert a position:\n%s", out)
	}
	if !strings.Contains(out, "demo/pkg.Conn") {
		t.Fatalf("elision marker should name the type to read:\n%s", out)
	}
}

// A single-resource read is not a batch and must be unchanged: the receiver is still inlined
// in full, because there is nothing for it to duplicate.
func TestReadSingleMethodStillInlinesItsReceiver(t *testing.T) {
	dir := multiMethodProject(t)

	out := mustRun(t, dir, "read", "demo/pkg.(Conn).Close")

	if !strings.Contains(out, "type Conn struct") {
		t.Fatalf("single read should inline the receiver:\n%s", out)
	}
	if strings.Contains(out, "not shown here") {
		t.Fatalf("single read should not elide anything:\n%s", out)
	}
}

// `src/app.rs:build_app` and `src/app.rs:95-115` are shapes a model invents when it wants part
// of a file. Every one of them missed in the compact-blocked-20260830c run -- four of its six
// total id misses -- and both are reasonable guesses, so neither should end in a dead end.
func TestReadAcceptsFileScopedSymbolID(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "pkg/shapes.go:Describe")

	if !strings.Contains(out, "func Describe") {
		t.Fatalf("file-scoped symbol id should resolve to the symbol:\n%s", out)
	}
	// It is a symbol read, not a file read: the rest of the file must not come along.
	if strings.Contains(out, "func Total") {
		t.Fatalf("file-scoped symbol id returned the whole file:\n%s", out)
	}
}

// A line range is deliberately NOT served -- Read.Parameters records that walking a file in
// windows was the most expensive habit an earlier benchmark found. The miss has to teach the
// id instead, or the model just retries the same shape.
//
// It teaches from a FAILURE: nothing was read, so `arac read` exits non-zero. A read that
// returns no source and exits 0 is a command claiming it did its job.
func TestReadLineRangeRedirectsToTheEnclosingID(t *testing.T) {
	dir := readProject(t)

	out, err := runLtp(t, dir, "read", "pkg/shapes.go:1-40")
	if err == nil {
		t.Fatalf("a read that resolved nothing should exit non-zero:\n%s", out)
	}
	if !strings.Contains(out, "# UNRESOLVED:") {
		t.Fatalf("a line range should not resolve:\n%s", out)
	}
	if !strings.Contains(out, "demo/pkg.") {
		t.Fatalf("a line range should name the ids covering it:\n%s", out)
	}
}

// The split must not fire on ids that legitimately contain colons. Rust and C++ use "::",
// which is the case most likely to break.
func TestReadStillResolvesIDsContainingColons(t *testing.T) {
	dir := readProject(t)

	// A "::"-shaped id has no lone colon, so nothing should be stripped and the resolver's
	// normal miss path (with candidates) should be what answers.
	out := mustRun(t, dir, "read", "demo::pkg::Describe")
	if !strings.Contains(out, "func Describe") {
		t.Fatalf("a :: separated id should still resolve:\n%s", out)
	}
}

// skeletonProject is readProject with read.file_mode set to "skeleton".
//
// The setting is patched INTO the config the scan wrote, not written over it: a config that
// omits the rest of the schema is treated as an old-format file and clean-break replaced with
// defaults (see helper.validConfig), which would silently un-set the very flag under test.
func skeletonProject(t *testing.T) string {
	t.Helper()
	dir := readProject(t)
	path := filepath.Join(dir, ".aracne", "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	read, ok := cfg["read"].(map[string]any)
	if !ok {
		t.Fatalf("config has no read section: %s", raw)
	}
	read["file_mode"] = "skeleton"
	// Explicit, so this fixture measures the MECHANISM and not the shipped default. The
	// default (helper.DefaultSkeletonThreshold) is deliberately generous enough to keep small
	// helpers whole, and this project's declarations are all small helpers.
	read["skeleton_threshold"] = 3
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(out))
	return dir
}

// A whole-file read measured 1.00x the bytes on disk plus the context block on top, which made
// it strictly more expensive than `cat`. Skeleton mode is the answer for the file path; symbol
// reads were already compact.
func TestReadFileSkeletonCostsLessThanTheFile(t *testing.T) {
	dir := skeletonProject(t)
	onDisk, err := os.ReadFile(filepath.Join(dir, "pkg", "shapes.go"))
	if err != nil {
		t.Fatal(err)
	}

	skeleton := mustRun(t, dir, "read", "pkg/shapes.go")
	full := mustRun(t, dir, "read", "--full", "pkg/shapes.go")
	out := skeleton

	// Compared against the verbatim read, not against the file: both carry the same context
	// block, so only the body differs and the saving is the thing actually being measured.
	if len(skeleton) >= len(full) {
		t.Fatalf("skeleton read (%d bytes) should beat the verbatim read (%d), file is %d:\n%s",
			len(skeleton), len(full), len(onDisk), skeleton)
	}
	// Signatures survive; long bodies do not.
	if !strings.Contains(out, "func Total") {
		t.Fatalf("skeleton should keep declarations:\n%s", out)
	}
	if strings.Contains(out, "sum += s.Area()") {
		t.Fatalf("skeleton should elide long bodies:\n%s", out)
	}
}

// The load-bearing safety property: `edit` matches old_string against the bytes on disk, so
// everything a skeleton shows must either BE those bytes or be unmistakably not source. Text
// that merely looks like a plausible signature is what would silently fail as an old_string.
func TestReadFileSkeletonMarksElisionsUnmistakably(t *testing.T) {
	dir := skeletonProject(t)
	onDisk, err := os.ReadFile(filepath.Join(dir, "pkg", "shapes.go"))
	if err != nil {
		t.Fatal(err)
	}

	out := mustRun(t, dir, "read", "pkg/shapes.go")

	body := out
	if i := strings.Index(out, "# CONTEXT:"); i >= 0 {
		body = out[:i]
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "```") {
			continue
		}
		if strings.Contains(line, "⋯") {
			// A marker must say HOW MUCH is missing and WHERE to get it, and must not read
			// as code. What "where" is spelled as follows the identification mode: a
			// trailing id segment, or a `path:start-end` span that is directly runnable.
			// It is the same marker the windowed reader emits, which is the point -- one
			// vocabulary per project rather than one per renderer.
			if !strings.Contains(line, "lines") {
				t.Errorf("elision marker does not say how much is missing: %q", line)
			}
			if !strings.Contains(line, "Total") && !strings.Contains(line, "Describe") &&
				!strings.Contains(line, "pkg/shapes.go:") {
				t.Errorf("elision marker points nowhere: %q", line)
			}
			continue
		}
		// Everything else has to be a verbatim line of the file.
		if !strings.Contains(string(onDisk), trimmed) {
			t.Errorf("skeleton emitted a line that is not in the file: %q", line)
		}
	}
}

// The override exists so a model that needs exact bytes to build an old_string can get them
// without an admin editing the project config.
func TestReadFileFullOverridesSkeletonMode(t *testing.T) {
	dir := skeletonProject(t)

	skeleton := mustRun(t, dir, "read", "pkg/shapes.go")
	full := mustRun(t, dir, "read", "--full", "pkg/shapes.go")

	if len(full) <= len(skeleton) {
		t.Fatalf("--full (%d bytes) should return more than the skeleton (%d)", len(full), len(skeleton))
	}
	if !strings.Contains(full, "sum += s.Area()") {
		t.Fatalf("--full should return the whole body:\n%s", full)
	}
}

// Default is unchanged: the measurement that justifies skeleton mode has to be repeatable
// against today's behaviour, so it stays opt-in until a run says otherwise.
func TestReadFileDefaultsToVerbatim(t *testing.T) {
	dir := readProject(t)
	out := mustRun(t, dir, "read", "pkg/shapes.go")
	if !strings.Contains(out, "sum += s.Area()") {
		t.Fatalf("default file read should be verbatim:\n%s", out)
	}
}

// hugeSymbolProject is a project with one deliberately enormous function, plus a config that
// caps symbol reads well below it.
func hugeSymbolProject(t *testing.T, maxSymbolLines int) string {
	t.Helper()
	dir := readProject(t)
	var body strings.Builder
	body.WriteString("package pkg\n\n// Huge is one very long function.\nfunc Huge(n int) int {\n\ttotal := 0\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&body, "\ttotal += n * %d // step %d\n", i, i)
	}
	body.WriteString("\treturn total\n}\n")
	writeFile(t, filepath.Join(dir, "pkg", "huge.go"), body.String())

	path := filepath.Join(dir, ".aracne", "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["read"].(map[string]any)["max_symbol_lines"] = maxSymbolLines
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(out))
	return dir
}

// Symbol reads were the one path with no ceiling at all: read.max_file_size bounds a file,
// file_mode "skeleton" bounds a whole-file read, and nothing bounded a symbol. One nushell
// read of two ids returned 75,893 bytes -- the largest single tool result across two benchmark
// runs -- because one of them was an enormous function.
func TestReadSymbolRespectsTheLineCap(t *testing.T) {
	dir := hugeSymbolProject(t, 20)
	mustRun(t, dir, "scan")

	out := mustRun(t, dir, "read", "demo/pkg.Huge")
	if !strings.Contains(out, "func Huge(n int) int {") {
		t.Fatalf("the signature must survive the cap:\n%s", out)
	}
	if strings.Contains(out, "step 150") {
		t.Fatalf("a body over the cap should be elided:\n%s", out)
	}
	if !strings.Contains(out, "⋯") || !strings.Contains(out, "full: true") {
		t.Fatalf("an elided symbol must be marked and must name its escape hatch:\n%s", out)
	}

	// `full` is the escape hatch a model needs to build an `edit` old_string, so it must
	// return the exact bytes even for an oversized symbol.
	full := mustRun(t, dir, "read", "--full", "demo/pkg.Huge")
	if !strings.Contains(full, "step 150") {
		t.Fatalf("--full must return the whole body:\n%s", full[:min(len(full), 400)])
	}
	if len(out) >= len(full) {
		t.Fatalf("capped read (%d bytes) should beat the full read (%d)", len(out), len(full))
	}
}

// A symbol comfortably under the cap must come back untouched -- the cap exists for the
// pathological case, not to abridge ordinary functions.
func TestReadSymbolUnderTheCapIsVerbatim(t *testing.T) {
	dir := hugeSymbolProject(t, 500)
	mustRun(t, dir, "scan")
	out := mustRun(t, dir, "read", "demo/pkg.Total")
	if !strings.Contains(out, "sum += s.Area()") {
		t.Fatalf("a small symbol must be verbatim:\n%s", out)
	}
	if strings.Contains(out, "⋯") {
		t.Fatalf("a small symbol must not be elided:\n%s", out)
	}
}

// A method's rendered body is not one declaration: the enclosing type is inlined above it. An
// abridger that split at the FIRST opening brace would keep the struct and elide the method
// the model asked for -- the exact opposite of the point.
func TestReadSymbolCapKeepsTheAskedForDeclaration(t *testing.T) {
	dir := readProject(t)
	var b strings.Builder
	b.WriteString("package pkg\n\n// Big is a type with a very long method.\ntype Big struct {\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "\tField%d int\n", i)
	}
	b.WriteString("}\n\n// Grind does a lot of work.\nfunc (x Big) Grind() int {\n\ttotal := 0\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "\ttotal += x.Field0 * %d // step %d\n", i, i)
	}
	b.WriteString("\treturn total\n}\n")
	writeFile(t, filepath.Join(dir, "pkg", "big.go"), b.String())

	path := filepath.Join(dir, ".aracne", "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["read"].(map[string]any)["max_symbol_lines"] = 30
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(out))
	mustRun(t, dir, "scan")

	got := mustRun(t, dir, "read", "demo/pkg.(Big).Grind")
	if !strings.Contains(got, "func (x Big) Grind() int {") {
		t.Fatalf("the method's own signature must survive the cap:\n%s", got)
	}
	if strings.Contains(got, "step 150") {
		t.Fatalf("the method body should be elided:\n%s", got)
	}
	if !strings.Contains(got, "⋯") {
		t.Fatalf("an elided method must be marked:\n%s", got)
	}
}

// Python renders a method INSIDE its class, so the declaration the cap must preserve is
// indented. A "last TOP-LEVEL declaration" rule would pick the class line every time and elide
// the method whole.
func TestReadSymbolCapHandlesIndentedDeclarations(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mod.py"), func() string {
		var b strings.Builder
		b.WriteString("class Widget:\n    \"\"\"A widget.\"\"\"\n\n    def grind(self, n):\n        total = 0\n")
		for i := 0; i < 200; i++ {
			fmt.Fprintf(&b, "        total += n * %d  # step %d\n", i, i)
		}
		b.WriteString("        return total\n")
		return b.String()
	}())
	mustRun(t, dir, "scan")
	path := filepath.Join(dir, ".aracne", "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["read"]; !ok {
		cfg["read"] = map[string]any{}
	}
	cfg["read"].(map[string]any)["max_symbol_lines"] = 20
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(out))

	got := mustRun(t, dir, "read", "Widget.grind")
	if !strings.Contains(got, "def grind(self, n):") {
		t.Fatalf("the method signature must survive the cap:\n%s", got)
	}
	if strings.Contains(got, "step 150") {
		t.Fatalf("the method body should be elided:\n%s", got)
	}
}

// `file.rs:Type::method` is the form a Rust-shaped model reaches for, and rejecting the `::`
// silently threw the file scope away: `serde_derive/src/de.rs:Parameters::new` was looked up
// as one whole id and came back "Multiple resources matching" with five unrelated `::new`
// methods from across the crate. The scoping was in the id and was being discarded.
func TestReadFileScopedSymbolAcceptsPathSeparators(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.21\n")
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The same method name in two files: without file scoping the lookup is ambiguous.
	writeFile(t, filepath.Join(dir, "a", "one.go"), `package a

type Params struct{ N int }

// New builds a Params in package a.
func (p Params) New() int { return p.N + 1 }
`)
	writeFile(t, filepath.Join(dir, "b", "two.go"), `package b

type Params struct{ N int }

// New builds a Params in package b.
func (p Params) New() int { return p.N + 2 }
`)
	mustRun(t, dir, "scan")

	out := mustRun(t, dir, "read", "a/one.go:Params.New")
	if !strings.Contains(out, "return p.N + 1") {
		t.Fatalf("file-scoped dotted form should resolve to package a:\n%s", out)
	}
	if strings.Contains(out, "return p.N + 2") {
		t.Fatalf("file scope was ignored — package b leaked in:\n%s", out)
	}
	// The `::` spelling must scope identically rather than falling through to a repo-wide
	// lookup that reports an ambiguity the id had already resolved.
	scoped := mustRun(t, dir, "read", "b/two.go:Params::New")
	if !strings.Contains(scoped, "return p.N + 2") {
		t.Fatalf("`::` spelling should resolve to package b:\n%s", scoped)
	}
	if strings.Contains(scoped, "Multiple resources matching") {
		t.Fatalf("`::` spelling lost its file scope:\n%s", scoped)
	}
}
