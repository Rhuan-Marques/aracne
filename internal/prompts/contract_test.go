package prompts

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// allModes is every mode a project can be in. A test that loops over it fails when a fifth is
// added without deciding what its contract says, which is the failure mode that produced the
// rework: a combination nobody wrote a contract for still rendered one.
var allModes = []string{
	helper.ModeMCP,
	helper.ModeCLI,
	helper.ModeInterceptID,
	helper.ModeInterceptLineRanges,
}

// allVerbosities is both settings of the one dial that is NOT the mode.
//
// Every mode invariant below loops over it, and that is the point of the unification: the long
// contract is assembled from the same mode switch as the terse one, so "does this sentence
// still hold in mode N?" has to keep having one answer per mode, not one per mode per
// verbosity. A rule that held only at ContractVerbosityLow was a rule the second document was
// free to break -- which is how a second document goes stale unnoticed.
var allVerbosities = []string{
	helper.ContractVerbosityLow,
	helper.ContractVerbosityHigh,
}

// testLanguages is a topology in more than one language, so the loops below exercise the
// per-language sections rather than the language-free fallback.
var testLanguages = []string{"go", "python", "rust", "java", "typescript", "javascript"}

func contractFor(mode string) string { return contractForAt(mode, helper.ContractVerbosityLow) }

func contractForAt(mode, verbosity string) string {
	cfg := helper.DefaultConfig()
	cfg.Mode = mode
	cfg.ContractVerbosity = verbosity
	return ContractContent(cfg, testLanguages)
}

// Every contract opens with the same heading and closes on a line cli can find, because those
// two are what `arac init` uses to REPLACE its block instead of stacking a second copy, and
// what `arac disable` uses to remove it. A contract that loses either silently starts
// duplicating itself on every init.
//
// The marker is always a real line of the document. An invisible delimiter would be easier to
// match and is the wrong trade: this text is a prompt, re-sent on every request, and a token
// the model can see but cannot use is noise in it.
func TestEveryContractIsFindableByInit(t *testing.T) {
	for _, mode := range allModes {
		for _, verbosity := range allVerbosities {
			got := contractForAt(mode, verbosity)
			if !strings.HasPrefix(got, "# Aracne\n") {
				t.Errorf("mode %q at %q: contract does not open with the heading init looks for\n%s",
					mode, verbosity, got)
			}
			last := ""
			for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
				if strings.TrimSpace(line) != "" {
					last = strings.TrimSpace(line)
				}
			}
			if last != "Good Luck in your task." && last != AracneReadClosingLine {
				t.Errorf("mode %q at %q: closes on %q, which cli cannot find", mode, verbosity, last)
			}
		}
	}
}

// findAracIntegrationEnd takes the EARLIEST closing marker it finds after the opening heading,
// so a contract that ends on one marker while carrying the other in its BODY makes `arac init`
// replace only the first half of its own block -- and append the rest on every run after that.
//
// The long contract is where this becomes possible: it says everything the terse one says, at
// length, so a batching paragraph phrased as the terse ModeCLI contract's last line would land
// mid-document.
func TestNoContractCarriesTheOtherClosingMarkerInItsBody(t *testing.T) {
	for _, mode := range allModes {
		for _, verbosity := range allVerbosities {
			got := contractForAt(mode, verbosity)
			body := strings.TrimRight(got, "\n")
			if i := strings.LastIndex(body, "\n"); i >= 0 {
				body = body[:i]
			}
			for _, marker := range []string{"Good Luck in your task.", AracneReadClosingLine} {
				if strings.Contains(body, marker) {
					t.Errorf("mode %q at %q: closing marker %q appears mid-document; init would cut the block short\n%s",
						mode, verbosity, marker, got)
				}
			}
		}
	}
}

