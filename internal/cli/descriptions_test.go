package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"io"
	"reflect"
	"sync"
	"time"

	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/lazydesc"
	"github.com/Rhuan-Marques/aracne/internal/progress"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func TestDescriptionsExecutorProfileIsLimited(t *testing.T) {
	tools := helper.DefaultAgentMCPTools("descriptions-generation-executor")
	allowed := make(map[string]bool, len(tools))
	for _, tool := range tools {
		allowed[tool] = true
	}

	for _, want := range []string{"read", "update_description"} {
		if !allowed[want] {
			t.Fatalf("executor profile missing %q in %v", want, tools)
		}
	}
	for _, blocked := range []string{"node_list_no_description", "edit", "write", "bug_report", "bug_list"} {
		if allowed[blocked] {
			t.Fatalf("executor profile should not include %q in %v", blocked, tools)
		}
	}
}

func TestChunkDescriptionResourcesUsesBatchesOfRequestedSize(t *testing.T) {
	resources := []descriptionResource{
		{ID: "1", Name: "one", Kind: domain.ResourceFunction},
		{ID: "2", Name: "two", Kind: domain.ResourceFunction},
		{ID: "3", Name: "three", Kind: domain.ResourceFunction},
		{ID: "4", Name: "four", Kind: domain.ResourceFunction},
		{ID: "5", Name: "five", Kind: domain.ResourceFunction},
	}

	batches := chunkDescriptionResources(resources, 2)
	if len(batches) != 3 {
		t.Fatalf("len(batches) = %d, want 3", len(batches))
	}
	if len(batches[0]) != 2 || len(batches[1]) != 2 || len(batches[2]) != 1 {
		t.Fatalf("unexpected batch sizes: %d, %d, %d", len(batches[0]), len(batches[1]), len(batches[2]))
	}

	seen := map[string]bool{}
	for _, batch := range batches {
		for _, res := range batch {
			if seen[res.ID] {
				t.Fatalf("resource %s appeared in multiple batches", res.ID)
			}
			seen[res.ID] = true
		}
	}
}

func TestDescriptionExecutorInputConstrainsAssignedResources(t *testing.T) {
	input := descriptionExecutorInput([]descriptionResource{{ID: "fn:one", Name: "One", Kind: domain.ResourceFunction}}, nil, nil)
	for _, want := range []string{"Describe only the assigned resource", "read", "update_description", "fn:one", "Kind: function"} {
		if !strings.Contains(input, want) {
			t.Fatalf("executor input missing %q:\n%s", want, input)
		}
	}
}

// A --regen_oversized batch must reach the executor as a rewrite of the stored text, not
// as a blank-slate description.
func TestDescriptionExecutorInputCarriesCurrentDescription(t *testing.T) {
	current := strings.Repeat("wordy ", 40)
	input := descriptionExecutorInput([]descriptionResource{
		{ID: "fn:one", Name: "One", Kind: domain.ResourceFunction, Current: current},
	}, nil, nil)
	for _, want := range []string{"Rewrite the description", "Current description (", "fn:one"} {
		if !strings.Contains(input, want) {
			t.Fatalf("regen executor input missing %q:\n%s", want, input)
		}
	}
}

