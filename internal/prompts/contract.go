package prompts

import (
	"strings"

	"aracne/internal/helper"
)

// The four contracts, one per mode.
//
// WHY ONE FILE. These texts are the only place the modes are described to a model, and the
// failure they exist to prevent is a contract that names a capability the mode does not have:
// an `mcp__aracne__grep` in a project with no MCP server, an intercepted `cat` in a project
// that does not intercept reads, a resource ID in the mode that stopped advertising them. Each
// of those costs a turn, and each is invisible until the model tries it. Keeping the four side
// by side is what makes "does this sentence still hold in mode N?" a question you can answer by
// reading down the page.
//
// SECOND RULE: every byte here is re-sent on every request. The MCP contract's job is done by
// the tool schemas, and an intercepting contract's job is done by the output itself; what
// survives in each is only what the model cannot work out from what it is already shown.

// ContractContent renders the contract for a project's mode.
func ContractContent(cfg *helper.Config) string {
	var b strings.Builder
	b.WriteString(contractIntro())
	switch cfg.EffectiveMode() {
	case helper.ModeMCP:
		b.WriteString(mcpHowItReaches())
	case helper.ModeAracneRead:
		b.WriteString(aracneReadHowItReaches())
	case helper.ModeInterceptID:
		b.WriteString(interceptHowItReaches())
		b.WriteString(interceptResourceIDs())
	default:
		b.WriteString(interceptHowItReaches())
		b.WriteString(interceptLineRanges())
	}
	b.WriteString(contractGuardNote(cfg))
	b.WriteString(contractOther(cfg))
	b.WriteString(behavioralRulesSection())
	b.WriteString(endingSection())
	return b.String()
}

func contractIntro() string {
	return "# Aracne\n\n" +
		"`.aracne/topology.db` holds a pre-analyzed graph of this repo: every function, type, " +
		"interface and variable, where it is declared, a one-line description, and what it " +
		"references.\n\n"
}

// mcpHowItReaches is deliberately the shortest of the four.
//
// Every tool carries its own description, and repeating argument shapes and result semantics
// here was the bulk of the old 5.8 KB contract for no measurable gain. What a schema cannot say
// is which QUESTION to ask -- a symbol rather than the file it sits in -- so that is all this
// says, plus the one capability that is not a tool at all.
func mcpHowItReaches() string {
	return "## How it reaches you\n\n" +
		"Aracne's capabilities arrive as MCP tools; each tool's own description says how to " +
		"call it. **Prefer a symbol over a file:** reading a declaration returns its source, " +
		"its imports, and a `# CONTEXT:` list of the neighbours it touches with their " +
		"descriptions -- usually the answer, for a fraction of a file's tokens.\n\n" +
		"`grep` is answered from the topology however you run it, and additionally searches " +
		"node names and stored descriptions -- so a plain-English query finds code that never " +
		"says the word.\n\n"
}

// aracneReadHowItReaches teaches one subcommand, because in this mode that is the whole of the
// aracne read surface: nothing intercepts a `cat`, and there is no tool schema to lean on.
func aracneReadHowItReaches() string {
	return "## How it reaches you\n\n" +
		"`arac read <id> <id> ...` returns those declarations with their source, their imports " +
		"and a `# CONTEXT:` list of what they touch -- several in one call, grouped by file " +
		"under a single context section. **Reach for it before opening the file:** a symbol " +
		"carries its neighbours and their descriptions for a fraction of the file's tokens.\n\n" +
		"```\narac read internal/cli.RunGuard app.Flask.register_blueprint\n```\n\n" +
		"A unique trailing part of an ID is enough, and a miss returns the nearest candidates " +
		"rather than an error. `arac grep <pattern>` searches node names and stored " +
		"descriptions as well as file contents, so a plain-English query finds code that never " +
		"says the word.\n\n" +
		"Ordinary reads (`cat`, `head`, `sed -n`) run as the plain command.\n\n"
}

// interceptHowItReaches is shared by the two intercepting modes: the mechanism is identical and
// only the vocabulary of the answer differs, which the section after this one supplies.
func interceptHowItReaches() string {
	return "## How it reaches you\n\n" +
		"Read files the way you normally would -- `cat`, `head -40`, `sed -n '80,120p'`. When " +
		"the target is indexed, the answer comes back enriched: the lines you asked for, the " +
		"signature of the declaration they sit inside, and a `# CONTEXT:` list of what they " +
		"touch. `grep` is answered the same way, and additionally searches node names and " +
		"stored descriptions -- so a plain-English query finds code that never says the word.\n\n" +
		"Flags aracne does not model, and files it does not index, run as the plain command.\n\n"
}