// What the graph IS still has to be said in every mode -- an agent that does not know a
// topology exists cannot reach for it.
func TestEveryContractSaysWhatTheGraphIs(t *testing.T) {
	for _, mode := range allModes {
		for _, verbosity := range allVerbosities {
			got := contractForAt(mode, verbosity)
			// The terse ModeCLI contract says it in prose rather than by naming the
			// file, because the path is not something the model ever types there.
			if !strings.Contains(got, ".aracne/topology.db") &&
				!strings.Contains(got, "every function, type, interface and variable is indexed") {
				t.Errorf("mode %q at %q: contract never says a topology exists\n%s", mode, verbosity, got)
			}
		}
	}
}

// No contract may name an MCP tool: in the three non-MCP modes there is no server to call, and
// in ModeMCP each tool's own description says how to call it. Naming one in prose is how the
// contract and the tool list drift.
func TestNoContractNamesAnMCPTool(t *testing.T) {
	for _, mode := range allModes {
		for _, verbosity := range allVerbosities {
			got := contractForAt(mode, verbosity)
			if strings.Contains(got, "mcp__aracne__") || strings.Contains(got, "aracne_read_resource") {
				t.Errorf("mode %q at %q: contract names an MCP tool by identifier\n%s", mode, verbosity, got)
			}
		}
	}
}

// Only the two intercepting modes may promise an enriched shell read. The `arac read` modes say
// the opposite -- that ordinary reads run as themselves -- and a contract that says both would
// leave the model testing which is true.
func TestOnlyInterceptingModesPromiseEnrichedShellReads(t *testing.T) {
	const promise = "the answer comes back enriched"
	for _, mode := range allModes {
		want := mode == helper.ModeInterceptID || mode == helper.ModeInterceptLineRanges
		for _, verbosity := range allVerbosities {
			if got := strings.Contains(contractForAt(mode, verbosity), promise); got != want {
				t.Errorf("mode %q at %q: promises enriched shell reads = %v, want %v",
					mode, verbosity, got, want)
			}
		}
	}
}

// ModeInterceptLineRanges dropped the resource-ID vocabulary on measured evidence: the model used an ID as
// a command operand 0 times in 408 commands. Re-adding a mention anywhere in its contract --
// including the `arac read <id>` bullet under "Other" -- spends bytes re-teaching exactly what
// the mode exists to retire.
func TestLineRangeContractNeverTeachesResourceIDs(t *testing.T) {
	for _, verbosity := range allVerbosities {
		got := contractForAt(helper.ModeInterceptLineRanges, verbosity)
		for _, forbidden := range []string{"Resource ID", "resource ID", "arac read <id>", "## Resource IDs"} {
			if strings.Contains(got, forbidden) {
				t.Errorf("intercept_line_ranges contract at %q verbosity mentions %q\n%s",
					verbosity, forbidden, got)
			}
		}
		if !strings.Contains(got, "src/parser.rs:940-1080") {
			t.Errorf("intercept_line_ranges contract at %q verbosity does not show a span to read:\n%s",
				verbosity, got)
		}
	}
}

// The guard note is per-request cost for a guard that fires in one mode. blocked_tools does
// nothing outside ModeMCP, so a paragraph explaining a denial belongs nowhere else.
func TestTheGuardNoteAppearsOnlyWhereAGuardCanFire(t *testing.T) {
	for _, mode := range allModes {
		cfg := helper.DefaultConfig()
		cfg.Mode = mode
		cfg.LLM.Any.MainAgent.BlockedTools = []string{"read", "grep"}

		// The two modes whose guard can actually refuse something: ModeMCP, and
		// ModeCLI where `arac read` is a surface a refusal can point at. The
		// intercepting modes answer the command instead, so a guard note there would explain
		// a denial that never comes.
		want := mode == helper.ModeMCP || mode == helper.ModeCLI
		for _, verbosity := range allVerbosities {
			cfg.ContractVerbosity = verbosity
			if got := strings.Contains(ContractContent(cfg, testLanguages), "## Tool Guard"); got != want {
				t.Errorf("mode %q at %q verbosity: carries a Tool Guard note = %v, want %v",
					mode, verbosity, got, want)
			}
		}
	}

	// And not even there when nothing is actually blocked.
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	cfg.LLM.Any.MainAgent.BlockedTools = nil
	if strings.Contains(ContractContent(cfg, testLanguages), "## Tool Guard") {
		t.Error("mcp contract explains a guard that denies nothing")
	}
}