// --regen_oversized selects described-but-too-long resources; the default pass selects
// undescribed ones. The two populations never overlap.
func TestPendingDescriptionResourcesRegenOversized(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "topology.db")
	loc := domain.Location{Path: "x.go", StartsAt: 1, EndsAt: 20}
	overFn := strings.Repeat("x", domain.DescriptionBudgetFunction+1)
	topo := &domain.Topology{Root: ".", Language: "go", Resources: map[string]domain.Resource{
		"fn:over":  {ID: "fn:over", Name: "Over", Kind: domain.ResourceFunction, Description: overFn, Location: loc},
		"fn:ok":    {ID: "fn:ok", Name: "Ok", Kind: domain.ResourceFunction, Description: "Fits fine.", Location: loc},
		"fn:blank": {ID: "fn:blank", Name: "Blank", Kind: domain.ResourceFunction, Location: loc},
		// Fits a function's 120 but overruns a type's 100.
		"st:over": {ID: "st:over", Name: "StOver", Kind: domain.ResourceStruct, Description: strings.Repeat("y", domain.DescriptionBudgetType+1), Location: loc},
	}}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	// WriteDb caps descriptions on the way in (domain.CapDescription), so an over-budget row
	// can no longer be created through it. These rows are the GRANDFATHERED ones --  written
	// before the cap existed -- which is the only population `--regen_oversized` exists to
	// find, so the test writes them the way history did: straight into the table.
	writeOversizedDescription(t, dbPath, "fn:over", strings.Repeat("x", domain.DescriptionBudgetFunction+1))
	writeOversizedDescription(t, dbPath, "st:over", strings.Repeat("y", domain.DescriptionBudgetType+1))
	manager := topology.New()
	if err := manager.Load(dbPath); err != nil {
		t.Fatalf("Load: %v", err)
	}

	targets := []domain.ResourceKind{domain.ResourceFunction, domain.ResourceStruct}
	regen, err := pendingDescriptionResources(manager, targets, domain.DefaultContextFilter(), false, true)
	if err != nil {
		t.Fatalf("pendingDescriptionResources: %v", err)
	}
	ids := make([]string, 0, len(regen))
	for _, res := range regen {
		ids = append(ids, res.ID)
	}
	if got := strings.Join(ids, ","); got != "fn:over,st:over" {
		t.Fatalf("regen selection = %q, want \"fn:over,st:over\"", got)
	}
	for _, res := range regen {
		if res.Current == "" {
			t.Fatalf("%s must carry its stored description into the prompt", res.ID)
		}
	}

	generate, err := pendingDescriptionResources(manager, targets, domain.DefaultContextFilter(), false, false)
	if err != nil {
		t.Fatalf("pendingDescriptionResources: %v", err)
	}
	if len(generate) != 1 || generate[0].ID != "fn:blank" || generate[0].Current != "" {
		t.Fatalf("default pass should select only the undescribed resource, got %+v", generate)
	}
}

func TestParseClearDescriptionTargetsAcceptsBracketedKinds(t *testing.T) {
	targets, err := parseClearDescriptionTargets("[Function, Struct]")
	if err != nil {
		t.Fatalf("parseClearDescriptionTargets: %v", err)
	}
	if len(targets) != 2 || targets[0] != domain.ResourceFunction || targets[1] != domain.ResourceStruct {
		t.Fatalf("targets = %v, want [function struct]", targets)
	}
}

func TestParseClearDescriptionTargetsEmptyMeansAll(t *testing.T) {
	targets, err := parseClearDescriptionTargets("")
	if err != nil {
		t.Fatalf("parseClearDescriptionTargets: %v", err)
	}
	if targets != nil {
		t.Fatalf("targets = %v, want nil", targets)
	}
}

func TestWriteOpenCodePrimaryCommandDoesNotCreateSubtask(t *testing.T) {
	dir := t.TempDir()
	writeOpenCodePrimaryCommand(dir, "descriptions-generate", "Generate descriptions", "build", "body", true)

	data, err := os.ReadFile(filepath.Join(dir, "descriptions-generate.md"))
	if err != nil {
		t.Fatalf("read command: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "agent: build") {
		t.Fatalf("command missing primary agent:\n%s", content)
	}
	if strings.Contains(content, "subtask: true") {
		t.Fatalf("descriptions command must not run as an OpenCode subtask:\n%s", content)
	}
}

// writeOversizedDescription puts a description into the table directly, bypassing the storage
// cap, to simulate a row written before that cap existed.
func writeOversizedDescription(t *testing.T, dbPath, id, description string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE resources SET description = ? WHERE id = ?", description, id); err != nil {
		t.Fatalf("seed oversized description: %v", err)
	}
}

// fakeDescriptionGenerator answers a batch from a canned map, so the sweep's CLI path can be
// exercised without launching anything.
type fakeDescriptionGenerator struct {
	replies map[string]string
	seen    []string
}

func (g *fakeDescriptionGenerator) Describe(ctx context.Context, batch lazydesc.Batch) (map[string]string, error) {
	out := map[string]string{}
	for _, req := range batch.Resources {
		g.seen = append(g.seen, req.ID)
		if d, ok := g.replies[req.ID]; ok {
			out[req.ID] = d
		}
	}
	return out, nil
}

