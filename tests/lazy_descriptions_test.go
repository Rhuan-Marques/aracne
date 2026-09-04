package tests_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/lazydesc"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/topogrep"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// descriptions.lazy, end to end over a really scanned project.
//
// The pipelines are exercised in process rather than through the binary, because the one thing
// a subprocess cannot be given is a generator that does not need an API key. Everything else is
// real: a real scan, a real SQLite topology, the real read and grep implementations both
// surfaces call, and the real renderer.
//
// The pair of assertions is the point. `read.context_filter.hide_no_description` defaults to
// true, so an undescribed neighbour is not merely undescribed in the "# CONTEXT:" block, it is
// ABSENT from it -- and that is exactly what makes "off" and "on" distinguishable in the
// output rather than only in the database.

// lazyProject scans a Go project with no doc comments at all, so nothing in it starts out
// described and every description in a result got there by being generated.
func lazyProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "pkg", "shapes.go"), `package pkg

import "fmt"

type Shape interface {
	Area() float64
}

type Circle struct {
	R float64
}

func (c Circle) Area() float64 { return 3.14 * c.R * c.R }

func Describe(s Shape) string {
	total := Total([]Shape{s})
	return fmt.Sprintf("area=%v total=%v", s.Area(), total)
}

func Total(shapes []Shape) float64 {
	sum := 0.0
	for _, s := range shapes {
		sum += s.Area()
	}
	return sum
}
`)
	mustRun(t, dir, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	return dir
}

// recordingGenerator answers everything with a recognisable line and remembers what it was
// asked, so a test can assert both the output and the cost.
type recordingGenerator struct {
	mu    sync.Mutex
	asked []string
}

func (g *recordingGenerator) Describe(_ context.Context, batch lazydesc.Batch) (map[string]string, error) {
	out := map[string]string{}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range batch.Resources {
		g.asked = append(g.asked, r.ID)
		out[r.ID] = "generated summary of " + r.Name
	}
	return out, nil
}

func (g *recordingGenerator) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.asked)
}

// lazyEnv opens the scanned project and returns its manager plus a config with the feature
// switched the way the test wants it.
func lazyEnv(t *testing.T, dir string, on bool) (*topology.TopologyManager, *helper.Config) {
	t.Helper()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatalf("load topology: %v", err)
	}
	cfg := helper.EnsureConfig(helper.ConfigPath(dbPath))
	cfg.Descriptions.Lazy.Enabled = &on
	cfg.Descriptions.StyleExemplars = 0
	return mgr, cfg
}

func readOf(t *testing.T, rd *universaltools.Read, ids ...string) string {
	t.Helper()
	out, err := rd.ReadIDs(ids, universaltools.ReadIDsOptions{Kinds: helper.AllReadKinds()})
	if err != nil {
		t.Fatalf("read %v: %v", ids, err)
	}
	return out
}

// With the feature off, a read of a function whose neighbours have no descriptions has no
// context section at all -- and nothing is generated.
func TestLazyOffLeavesTheReadUndescribed(t *testing.T) {
	dir := lazyProject(t)
	mgr, cfg := lazyEnv(t, dir, false)
	gen := &recordingGenerator{}

	rd := universaltools.NewRead(mgr, cfg, false, nil).
		WithFiller(lazydesc.NewWithGenerator(mgr, cfg, "", gen))
	out := readOf(t, rd, "demo/pkg.Describe")

	if strings.Contains(out, "# CONTEXT:") {
		t.Fatalf("lazy off produced a context section:\n%s", out)
	}
	if gen.count() != 0 {
		t.Fatalf("lazy off called the generator %d times", gen.count())
	}
	if desc := storedDescription(t, mgr, "demo/pkg.Total"); desc != "" {
		t.Fatalf("lazy off wrote a description: %q", desc)
	}
}

// With it on, the same read generates the neighbours' descriptions, waits for them, and comes
// back with the context section they unlock.
func TestLazyOnFillsTheContextSection(t *testing.T) {
	dir := lazyProject(t)
	mgr, cfg := lazyEnv(t, dir, true)
	gen := &recordingGenerator{}

	rd := universaltools.NewRead(mgr, cfg, false, nil).
		WithFiller(lazydesc.NewWithGenerator(mgr, cfg, "", gen))
	out := readOf(t, rd, "demo/pkg.Describe")

	if !strings.Contains(out, "# CONTEXT:") {
		t.Fatalf("lazy on produced no context section:\n%s", out)
	}
	if !strings.Contains(out, "demo/pkg.Total: generated summary of Total") {
		t.Fatalf("the generated description is not in the read:\n%s", out)
	}
	// The body is still the body: a fill must not disturb what the read was asked for.
	if !strings.Contains(out, "func Describe(s Shape) string {") {
		t.Fatalf("the read lost its own source:\n%s", out)
	}
	// ...and it landed in the database, not just in this one answer.
	if desc := storedDescription(t, mgr, "demo/pkg.Total"); desc != "generated summary of Total" {
		t.Fatalf("stored description = %q", desc)
	}
	// The resource that was READ is not described: its body was the answer.
	if desc := storedDescription(t, mgr, "demo/pkg.Describe"); desc != "" {
		t.Fatalf("the requested resource was described: %q", desc)
	}
}