// The three modes that TEACH a search have to say what it additionally reaches, because that
// is the half a plain grep cannot do and the model has no way to discover.
//
// ModeCLI is deliberately absent. A shell grep is rewritten to the annotated grep in
// every mode (Config.InterceptGrep), so the capability arrives whether or not the contract
// names it -- and naming a second spelling of something already automatic costs bytes on every
// request. The gap that leaves is real and small: a shape the guard will not rewrite
// (`grep … | wc -l`, `grep -o`) falls back to plain grep, and there the model never learns the
// annotated one exists.
func TestContractsThatTeachSearchSayWhatItReaches(t *testing.T) {
	for _, mode := range []string{helper.ModeMCP, helper.ModeInterceptID, helper.ModeInterceptLineRanges} {
		for _, verbosity := range allVerbosities {
			if !strings.Contains(contractForAt(mode, verbosity), "node names and stored descriptions") {
				t.Errorf("mode %q at %q: contract never explains what grep additionally searches\n%s",
					mode, verbosity, contractForAt(mode, verbosity))
			}
		}
	}
	for _, verbosity := range allVerbosities {
		if strings.Contains(contractForAt(helper.ModeCLI, verbosity), "arac grep") {
			t.Errorf("cli at %q verbosity names a subcommand the model gets for free; those bytes ship on every request",
				verbosity)
		}
	}
}

// An unrecognized mode must still render a whole contract. Config.EffectiveMode normalizes it,
// and a contract builder that returned "" for a typo would strip the project's guidance without
// saying anything.
func TestAnUnknownModeStillRendersAContract(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.Mode = "wat"
	got := ContractContent(cfg, testLanguages)
	// EffectiveMode normalizes a typo to the DEFAULT mode, which is cli -- so the
	// assertion is on the properties every contract must have, not on one mode's wording.
	if !strings.HasPrefix(got, "# Aracne\n") {
		t.Errorf("an unknown mode rendered a contract init cannot find:\n%s", got)
	}
	if !strings.Contains(got, "Good Luck") && !strings.Contains(got, AracneReadClosingLine) {
		t.Errorf("an unknown mode rendered a contract with no findable ending:\n%s", got)
	}
	if got != contractFor(helper.ModeCLI) {
		t.Error("an unknown mode must render exactly the default mode's contract")
	}

	// Same for a typo'd verbosity, and for the same reason: the fallback must be the cheap
	// document, not the four-times-larger one.
	cfg = helper.DefaultConfig()
	cfg.ContractVerbosity = "verbose"
	if ContractContent(cfg, testLanguages) != contractForAt(helper.ModeCLI, helper.ContractVerbosityLow) {
		t.Error("an unknown contract_verbosity must render exactly the low contract")
	}
}

// ---------------------------------------------------------------------------
// One document, three surfaces
// ---------------------------------------------------------------------------

