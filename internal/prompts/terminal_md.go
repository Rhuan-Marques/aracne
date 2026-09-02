package prompts

import (
	"strings"

	"aracne/internal/helper"
)

// The terminal-surface contract.
//
// WHY IT IS THIS SHORT. The MCP contract has to teach a tool the model has never seen: its
// name, its argument shape, what its output section means. None of that applies here -- the
// model already knows `head`, and aracne answers it. What is left is the only thing it cannot
// work out on its own, which is that resources have IDs and that an ID is a cheaper question
// than a path. Every byte of this file is re-sent on every request, so anything the model
// would have done anyway is not worth stating.
//
// The one non-obvious claim it makes is the safety one: commands aracne does not model, and
// files it does not index, behave exactly as they always did. Without that a model that hits
// one passthrough starts second-guessing every read.

// TerminalContractContent renders the contract for a project on the terminal surface.
func TerminalContractContent(cfg *helper.Config) string {
	var b strings.Builder
	b.WriteString(terminalIntro())
	b.WriteString(terminalHowItReaches(cfg))
	b.WriteString(terminalAddressing(cfg))
	b.WriteString(terminalGuardNote(cfg))
	b.WriteString(terminalOther(cfg))
	b.WriteString(behavioralRulesSection())
	b.WriteString(endingSection())
	return b.String()
}

func terminalIntro() string {
	return "# Aracne\n\n" +
		"`.aracne/topology.db` holds a pre-analyzed graph of this repo: every function, type, " +
		"interface and variable, where it is declared, a one-line description, and what it " +
		"references.\n\n"
}

func terminalHowItReaches(cfg *helper.Config) string {
	if !cfg.EffectiveEnhanceFiles() {
		// Files are served raw here, so promising enriched reads would be a lie the model
		// would spend turns testing.
		return "## How it reaches you\n\nRead files however you like. `arac read <id>` and " +
			"`arac grep <pattern>` query the graph directly.\n\n"
	}
	search := ""
	if cfg.EffectiveTerminalGrep() {
		search = " `grep` is answered the same way, and additionally searches node names and " +
			"stored descriptions -- so a plain-English query finds code that never says the word."
	}
	return "## How it reaches you\n\n" +
		"Read files the way you normally would -- `cat`, `head -40`, `sed -n '80,120p'`. When " +
		"the target is indexed, the answer comes back enriched: the lines you asked for, the " +
		"signature of the declaration they sit inside, and a `# CONTEXT:` list of what they " +
		"touch." + search + "\n\n" +
		"Flags aracne does not model, and files it does not index, run as the plain command.\n\n"
}

// terminalAddressing tells the model how aracne NAMES things, which is the one thing it cannot
// work out on its own. Which form it teaches is identification_mode.
func terminalAddressing(cfg *helper.Config) string {
	if cfg.LineRangeIdentification() {
		return terminalLineRanges()
	}
	return terminalResourceIDs(cfg)
}

// terminalLineRanges is the default contract, and it is deliberately short.
//
// The section it replaces spent about a third of the file teaching resource IDs. Measured over
// ab-prefer-ids-20260902a the model used one 0 times in 408 commands, so those bytes bought
// nothing and were re-sent on every turn -- 792 tokens/turn for the whole contract, which is
// 0.91 turns per task. What is left is the only claim the model cannot derive: that the ranges
// it is handed are exact, and that reading one gets the whole declaration with its context.
func terminalLineRanges() string {
	return "## Line ranges\n\n" +
		"Search results and `# CONTEXT:` entries name each declaration by the exact lines it " +
		"spans, e.g. `src/parser.rs:940-1080`. **Reading that range is the cheapest move " +
		"available:**\n\n" +
		"```\nsed -n '940,1080p' src/parser.rs\n```\n\n" +
		"Reading a declaration's exact range returns the whole thing -- its imports, its " +
		"enclosing type, and a `# CONTEXT:` list of what it touches. You do not need to " +
		"locate it first, and you do not need to guess how far it runs: the range already " +
		"says.\n\n"
}

func terminalResourceIDs(cfg *helper.Config) string {
	if !cfg.EffectiveEnhanceResources() {
		return ""
	}
	b := &strings.Builder{}
	b.WriteString("## Resource IDs\n\n" +
		"Every declaration has an ID -- `internal/cli.RunGuard`, " +
		"`src/flask/app.Flask.register_blueprint`, `crate::args::Args::parse`. " +
		"**Any read command takes an ID where it takes a path:**\n\n" +
		"```\n" +
		"cat internal/cli.RunGuard      # the function, with the resources it touches\n" +
		"head -20 app.Flask             # the first 20 lines of the class body\n" +
		"grep 'retry' internal/http.Client   # search inside one resource\n" +
		"```\n\n" +
		"A unique trailing part is enough (`Flask.register_blueprint`), and a miss returns the " +
		"nearest candidates rather than an error.")
	// The steer, and the ONLY thing the ab-prefer-ids arms differ by. Everything above states
	// a capability the model cannot infer; this states a preference, and a preference is a
	// claim about which shape is cheaper -- which is measurable and currently unmeasured.
	if cfg.EffectivePreferResourceIDs() {
		b.WriteString(" **Reach for an ID before a line range.** An ID is exact where a line " +
			"number is a guess: it cannot land mid-declaration, it does not go stale when the " +
			"file shifts, and it comes back with the neighbours' descriptions attached. Every " +
			"search result prints the ID of the declaration each hit sits in -- feed that " +
			"straight back rather than translating it into a range.")
	}
	b.WriteString("\n\n")
	return b.String()
}

// terminalGuardNote appears only when the project actually denies something.
//
// Most terminal projects deny nothing -- interception is the mechanism, not the guard -- and a
// standing paragraph about a guard that never fires is pure per-request cost. But when
// blocked_tools IS set, a refusal arrives with no explanation of why an ordinary `cat` was
// turned down, and the model spends a turn testing spellings to find out.
func terminalGuardNote(cfg *helper.Config) string {
	blocked := cfg.EffectiveAgent("claude_code", "main").BlockedTools
	var gated []string
	for _, key := range blocked {
		if key == "read" || key == "grep" {
			gated = append(gated, key)
		}
	}
	if len(gated) == 0 {
		return ""
	}
	return "## Tool Guard\n\n" +
		"This project denies shell " + strings.Join(gated, " and ") + " commands that aracne " +
		"cannot answer (`" + strings.Join(gated, "`/`") + "` in its `blocked_tools`). The ones it " +
		"CAN answer are served, not refused -- so a refusal means that exact spelling has no " +
		"topology-backed form. The message names one that does.\n\n"
}

func terminalOther(cfg *helper.Config) string {
	// The `arac read <id>` bullet is ID vocabulary, so it is dropped under line_range: naming
	// the form this mode exists to stop advertising would undo the trim it is paying for.
	batch := "- `arac read <id> <id> ...` reads several resources in one call, grouped by file " +
		"under a single context section.\n"
	if cfg.LineRangeIdentification() {
		batch = ""
	}
	return "## Other\n\n" + batch +
		"- Your edits keep the graph current automatically; act on any topology warning that " +
		"comes back.\n\n"
}