// A warm repo pays nothing. Once the descriptions exist, the second read plans no targets and
// calls no provider -- which is what makes the cost converge instead of recurring.
func TestLazyConvergesAfterTheFirstRead(t *testing.T) {
	dir := lazyProject(t)
	mgr, cfg := lazyEnv(t, dir, true)
	gen := &recordingGenerator{}
	filler := lazydesc.NewWithGenerator(mgr, cfg, "", gen)

	rd := universaltools.NewRead(mgr, cfg, false, nil).WithFiller(filler)
	first := readOf(t, rd, "demo/pkg.Describe")
	afterFirst := gen.count()
	if afterFirst == 0 {
		t.Fatal("the first read generated nothing")
	}

	// A brand-new filler, to prove convergence comes from the DATABASE and not from the
	// in-process attempted set.
	fresh := &recordingGenerator{}
	rd2 := universaltools.NewRead(mgr, cfg, false, nil).
		WithFiller(lazydesc.NewWithGenerator(mgr, cfg, "", fresh))
	second := readOf(t, rd2, "demo/pkg.Describe")

	if fresh.count() != 0 {
		t.Fatalf("the second read generated %d description(s) on a warm repo", fresh.count())
	}
	if first != second {
		t.Fatalf("the second read differs from the first:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// The search half. A node found by a line in its body has no description to match on -- that
// is inherent -- but its header is about to be printed, so the description is written now.
func TestLazyGrepDescribesTheNodesItFound(t *testing.T) {
	dir := lazyProject(t)

	off := grepIn(t, dir, "sum", false, nil)
	if strings.Contains(off, "—") {
		t.Fatalf("lazy off produced a described grep header:\n%s", off)
	}
	if !strings.Contains(off, "demo/pkg.Total") {
		t.Fatalf("the search did not find the node at all:\n%s", off)
	}

	gen := &recordingGenerator{}
	on := grepIn(t, dir, "sum", true, gen)
	if !strings.Contains(on, "demo/pkg.Total — generated summary of Total") {
		t.Fatalf("lazy on did not describe the node it found:\n%s", on)
	}
}

// A search finds a node by NAME as well as by content, and that row is a node row: it is
// annotated unconditionally, so its description is always worth generating.
func TestLazyGrepDescribesATitleMatch(t *testing.T) {
	dir := lazyProject(t)
	gen := &recordingGenerator{}
	out := grepIn(t, dir, "Circle", true, gen)
	if !strings.Contains(out, "generated summary of") {
		t.Fatalf("a title match came back undescribed:\n%s", out)
	}
}

// The switch is one switch. Turning it off in the config file has to reach every surface,
// including the one that builds its own filler from the config rather than being handed one.
func TestLazyOffIsRespectedFromTheConfigFile(t *testing.T) {
	dir := lazyProject(t)
	setLazy(t, dir, false)

	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatal(err)
	}
	cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
	if cfg.LazyDescriptionsEnabled() {
		t.Fatal("the config file says lazy is off; the config disagrees")
	}
	if lazydesc.New(mgr, cfg, "") != nil {
		t.Fatal("a config with lazy off still built a filler")
	}

	// And on, which is also the default when the key is absent entirely.
	setLazy(t, dir, true)
	cfg = helper.LoadConfig(helper.ConfigPath(dbPath))
	if !cfg.LazyDescriptionsEnabled() {
		t.Fatal("the config file says lazy is on; the config disagrees")
	}
}

// grepIn runs the MCP grep tool over the project, with the feature switched as asked.
func grepIn(t *testing.T, dir, pattern string, on bool, gen lazydesc.Generator) string {
	t.Helper()
	mgr, cfg := lazyEnv(t, dir, on)
	var filler *lazydesc.Filler
	if gen != nil {
		filler = lazydesc.NewWithGenerator(mgr, cfg, "", gen)
	}
	g := tools.NewGrep(mgr).WithFiller(filler)

	// The tool searches from the working directory, so run it there.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	args, err := json.Marshal(map[string]any{"pattern": pattern, "path": "."})
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.Run(args)
	if err != nil {
		t.Fatalf("grep %q: %v", pattern, err)
	}
	return out
}

// setLazy rewrites descriptions.lazy in the project's config file, the way a user would.
func setLazy(t *testing.T, dir string, on bool) {
	t.Helper()
	path := filepath.Join(dir, ".aracne", "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	descriptions, _ := cfg["descriptions"].(map[string]any)
	if descriptions == nil {
		descriptions = map[string]any{}
	}
	descriptions["lazy"] = on
	cfg["descriptions"] = descriptions
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func storedDescription(t *testing.T, mgr *topology.TopologyManager, id string) string {
	t.Helper()
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	res, ok := topo.Resources[id]
	if !ok {
		t.Fatalf("resource %q is not in the topology", id)
	}
	return res.Description
}

// A sanity check on the fixture itself: if the scanner ever starts seeding descriptions from
// somewhere, these tests would pass for the wrong reason and this is what says so.
func TestLazyFixtureStartsWithNoDescriptions(t *testing.T) {
	dir := lazyProject(t)
	mgr, _ := lazyEnv(t, dir, false)
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var described []string
	for id, res := range topo.Resources {
		if strings.TrimSpace(res.Description) != "" {
			described = append(described, fmt.Sprintf("%s = %q", id, res.Description))
		}
	}
	if len(described) > 0 {
		t.Fatalf("the fixture is not undescribed: %s", strings.Join(described, ", "))
	}
	_ = topogrep.DefaultDescriptionKinds() // keep the import honest about what grep matches on
}

// The windowed read -- what `head -20 pkg/shapes.go` becomes on the terminal surface -- has its
// own render path and therefore its own fill. Its context is bounded to the resources the
// WINDOW mentions, and the fill is bounded the same way.
func TestLazySliceFillsOnlyWhatTheWindowMentions(t *testing.T) {
	dir := lazyProject(t)
	mgr, cfg := lazyEnv(t, dir, true)
	gen := &recordingGenerator{}

	rd := universaltools.NewRead(mgr, cfg, false, nil).
		WithFiller(lazydesc.NewWithGenerator(mgr, cfg, "", gen))

	// Lines 15-18 are the body of Describe, which mentions Total and Shape.Area.
	out, err := rd.ReadSlice(filepath.Join(dir, "pkg", "shapes.go"), 15, 18)
	if err != nil {
		t.Fatalf("ReadSlice: %v", err)
	}
	if !strings.Contains(out, "generated summary of") {
		t.Fatalf("a windowed read produced no generated description:\n%s", out)
	}
	if desc := storedDescription(t, mgr, "demo/pkg.Total"); desc == "" {
		t.Fatal("the windowed read did not persist the description it showed")
	}
	// Circle is nowhere in this window, so nothing about it was worth generating.
	for _, id := range gen.asked {
		if strings.Contains(id, "Circle") {
			t.Fatalf("the window described %s, which it never mentions", id)
		}
	}
}

// The same window with the feature off: no fill, no context, no cost.
func TestLazySliceOffGeneratesNothing(t *testing.T) {
	dir := lazyProject(t)
	mgr, cfg := lazyEnv(t, dir, false)
	gen := &recordingGenerator{}

	rd := universaltools.NewRead(mgr, cfg, false, nil).
		WithFiller(lazydesc.NewWithGenerator(mgr, cfg, "", gen))
	out, err := rd.ReadSlice(filepath.Join(dir, "pkg", "shapes.go"), 15, 18)
	if err != nil {
		t.Fatalf("ReadSlice: %v", err)
	}
	if gen.count() != 0 {
		t.Fatalf("a windowed read generated %d description(s) with the feature off", gen.count())
	}
	if strings.Contains(out, "generated summary of") {
		t.Fatalf("a windowed read showed a generated description with the feature off:\n%s", out)
	}
}

// The whole chain with nothing faked but the model: a real scan, the real config, the
// production generator resolved from `descriptions.lazy`, a real HTTP round trip, the real
// parser, a real database write, and the real renderer.
//
// The other tests substitute a Generator, which is the right seam for asserting behaviour but
// leaves the production factory, the provider wiring and the reply parser untested together.
// This is the one that says the feature works, not just that its parts do.
func TestLazyEndToEndThroughARealProvider(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	var asked int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		asked++
		// Answer for whichever assigned ids the prompt actually carried, in the documented
		// reply format. Anything else would be testing the fixture, not the pipeline.
		var reply strings.Builder
		for _, id := range []string{"demo/pkg.Total", "demo/pkg.Shape.Area", "demo/pkg.Circle"} {
			if strings.Contains(string(raw), id) {
				fmt.Fprintf(&reply, "%s :: sums what it is given\n", id)
			}
		}
		block, _ := json.Marshal(map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": reply.String()},
		})
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: " + string(block) + "\n\n"))
	}))
	defer server.Close()

	dir := lazyProject(t)
	mgr, cfg := lazyEnv(t, dir, true)
	cfg.Descriptions.Lazy.Provider = "anthropic"
	cfg.Descriptions.Lazy.Model = "haiku"
	cfg.Descriptions.Lazy.BaseURL = server.URL

	// No WithFiller: the read builds its own filler from the config, exactly as it does in
	// production.
	out := readOf(t, universaltools.NewRead(mgr, cfg, false, nil), "demo/pkg.Describe")

	if asked == 0 {
		t.Fatal("the read never called the provider")
	}
	if !strings.Contains(out, "demo/pkg.Total: sums what it is given") {
		t.Fatalf("the generated description is not in the read:\n%s", out)
	}
	if desc := storedDescription(t, mgr, "demo/pkg.Total"); desc != "sums what it is given" {
		t.Fatalf("stored description = %q", desc)
	}
}