// CLAUDE.md, AGENTS.md and the system prompt aracne's own harness sends are the same bytes.
//
// They were not. The first two came from ContractContent; the third came from five
// per-language constants under internal/llm/languages that no other surface could see. Both
// described the topology, the read output and how to spend a context window, so every change to
// what a read RETURNS had to be made twice -- and the copy nobody was reading was the one that
// went stale. This test is what makes a third spelling impossible to add quietly.
func TestEverySurfaceRendersTheSameDocument(t *testing.T) {
	for _, mode := range allModes {
		for _, verbosity := range allVerbosities {
			cfg := helper.DefaultConfig()
			cfg.Mode = mode
			cfg.ContractVerbosity = verbosity
			claude := ClaudeMdForConfig(cfg, testLanguages)
			agents := AgentsMdForConfig(cfg, testLanguages)
			system := SystemPromptForConfig(cfg, testLanguages)
			if claude != agents {
				t.Errorf("mode %q at %q: CLAUDE.md and AGENTS.md have drifted apart", mode, verbosity)
			}
			if claude != system {
				t.Errorf("mode %q at %q: CLAUDE.md and the harness system prompt have drifted apart",
					mode, verbosity)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The verbosity dial
// ---------------------------------------------------------------------------

// The dial has to actually move, in every mode. A "high" that renders the terse contract for
// some mode is the worst outcome available: the project pays nothing extra and gets none of the
// guidance it asked for, and nothing says so.
func TestHighVerbosityIsSubstantiallyLongerInEveryMode(t *testing.T) {
	for _, mode := range allModes {
		low := len(contractForAt(mode, helper.ContractVerbosityLow))
		high := len(contractForAt(mode, helper.ContractVerbosityHigh))
		if high <= low*2 {
			t.Errorf("mode %q: high contract is %d bytes against low's %d -- the dial barely moves",
				mode, high, low)
		}
	}
}

// Low is the default, and has to stay so. Every byte of the contract is re-sent on every
// request, and the long one is several times the size: a project that gets it without asking
// pays for it on every turn of every session.
func TestTheDefaultVerbosityIsLow(t *testing.T) {
	cfg := helper.DefaultConfig()
	if got := cfg.EffectiveContractVerbosity(); got != helper.ContractVerbosityLow {
		t.Errorf("default contract_verbosity is %q, want %q", got, helper.ContractVerbosityLow)
	}
	if ContractContent(cfg, testLanguages) != lowContract(cfg) {
		t.Error("the default config does not render the low contract")
	}
}

// ---------------------------------------------------------------------------
// The language half
// ---------------------------------------------------------------------------

// The long contract teaches the language the topology is actually in, and only that one.
// Writing Go's ID spelling into a Python project's CLAUDE.md is the same class of failure as a
// contract naming a tool the mode does not have: it costs a turn per attempt and nothing
// reports it.
func TestTheHighContractTeachesOnlyTheProjectsLanguages(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.ContractVerbosity = helper.ContractVerbosityHigh
	got := ContractContent(cfg, []string{"python"})
	if !strings.Contains(got, "Python") {
		t.Errorf("a Python project's contract never names Python:\n%s", got)
	}
	for _, other := range []string{"crate name", "fully-qualified names rooted", "module-path-prefixed"} {
		if strings.Contains(got, other) {
			t.Errorf("a Python project's contract teaches %q, which belongs to another language\n%s",
				other, got)
		}
	}
}

// `arac init` legitimately runs before the first scan, so the languages list is empty on a
// fresh checkout. The contract still has to be a whole contract: everything in it that is true
// of every language is still true, and a builder that returned "" for a missing language would
// strip the project's guidance without saying anything.
func TestTheHighContractSurvivesAnUnknownOrMissingLanguage(t *testing.T) {
	for _, languages := range [][]string{nil, {}, {"cobol"}, {"", "  "}} {
		cfg := helper.DefaultConfig()
		cfg.ContractVerbosity = helper.ContractVerbosityHigh
		got := ContractContent(cfg, languages)
		if !strings.HasPrefix(got, "# Aracne\n") || !strings.HasSuffix(got, "Good Luck in your task.\n") {
			t.Errorf("languages %v rendered a contract init cannot find:\n%s", languages, got)
		}
		if !strings.Contains(got, "## Guidelines") {
			t.Errorf("languages %v rendered a contract with no guidelines:\n%s", languages, got)
		}
	}
}

// The description workflow is the one section that names tools rather than a vocabulary, so it
// may only render where those tools exist -- which is ModeMCP alone. In the other three the job
// is `arac descriptions generate`, run by a person, and there is no node_list_no_description in
// the model's tool list to start from.
func TestOnlyTheMCPContractTeachesTheDescriptionWorkflow(t *testing.T) {
	for _, mode := range allModes {
		for _, verbosity := range allVerbosities {
			got := contractForAt(mode, verbosity)
			for _, tool := range []string{"node_list_no_description", "update_description"} {
				named := strings.Contains(got, tool)
				want := mode == helper.ModeMCP && verbosity == helper.ContractVerbosityHigh
				if named != want {
					t.Errorf("mode %q at %q: names %q = %v, want %v", mode, verbosity, tool, named, want)
				}
			}
		}
	}
}