// interceptResourceIDs is ModeInterceptID's addressing section.
//
// It is the more expensive of the two -- an ID is vocabulary the model has to learn, where a
// line range is vocabulary it already has -- and it earns that only if the steer lands. So the
// steer is stated once, concretely, as the thing a search result hands back ready to reuse.
func interceptResourceIDs() string {
	return "## Resource IDs\n\n" +
		"Every declaration has an ID -- `internal/cli.RunGuard`, " +
		"`src/flask/app.Flask.register_blueprint`, `crate::args::Args::parse`. " +
		"**Any read command takes an ID where it takes a path:**\n\n" +
		"```\n" +
		"cat internal/cli.RunGuard      # the function, with the resources it touches\n" +
		"head -20 app.Flask             # the first 20 lines of the class body\n" +
		"grep 'retry' internal/http.Client   # search inside one resource\n" +
		"```\n\n" +
		"A unique trailing part is enough (`Flask.register_blueprint`), and a miss returns the " +
		"nearest candidates rather than an error. **Reach for an ID before a file.** An ID " +
		"cannot land mid-declaration and does not go stale when the file shifts, and it comes " +
		"back with the neighbours' descriptions attached. Search results and `# CONTEXT:` " +
		"entries print the ID of every declaration they name -- feed those straight back.\n\n"
}

// interceptLineRanges is ModeLineRange's addressing section, and the shortest addressing
// section of the three that have one.
//
// The section it replaces spent about a third of the file teaching resource IDs. Measured over
// ab-prefer-ids-20260902a the model used one 0 times in 408 commands, so those bytes bought
// nothing and were re-sent on every turn. What is left is the only claim the model cannot
// derive: that the ranges it is handed are exact, and that reading one gets the whole
// declaration with its context -- which is what makes re-reading a file to find an edge
// unnecessary.
func interceptLineRanges() string {
	return "## Line ranges\n\n" +
		"Search results and `# CONTEXT:` entries name each declaration by the exact lines it " +
		"spans, e.g. `src/parser.rs:940-1080`. **Reading that range is the cheapest move " +
		"available:**\n\n" +
		"```\nsed -n '940,1080p' src/parser.rs\n```\n\n" +
		"Reading a declaration's exact range returns the whole thing -- its imports, its " +
		"enclosing type, and a `# CONTEXT:` list of what it touches. You do not need to " +
		"locate it first, and you do not need to guess how far it runs: the range already " +
		"says. Widening a range by trial and error is the one habit this replaces.\n\n"
}

// contractGuardNote appears only where a guard can actually deny something, which since the
// mode rework is ModeMCP and nowhere else.
//
// A standing paragraph about a guard that never fires is pure per-request cost. But when
// blocked_tools IS set, a refusal arrives with no explanation of why an ordinary `cat` was
// turned down, and the model spends a turn testing spellings to find out.
func contractGuardNote(cfg *helper.Config) string {
	if !cfg.GuardBlocksNativeReads() {
		return ""
	}
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
		"This project denies shell and native " + strings.Join(gated, " and ") + " calls that " +
		"aracne can answer instead (`" + strings.Join(gated, "`/`") + "` in its " +
		"`blocked_tools`). The refusal carries the answer or names the tool that has it.\n\n"
}

// contractOther carries the two facts that hold in every mode, plus the batch-read bullet
// wherever naming `arac read <id>` does not undo the mode's own steer.
//
// It is dropped under ModeLineRange for exactly that reason -- advertising the id form there
// would spend bytes re-teaching the vocabulary that mode exists to retire -- and under ModeMCP,
// where the read tool's own schema already says it batches.
func contractOther(cfg *helper.Config) string {
	batch := ""
	if cfg.EffectiveMode() == helper.ModeInterceptID {
		batch = "- `arac read <id> <id> ...` reads several resources in one call, grouped by " +
			"file under a single context section.\n"
	}
	return "## Other\n\n" + batch +
		"- Your edits keep the graph current automatically; act on any topology warning that " +
		"comes back.\n\n"
}