// The CLI-provider sweep writes what the reply carried, straight into the topology, and leaves
// what it did not carry undescribed for the next wave to re-list. That partial-success reading
// is the contract the agent runner already has; a CLI run must not trade it for all-or-nothing.
func TestCLIDescriptionRunnerWritesWhatCameBack(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "topology.db")
	loc := domain.Location{Path: "x.go", StartsAt: 1, EndsAt: 20}
	topo := &domain.Topology{Root: ".", Language: "go", Resources: map[string]domain.Resource{
		"fn:alpha": {ID: "fn:alpha", Name: "Alpha", Kind: domain.ResourceFunction, Location: loc},
		"fn:beta":  {ID: "fn:beta", Name: "Beta", Kind: domain.ResourceFunction, Location: loc},
	}}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	manager := topology.New()
	if err := manager.Load(dbPath); err != nil {
		t.Fatalf("Load: %v", err)
	}

	gen := &fakeDescriptionGenerator{replies: map[string]string{"fn:alpha": "Normalizes a tag"}}
	runner := &cliDescriptionRunner{gen: gen, manager: manager}
	batch := []descriptionResource{
		{ID: "fn:alpha", Name: "Alpha", Kind: domain.ResourceFunction},
		{ID: "fn:beta", Name: "Beta", Kind: domain.ResourceFunction},
	}
	text, err := runner.Run(batch, nil, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(text, "fn:alpha") {
		t.Errorf("the batch report should name what it wrote, got %q", text)
	}
	if len(gen.seen) != 2 {
		t.Errorf("the generator should see the whole batch, saw %v", gen.seen)
	}

	written, err := manager.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := written.Resources["fn:alpha"].Description; got != "Normalizes a tag" {
		t.Errorf("fn:alpha description = %q", got)
	}
	if got := written.Resources["fn:beta"].Description; got != "" {
		t.Errorf("fn:beta was not described in the reply, got %q", got)
	}
}

// `descriptions.provider: "cli"` is the one config both entry points read, so the sweep must
// build the CLI runner from it -- and must not need an API key to do it.
func TestNewDescriptionRunnerPicksTheCLITransport(t *testing.T) {
	if _, err := os.Stat("/bin/cat"); err != nil {
		t.Skip("needs a real binary on disk")
	}
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "DEEPSEEK_API_KEY"} {
		t.Setenv(k, "")
	}
	cfg := helper.DefaultConfig()
	cfg.Descriptions.Provider = "cli"
	cfg.Descriptions.CLIProviderCommand = "/bin/cat -"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a cli provider with a command must validate: %v", err)
	}

	runner, label, err := newDescriptionRunner(nil, nil, cfg,
		cfg.EffectiveLazyDescriptions(helper.DefaultLazyHarness))
	if err != nil {
		t.Fatalf("newDescriptionRunner: %v", err)
	}
	if _, ok := runner.(*cliDescriptionRunner); !ok {
		t.Fatalf("expected the CLI runner, got %T", runner)
	}
	if !strings.Contains(label, "/bin/cat") {
		t.Errorf("the run should announce what it describes with, got %q", label)
	}
}

