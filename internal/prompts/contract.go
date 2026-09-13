package prompts

import (
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
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

// ContractContent renders the contract for a project's mode, at its configured verbosity.
//
// This is the ONLY contract there is. CLAUDE.md, AGENTS.md and the system prompt aracne's own
// harness sends are the same document, produced here: the contract is surface-shaped, not
// harness-shaped, and the two things that legitimately vary -- which capabilities the mode has,
// and how much is said about them -- are both arguments to this function.
//
// languages are the topology's languages, most significant first, and may be empty: `arac setup`
// can run before the first scan. Only ContractVerbosityHigh reads them; the terse contract says
// nothing that changes with the language.
func ContractContent(cfg *helper.Config, languages []string) string {
	if cfg.EffectiveContractVerbosity() == helper.ContractVerbosityHigh {
		return highContract(cfg, languages)
	}
	return lowContract(cfg)
}

// lowContract renders ContractVerbosityLow: what the graph is, how it reaches this surface, and
// the one or two facts the model cannot derive from what it is already shown.
func lowContract(cfg *helper.Config) string {
	// ModeCLI is written as one continuous document rather than assembled from the
	// shared sections. It has one capability to teach and no tool schemas to lean on, so the
	// headings were structure around three paragraphs -- and every byte of them is re-sent on
	// every request.
	if cfg.EffectiveMode() == helper.ModeCLI {
		return aracneReadContract(cfg)
	}
	var b strings.Builder
	b.WriteString(contractIntro())
	switch cfg.EffectiveMode() {
	case helper.ModeMCP:
		b.WriteString(mcpHowItReaches())
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
		"call it.\n\n" +
		"**Prefer a symbol over a file.** Where something is defined, who calls or implements " +
		"it, what one declaration does inside a large file -- each of those is a read of the " +
		"SYMBOL, not a `grep`, a `cat` or a `find`. It returns the source, the imports, and a " +
		"`# CONTEXT:` list of what it touches AND what implements or uses it, each with its " +
		"description: usually the whole answer, for a fraction of a file's tokens.\n\n" +
		"`grep` is answered from the topology however you run it, and additionally searches " +
		"node names and stored descriptions -- so a plain-English query finds code that never " +
		"says the word.\n\n"
}

// aracneReadContract is the whole document for ModeCLI.
//
// It teaches one subcommand, because in this mode that is the whole of the aracne read
// surface: nothing intercepts a `cat`, and there is no tool schema to lean on.
//
// TWO THINGS IT DELIBERATELY NO LONGER SAYS.
//
// `arac grep` is gone. A shell `grep` is rewritten to the annotated grep in EVERY mode
// (Config.InterceptGrep), so the model gets node names and stored descriptions searched
// whether or not it knows the subcommand exists. Spending contract bytes -- re-sent every
// request -- to name a second spelling of something already automatic is the clearest case of
// paying twice. The gap that leaves is real and small: a shape the guard will not rewrite
// (`grep … | wc -l`, `grep -o`) falls back to plain grep and the model never learns the
// annotated one exists.
//
// "Ordinary reads (`cat`, `head`, `sed -n`) run as the plain command" is gone too. It was
// true, and it read as permission -- granted immediately after telling the model to prefer
// `arac read`. Nothing else here implies interception, so stating the default only invited the
// behaviour the paragraph above it argues against.
func aracneReadContract(cfg *helper.Config) string {
	var b strings.Builder
	// contractIntro carries the H1 -- how init finds this block again to REPLACE it, and how
	// `arac disable` finds it to remove it -- and the one sentence saying what the graph is.
	// Shared with the other three modes so the four cannot drift on the same fact.
	b.WriteString(contractIntro())
	b.WriteString(
		"`arac read <id> <id> ...` returns declarations with their source, their imports " +
			"and a `# CONTEXT:` list of what they touch AND what implements, subclasses or " +
			"uses them, each with its description. Several ids in one call.\n\n" +
			"**Reach for it the moment you want to know** where something is defined, who " +
			"calls or implements it, or what one declaration does inside a large file. Each " +
			"of those is `arac read <name>` -- not `grep -rn`, not `cat`, not `sed -n`, not " +
			"`find`. Open a whole file only for a config, an unsupported language, or when " +
			"you genuinely need all of it.\n\n" +
			"**The bare name is enough** -- any unique trailing part of an id resolves, and a " +
			"miss returns the nearest candidates, so guess rather than searching for one " +
			"first. To learn about the function `NotifyPlayers`, run " +
			"`arac read NotifyPlayers`.\n\n" +
			"A CONTEXT entry's description is usually already the answer; drill in only to " +
			"change or deeply understand that neighbour.\n\n" +
			"Edits re-sync the graph; act on any topology warning that comes back.\n\n")
	// Only when someone has opted into blocked_tools. A denial the contract has not explained
	// costs a turn to work out. Written BEFORE the closing line, because that line is what
	// init and disable find to locate the end of this block.
	b.WriteString(contractGuardNote(cfg))
	// The last line, and the block's end marker. It is a real instruction rather than a
	// delimiter dressed as one: `arac setup` needs to find where its block stops so a re-run
	// replaces it instead of stacking a second copy, and every candidate for that job is
	// re-sent to the model on every request -- so it had better be a line worth sending.
	b.WriteString(AracneReadClosingLine + "\n")
	return b.String()
}

// AracneReadClosingLine is the final line of the ModeCLI contract, and the marker cli
// uses to find the end of the generated block. Exported so the two cannot drift: a change to
// the wording here without a matching change there would make every `arac setup` append a
// second contract instead of replacing the first.
const AracneReadClosingLine = "Parallelise multiple reads and edits in a single command when possible."

// interceptHowItReaches is shared by the two intercepting modes: the mechanism is identical and
// only the vocabulary of the answer differs, which the section after this one supplies.
func interceptHowItReaches() string {
	return "## How it reaches you\n\n" +
		"Read files the way you normally would -- `cat`, `head -40`, `sed -n '80,120p'`. When " +
		"the target is indexed, the answer comes back enriched: the lines you asked for, the " +
		"signature of the declaration they sit inside, and a `# CONTEXT:` list of what they " +
		"touch and of what implements or uses them. `grep` is answered the same way, and " +
		"additionally searches node names and " +
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
		"**Where is it defined? Who calls or implements it?** `cat` the ID -- do not `grep` " +
		"for the file first. An ID cannot land mid-declaration, does not go stale when the " +
		"file shifts, and arrives with its neighbours' descriptions.\n\n" +
		"**The bare name is usually enough** (`RunGuard`, `register_blueprint`): any unique " +
		"trailing part resolves, and a miss returns candidates rather than an error -- so " +
		"guess an ID rather than searching for one. Search results and `# CONTEXT:` entries " +
		"print the ID of every declaration they name; feed those straight back.\n\n"
}

// interceptLineRanges is ModeInterceptLineRanges's addressing section, and the shortest addressing
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
		"spans, e.g. `src/parser.rs:940-1080`. **Want that declaration? Read the span you " +
		"were handed -- do not search for it again:**\n\n" +
		"```\nsed -n '940,1080p' src/parser.rs\n```\n\n" +
		"That returns the whole thing -- its imports, its enclosing type, and a `# CONTEXT:` " +
		"list of what it touches. You never need to locate it first or guess how far it runs: " +
		"the range already says. Widening a range by trial and error is the one habit this " +
		"replaces.\n\n"
}

// toolNameSet turns a blocked_tools list into the set BlockableInMode takes.
func toolNameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
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
	// Filtered through BlockableInMode, which is what the guard actually enforces: it drops
	// `grep` outside ModeMCP, because search is intercepted in every mode and refusing it
	// would deny a call aracne is one step from answering. Reading the raw list here wrote
	// "denies shell and native read and grep calls" into a cli-mode CLAUDE.md and taught the
	// model, on every request, a restriction that does not exist.
	blocked := cfg.BlockableInMode(toolNameSet(cfg.EffectiveAgent("claude_code", "main").BlockedTools))
	var gated []string
	for _, key := range []string{"read", "grep"} {
		if blocked[key] {
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
// It is dropped under ModeInterceptLineRanges for exactly that reason -- advertising the id form there
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
