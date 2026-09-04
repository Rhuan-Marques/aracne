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
	helper.ModeAracneRead,
	helper.ModeInterceptID,
	helper.ModeLineRange,
}

func contractFor(mode string) string {
	cfg := helper.DefaultConfig()
	cfg.Mode = mode
	return ContractContent(cfg)
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
		got := contractFor(mode)
		if !strings.HasPrefix(got, "# Aracne\n") {
			t.Errorf("mode %q: contract does not open with the heading init looks for\n%s", mode, got)
		}
		last := ""
		for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				last = strings.TrimSpace(line)
			}
		}
		if last != "Good Luck in your task." && last != AracneReadClosingLine {
			t.Errorf("mode %q: closes on %q, which cli cannot find", mode, last)
		}
	}
}

// What the graph IS still has to be said in every mode -- an agent that does not know a
// topology exists cannot reach for it.
func TestEveryContractSaysWhatTheGraphIs(t *testing.T) {
	for _, mode := range allModes {
		got := contractFor(mode)
		// ModeAracneRead says it in prose rather than by naming the file, because the path is
		// not something the model ever types there.
		if !strings.Contains(got, ".aracne/topology.db") &&
			!strings.Contains(got, "every function, type, interface and variable is indexed") {
			t.Errorf("mode %q: contract never says a topology exists\n%s", mode, got)
		}
	}
}

// No contract may name an MCP tool: in the three non-MCP modes there is no server to call, and
// in ModeMCP each tool's own description says how to call it. Naming one in prose is how the
// contract and the tool list drift.
func TestNoContractNamesAnMCPTool(t *testing.T) {
	for _, mode := range allModes {
		if got := contractFor(mode); strings.Contains(got, "mcp__aracne__") || strings.Contains(got, "aracne_read_resource") {
			t.Errorf("mode %q: contract names an MCP tool by identifier\n%s", mode, got)
		}
	}
}

// Only the two intercepting modes may promise an enriched shell read. The `arac read` modes say
// the opposite -- that ordinary reads run as themselves -- and a contract that says both would
// leave the model testing which is true.
func TestOnlyInterceptingModesPromiseEnrichedShellReads(t *testing.T) {
	const promise = "the answer comes back enriched"
	for _, mode := range allModes {
		want := mode == helper.ModeInterceptID || mode == helper.ModeLineRange
		if got := strings.Contains(contractFor(mode), promise); got != want {
			t.Errorf("mode %q: promises enriched shell reads = %v, want %v", mode, got, want)
		}
	}
}

// ModeLineRange dropped the resource-ID vocabulary on measured evidence: the model used an ID as
// a command operand 0 times in 408 commands. Re-adding a mention anywhere in its contract --
// including the `arac read <id>` bullet under "Other" -- spends bytes re-teaching exactly what
// the mode exists to retire.
func TestLineRangeContractNeverTeachesResourceIDs(t *testing.T) {
	got := contractFor(helper.ModeLineRange)
	for _, forbidden := range []string{"Resource ID", "resource ID", "arac read <id>"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("line_range contract mentions %q\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "src/parser.rs:940-1080") {
		t.Errorf("line_range contract does not show a span to read:\n%s", got)
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
		// ModeAracneRead where `arac read` is a surface a refusal can point at. The
		// intercepting modes answer the command instead, so a guard note there would explain
		// a denial that never comes.
		want := mode == helper.ModeMCP || mode == helper.ModeAracneRead
		if got := strings.Contains(ContractContent(cfg), "## Tool Guard"); got != want {
			t.Errorf("mode %q: carries a Tool Guard note = %v, want %v", mode, got, want)
		}
	}

	// And not even there when nothing is actually blocked.
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	cfg.LLM.Any.MainAgent.BlockedTools = nil
	if strings.Contains(ContractContent(cfg), "## Tool Guard") {
		t.Error("mcp contract explains a guard that denies nothing")
	}
}

// The three modes that TEACH a search have to say what it additionally reaches, because that
// is the half a plain grep cannot do and the model has no way to discover.
//
// ModeAracneRead is deliberately absent. A shell grep is rewritten to the annotated grep in
// every mode (Config.InterceptGrep), so the capability arrives whether or not the contract
// names it -- and naming a second spelling of something already automatic costs bytes on every
// request. The gap that leaves is real and small: a shape the guard will not rewrite
// (`grep … | wc -l`, `grep -o`) falls back to plain grep, and there the model never learns the
// annotated one exists.
func TestContractsThatTeachSearchSayWhatItReaches(t *testing.T) {
	for _, mode := range []string{helper.ModeMCP, helper.ModeInterceptID, helper.ModeLineRange} {
		if !strings.Contains(contractFor(mode), "node names and stored descriptions") {
			t.Errorf("mode %q: contract never explains what grep additionally searches\n%s",
				mode, contractFor(mode))
		}
	}
	if strings.Contains(contractFor(helper.ModeAracneRead), "arac grep") {
		t.Error("aracne_read names a subcommand the model gets for free; those bytes ship on every request")
	}
}

// An unrecognized mode must still render a whole contract. Config.EffectiveMode normalizes it,
// and a contract builder that returned "" for a typo would strip the project's guidance without
// saying anything.
func TestAnUnknownModeStillRendersAContract(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.Mode = "wat"
	got := ContractContent(cfg)
	// EffectiveMode normalizes a typo to the DEFAULT mode, which is aracne_read -- so the
	// assertion is on the properties every contract must have, not on one mode's wording.
	if !strings.HasPrefix(got, "# Aracne\n") {
		t.Errorf("an unknown mode rendered a contract init cannot find:\n%s", got)
	}
	if !strings.Contains(got, "Good Luck") && !strings.Contains(got, AracneReadClosingLine) {
		t.Errorf("an unknown mode rendered a contract with no findable ending:\n%s", got)
	}
	if got != contractFor(helper.ModeAracneRead) {
		t.Error("an unknown mode must render exactly the default mode's contract")
	}
}