// `--cli` takes an optional value, which the flag package cannot express on its own. Both
// spellings have to survive the pre-pass: bare, and with a quoted command.
func TestExpandCLIFlagValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"bare", []string{"--cli"}, []string{"--cli"}},
		{"with a command", []string{"--cli", "codex exec"}, []string{"--cli=codex exec"}},
		{"single dash", []string{"-cli", "codex exec"}, []string{"-cli=codex exec"}},
		{"already joined", []string{"--cli=codex exec"}, []string{"--cli=codex exec"}},
		{"followed by a flag", []string{"--cli", "--parallel", "2"}, []string{"--cli", "--parallel", "2"}},
		{"among others", []string{"--parallel", "2", "--cli", "claude -p", "-y"},
			[]string{"--parallel", "2", "--cli=claude -p", "-y"}},
		{"after a terminator", []string{"--", "--cli", "x"}, []string{"--", "--cli", "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := expandCLIFlagValue(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("expandCLIFlagValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The three states of the flag: absent, bare (the default, which must ask), and given.
func TestCLICommandFlagStates(t *testing.T) {
	parse := func(args ...string) *cliCommandFlag {
		t.Helper()
		f := &cliCommandFlag{}
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.Var(f, "cli", "")
		fs.Bool("parallel", false, "")
		if err := fs.Parse(expandCLIFlagValue(args)); err != nil {
			t.Fatalf("parse %q: %v", args, err)
		}
		return f
	}
	if f := parse(); f.set {
		t.Error("no --cli must leave the transport to the config")
	}
	if f := parse("--cli"); !f.set || f.command != "" {
		t.Errorf("bare --cli = %+v, want set with no command", f)
	}
	if f := parse("--cli", "codex exec"); !f.set || f.command != "codex exec" {
		t.Errorf(`--cli "codex exec" = %+v`, f)
	}
}

// The flag overrides whatever the config says, and a bare one is confirmed before it spends
// anything. -y stands in for the answer here; the prompt itself needs a terminal.
func TestApplyCLIOverride(t *testing.T) {
	cfg := helper.ResolvedLazyDescriptions{Provider: "anthropic", Model: "claude-haiku-4-5"}
	if err := applyCLIOverride(&cfg, "codex exec", false); err != nil {
		t.Fatalf("an explicit command must not need confirming: %v", err)
	}
	if cfg.Provider != helper.ProviderNameCLI {
		t.Errorf("provider = %q, want %q", cfg.Provider, helper.ProviderNameCLI)
	}
	if want := []string{"codex", "exec"}; !reflect.DeepEqual(cfg.CLICommand, want) {
		t.Errorf("CLICommand = %q, want %q", cfg.CLICommand, want)
	}

	// The default, confirmed.
	bare := helper.ResolvedLazyDescriptions{Provider: "anthropic"}
	if err := applyCLIOverride(&bare, "", true); err != nil {
		t.Fatalf("applyCLIOverride: %v", err)
	}
	if want := []string{"claude", "-p"}; !reflect.DeepEqual(bare.CLICommand, want) {
		t.Errorf("the bare default = %q, want %q", bare.CLICommand, want)
	}

	if err := applyCLIOverride(&cfg, `claude "oops`, false); err == nil {
		t.Error("an unparseable command must be an error, not a truncated argv")
	}
}

// Nothing is spent on a guess. With no terminal to ask, the bare default refuses and names the
// two spellings that need no answer; with a terminal, a "no" cancels and a "y" proceeds.
func TestBareCLIDefaultIsConfirmed(t *testing.T) {
	realTerminal, realReader := stdinIsTerminal, promptReader
	t.Cleanup(func() { stdinIsTerminal, promptReader = realTerminal, realReader })

	stdinIsTerminal = func() bool { return false }
	unattended := helper.ResolvedLazyDescriptions{}
	err := applyCLIOverride(&unattended, "", false)
	if err == nil {
		t.Fatal("an unconfirmed default must not run")
	}
	if !strings.Contains(err.Error(), "claude -p") {
		t.Errorf("the refusal should name the command to pass explicitly, got %v", err)
	}
	if unattended.Provider != "" {
		t.Errorf("a refused override must not have changed the transport, got %q", unattended.Provider)
	}

	stdinIsTerminal = func() bool { return true }
	for _, answer := range []string{"n\n", "\n", ""} {
		promptReader = strings.NewReader(answer)
		declined := helper.ResolvedLazyDescriptions{}
		if err := applyCLIOverride(&declined, "", false); err == nil {
			t.Errorf("answering %q must cancel", answer)
		}
		if declined.CLICommand != nil {
			t.Errorf("answering %q must leave the transport alone", answer)
		}
	}
	promptReader = strings.NewReader("y\n")
	accepted := helper.ResolvedLazyDescriptions{}
	if err := applyCLIOverride(&accepted, "", false); err != nil {
		t.Fatalf("answering yes must proceed: %v", err)
	}
	if want := []string{"claude", "-p"}; !reflect.DeepEqual(accepted.CLICommand, want) {
		t.Errorf("CLICommand = %q, want %q", accepted.CLICommand, want)
	}
}

// The bar is resolved once, from the pending count and the terminal, before any wave starts.
func TestShowDescriptionProgress(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       string
		isTerminal bool
		pending    int
		want       bool
	}{
		{"auto on a terminal with work", helper.ProgressAuto, true, 12, true},
		{"auto with a single resource", helper.ProgressAuto, true, 1, true},
		{"auto with nothing pending", helper.ProgressAuto, true, 0, false},
		{"auto when piped", helper.ProgressAuto, false, 12, false},
		{"always when piped", helper.ProgressAlways, false, 12, true},
		{"never on a terminal", helper.ProgressNever, true, 12, false},
		{"unset reads as auto", "", true, 12, true},
		{"a misspelling reads as auto", "  ALWAYs-ish ", false, 12, false},
		{"case and padding are forgiven", "  NEVER ", true, 12, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := showDescriptionProgress(tc.mode, tc.isTerminal, tc.pending); got != tc.want {
				t.Fatalf("showDescriptionProgress(%q, %v, %d) = %v, want %v", tc.mode, tc.isTerminal, tc.pending, got, tc.want)
			}
		})
	}
}

// blockingRunner answers one batch immediately and holds every other one until the test lets
// go, which is how a wave whose batches finish at different times is written down.
type blockingRunner struct {
	release chan struct{}
	first   sync.Once
	done    chan struct{} // closed once the fast batch has been answered
}

func (r *blockingRunner) Run(batch []descriptionResource, _ *domain.Topology, _ int) (string, error) {
	fast := false
	r.first.Do(func() {
		fast = true
		close(r.done)
	})
	if fast {
		return "wrote " + batch[0].ID, nil
	}
	<-r.release
	return "wrote " + batch[0].ID, nil
}

// The bar has to move when a batch lands, not when the wave ends. The wave used to wait for
// every worker before draining its results, which is invisible to a bar -- so this pins the
// draining: with one batch answered and the rest still running, the bar is already past zero.
func TestBatchWaveAdvancesTheBarBeforeTheWaveEnds(t *testing.T) {
	runner := &blockingRunner{release: make(chan struct{}), done: make(chan struct{})}
	batches := [][]descriptionResource{
		{{ID: "fn:a", Name: "A", Kind: domain.ResourceFunction}},
		{{ID: "fn:b", Name: "B", Kind: domain.ResourceFunction}},
		{{ID: "fn:c", Name: "C", Kind: domain.ResourceFunction}},
	}

	var bar progress.Reporter
	bar.SetOutput(io.Discard)
	bar.SetEnabled(true)
	bar.StartPhase("describing", len(batches))

	collected := make(chan []descriptionBatchResult, 1)
	go func() { collected <- runDescriptionBatchWave(runner, batches, 3, nil, 0, &bar) }()

	<-runner.done
	// The fast batch is answered; the other two are still blocked. Give the collector a
	// moment to see it, then assert the bar moved while the wave is demonstrably unfinished.
	deadline := time.After(2 * time.Second)
	for bar.Done() == 0 {
		select {
		case <-deadline:
			t.Fatal("the bar never advanced while batches were still running")
		case <-time.After(time.Millisecond):
		}
	}

	close(runner.release)
	select {
	case results := <-collected:
		if len(results) != len(batches) {
			t.Fatalf("the wave collected %d results, want %d", len(results), len(batches))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wave never finished")
	}
	if got := bar.Done(); got != len(batches) {
		t.Fatalf("bar advanced to %d, want %d", got, len(batches))
	}
}

// A batch that fails still advances the bar: it spent the wave's wall clock, and what it cost
// is reported by the summary and by the next wave re-listing what is still missing.
func TestBatchWaveCountsFailedBatches(t *testing.T) {
	runner := failingRunner{}
	batches := [][]descriptionResource{
		{{ID: "fn:a"}, {ID: "fn:b"}},
		{{ID: "fn:c"}},
	}

	var bar progress.Reporter
	bar.SetOutput(io.Discard)
	bar.SetEnabled(true)
	bar.StartPhase("describing", 3)

	results := runDescriptionBatchWave(runner, batches, 2, nil, 0, &bar)
	if len(results) != 2 {
		t.Fatalf("collected %d results, want 2", len(results))
	}
	if got := bar.Done(); got != 3 {
		t.Fatalf("bar advanced to %d, want 3 -- failures count too", got)
	}
}

type failingRunner struct{}

func (failingRunner) Run([]descriptionResource, *domain.Topology, int) (string, error) {
	return "", errors.New("executor blew up")
}
