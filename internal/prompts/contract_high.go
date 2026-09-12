package prompts

import (
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// The long contract: ContractVerbosityHigh.
//
// WHAT IT IS. The same document as the terse contract in contract.go, said at length. It is
// assembled from the SAME mode switch, because the rule that produced four modes has not
// changed: a contract that names a capability the mode does not have costs a turn per attempt
// and is invisible until the model tries it. Verbosity decides how much is said about a
// capability; the mode decides whether it may be named at all. Nothing here re-decides a mode.
//
// WHY IT EXISTS. It is the text aracne's own harness used to send as five per-language system
// prompts under internal/llm/languages. That harness has no system prompt of its own and no
// tool descriptions worth leaning on, so it needed every word: the read output format, the ID
// vocabulary, the language's semantics, the full guidelines. None of that was ever
// harness-specific -- a project running a weaker model through Claude Code wants the same
// paragraphs -- so it is a dial now rather than a second document.
//
// WHAT IT COSTS. Every byte is re-sent on every request, and this is roughly four times the
// terse contract. That is the whole reason ContractVerbosityLow is the default: on a harness
// that already tells the model how to read code, most of this is paid for twice.

// highContract renders the long contract for a project's mode and languages.
func highContract(cfg *helper.Config, languages []string) string {
	profiles := profilesFor(languages)
	var b strings.Builder
	// The H1 is how `arac setup` finds this block again to REPLACE it and how `arac disable`
	// finds it to remove it, in every mode and at either verbosity.
	b.WriteString("# Aracne\n\n")
	b.WriteString(highIntro(profiles))
	switch cfg.EffectiveMode() {
	case helper.ModeMCP:
		b.WriteString(highMCPHowItReaches())
	case helper.ModeInterceptID:
		b.WriteString(highInterceptHowItReaches())
		b.WriteString(highResourceIDs())
	case helper.ModeInterceptLineRanges:
		b.WriteString(highInterceptHowItReaches())
		b.WriteString(highLineRanges())
	default:
		b.WriteString(highCLIHowItReaches(profiles))
	}
	// How an ID is SPELLED is worth saying in three of the four modes, not just the one whose
	// addressing section is about IDs: ModeMCP's read tool takes them and ModeCLI's
	// `arac read` takes nothing else, and a model that has to infer Java's mandatory
	// parameter signature or Rust's `::` paths from a search result spends turns doing it.
	// ModeInterceptLineRanges is the exception, and the only one -- see highLineRanges.
	if !cfg.LineRangeIdentification() {
		b.WriteString(highLanguageIDs(profiles))
	}
	b.WriteString(highReadOutput(cfg, profiles))
	b.WriteString(highDescriptionGeneration(cfg))
	b.WriteString(contractGuardNote(cfg))
	b.WriteString(highLanguageSemantics(profiles))
	b.WriteString(highGuidelines())
	// The same closing line the terse contract uses, and the same end marker
	// (cli.AracIntegrationEnd). The long contract never ends on
	// AracneReadClosingLine and must never contain that sentence anywhere in its body:
	// findAracIntegrationEnd takes the EARLIEST marker it finds, so a stray copy mid-document
	// would make init replace only the first half of its own block.
	b.WriteString(endingSection())
	return b.String()
}

// highIntro says what the graph is, in the language the project is actually written in.
//
// Naming the kinds it holds is the part that earns its bytes: it is what stops an agent asking
// for something the graph has never modeled -- a Java package node, a Go generic
// instantiation -- and reading the miss as "aracne is broken" rather than "that is not a
// resource here".
func highIntro(profiles []languageProfile) string {
	subject := "this project"
	if names := languageNames(profiles); names != "" {
		subject = "this " + names + " project"
	}
	graph := "every declaration in it, where it is declared, a one-line description, and what it references"
	if len(profiles) == 1 {
		graph = profiles[0].Graph + ", with a one-line description on each and the references between them"
	}
	return "`.aracne/topology.db` holds a pre-analyzed graph of " + subject + ": " + graph +
		".\n\nIt is the source of truth about this codebase, and it re-syncs after every " +
		"edit. You never need to parse code by hand to work out what calls what, and you " +
		"never need to ask for a re-scan.\n\n"
}

// highMCPHowItReaches is the long form of mcpHowItReaches.
//
// It still does not enumerate the tools. Each tool ships its own description in its schema, so
// a list here is bytes paid twice and one more place for the contract and the tool set to
// drift -- which is the failure the four-mode rework was for. What a schema cannot say is which
// QUESTION to ask and how much of the answer to believe, so that is what this says instead.
func highMCPHowItReaches() string {
	return "## How it reaches you\n\n" +
		"Aracne's capabilities arrive as MCP tools, and each tool's own description says how " +
		"to call it. Two things the schemas cannot tell you:\n\n" +
		"**Prefer a symbol over a file.** Reading a declaration returns its source, the " +
		"imports it needs, and a `# CONTEXT:` list of the neighbours around it with their " +
		"descriptions -- BOTH what it touches and what implements, subclasses or uses it. " +
		"Usually the whole answer, for a fraction of a file's tokens. Read a " +
		"whole file only for a config, an unsupported language, or when you genuinely need " +
		"all of it.\n\n" +
		"**Batch your reads.** The read tool takes a LIST. Pass every ID you already know you " +
		"need in ONE call: results are grouped by file under a single context section, and " +
		"every call re-sends the whole conversation, so one read of three IDs costs far less " +
		"than three reads of one.\n\n" +
		"`grep` is answered from the topology however you run it, and additionally searches " +
		"node names and stored descriptions -- so a plain-English query finds code that never " +
		"says the word.\n\n"
}

// highCLIHowItReaches is the long form of aracneReadContract's body.
//
// It teaches one subcommand, because in ModeCLI that is the whole of the aracne read
// surface: nothing intercepts a `cat`, and there is no tool schema to lean on.
//
// It deliberately does not name `arac grep`. A shell `grep` is rewritten to the annotated grep
// in EVERY mode (Config.InterceptGrep), so the capability arrives whether or not the model
// knows a second spelling exists, and naming one costs bytes on every request.
func highCLIHowItReaches(profiles []languageProfile) string {
	return "## How it reaches you\n\n" +
		"`arac read <id> <id> ...` returns declarations with their source, the imports they " +
		"need, and the context around them -- BOTH what they touch and what implements, " +
		"subclasses or uses them. It takes a LIST: pass every ID you already know " +
		"you need in ONE call, because the results are grouped by file under a single context " +
		"section and one batched call costs far less than one call per ID.\n\n" +
		"```\n" + readExample(profiles) + "\n```\n\n" +
		"**Prefer it over opening files.** A symbol read returns the declaration and the " +
		"descriptions of everything it touches, for a fraction of a file's tokens; a whole " +
		"file is for a config, an unsupported language, or when you genuinely need all of " +
		"it. A unique trailing part of an ID is enough, and a miss returns the nearest " +
		"candidates rather than an error.\n\n" +
		"`grep` is answered from the topology however you run it, and additionally searches " +
		"node names and stored descriptions -- so a plain-English query finds code that never " +
		"says the word.\n\n"
}

// readExample writes the `arac read` line in the IDs of the project's OWN languages.
//
// A generic example is not free here: an ID form is exactly what this mode's contract is
// teaching, so demonstrating Go's dotted module path to a Java project shows the wrong answer
// in the one place the model is most likely to copy from. With several languages it takes one
// ID from each of the first two, which is also what a real call into a mixed repo looks like.
func readExample(profiles []languageProfile) string {
	switch len(profiles) {
	case 0:
		// No scan yet, or no scanner for this project's language. There is no honest ID to
		// show, so the example shows the shape instead.
		return "arac read <id> <id>"
	case 1:
		return "arac read " + profiles[0].Examples[0] + " " + profiles[0].Examples[1]
	default:
		return "arac read " + profiles[0].Examples[0] + " " + profiles[1].Examples[0]
	}
}

// highInterceptHowItReaches is the long form shared by the two intercepting modes. The
// mechanism is identical in both and only the vocabulary of the answer differs, which the
// addressing section after this one supplies.
func highInterceptHowItReaches() string {
	return "## How it reaches you\n\n" +
		"Read files the way you normally would -- `cat`, `head -40`, `sed -n '80,120p'`. When " +
		"the target is indexed, the answer comes back enriched: the lines you asked for, the " +
		"signature of the declaration they sit inside, and a `# CONTEXT:` list of what they " +
		"touch, and of what implements or uses them, each with its description. `grep` is " +
		"answered the same way, and " +
		"additionally searches node names and stored descriptions -- so a plain-English query " +
		"finds code that never says the word.\n\n" +
		"There is nothing to opt into and no second spelling to learn: the command you " +
		"already type is the command that gets the better answer. Flags aracne does not " +
		"model, and files it does not index, run as the plain command.\n\n"
}

// highResourceIDs is ModeInterceptID's addressing section, with the language's own ID spelling
// folded in.
//
// This is the more expensive of the two addressing sections -- an ID is vocabulary the model
// has to learn, where a line range is vocabulary it already has -- so it earns its place only
// if the steer lands. It is stated concretely, as the thing a search result hands back ready
// to reuse.
func highResourceIDs() string {
	return "## Resource IDs\n\n" +
		"Every declaration has an ID, and **any read command takes an ID where it takes a " +
		"path**:\n\n" +
		"```\n" +
		"cat internal/cli.RunGuard           # the function, with the resources it touches\n" +
		"head -20 app.Flask                  # the first 20 lines of the class body\n" +
		"grep 'retry' internal/http.Client   # search inside one resource\n" +
		"```\n\n" +
		"**Reach for an ID before a file.** An ID cannot land mid-declaration, it does not go " +
		"stale when the file shifts, and it comes back with its neighbours' descriptions " +
		"attached. Search results and `# CONTEXT:` entries print the ID of every declaration " +
		"they name -- feed those straight back. `arac read <id> <id> ...` reads several at " +
		"once, grouped by file under a single context section.\n\n"
}

// highLanguageIDs says how this project's languages spell an ID.
//
// It is a section of its own rather than a paragraph inside one mode's addressing section
// because three modes want it: the MCP read tool takes IDs, `arac read` takes nothing else,
// and an intercepted `cat` takes them where it takes a path. Only ModeInterceptLineRanges
// leaves it out, deliberately.
func highLanguageIDs(profiles []languageProfile) string {
	var b strings.Builder
	for _, p := range profiles {
		if p.IDs == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("## How an ID is spelled\n\n" +
				"**Try the bare name first.** Any unique trailing part of an ID resolves on its " +
				"own -- `RunGuard`, `register_blueprint` -- and an ambiguous or unknown one comes " +
				"back with the matching candidates rather than an error, so guessing costs nothing " +
				"and never needs a search first. The full spelling is for disambiguating.\n\n")
		}
		b.WriteString("**" + p.Display + ".** " + p.IDs + "\n\n")
	}
	return b.String()
}

// highLineRanges is ModeInterceptLineRanges's addressing section.
//
// It teaches no ID vocabulary, and no ADDRESSING section in this mode may. The mode exists to
// retire that vocabulary on measured evidence -- the model used an ID as a command operand 0
// times in 408 commands -- so spending the long contract's extra room on re-teaching it would
// undo the one thing the mode is for. The claim left is the only one the model cannot derive:
// that the spans it is handed are exact.
//
// A read's own output is a different matter: readunit.withLocations prints the span BESIDE the
// id rather than in place of it, so highReadOutput shows what the renderer really emits. The
// mode stops advertising ids as the way to address code; it does not pretend they are gone.
func highLineRanges() string {
	return "## Line ranges\n\n" +
		"Search results and `# CONTEXT:` entries name each declaration by the exact lines it " +
		"spans, e.g. `src/parser.rs:940-1080`. **Reading that range is the cheapest move " +
		"available:**\n\n" +
		"```\nsed -n '940,1080p' src/parser.rs\n```\n\n" +
		"Reading a declaration's exact span returns the whole thing -- its imports, its " +
		"enclosing type, and a `# CONTEXT:` list of what it touches. You do not need to " +
		"locate it first, and you do not need to guess how far it runs: the span already " +
		"says. Widening a range by trial and error is the one habit this replaces.\n\n"
}

// highReadOutput teaches the shape a read comes back in, so the model can plan around the
// answer instead of re-reading to find its edges.
//
// It is the single largest reason the long contract exists. The `# CONTEXT:` block is not
// something a tool schema or an enriched `cat` explains -- the model sees it, has to guess
// what the indentation and the parentheticals mean, and guesses that a nested line is
// something it must go and read. Saying "anything shown as source is never repeated below,
// and a CONTEXT entry is already the answer for most questions" is what stops the recursive
// read-everything walk.
//
// In ModeInterceptLineRanges the entries carry `path:start-end` beside the id
// (Config.LineRangeIdentification), so the language's plain ID-keyed example would be wrong
// there and a span-carrying one is rendered instead.
//
// The example has to match readunit.withLocations exactly, and it did not: it showed the span
// keying the line with the id gone, while the renderer keeps BOTH -- the id ties the entry to
// the symbol the reader just saw in the code above it, the span says how to fetch it. A
// documented shape the tool never emits costs the model the re-derivation the long contract is
// paid to save.
func highReadOutput(cfg *helper.Config, profiles []languageProfile) string {
	var b strings.Builder
	b.WriteString("## What a read returns\n\n" +
		"Two sections. First one fenced code block per file, holding that file's pooled " +
		"imports and every requested declaration in it -- a method comes with its enclosing " +
		"type when the type is small enough to inline. Then ONE `# CONTEXT:` section for the " +
		"whole call, describing everything the requested declarations connect to in BOTH " +
		"directions -- what they touch, and what implements, subclasses or uses them. " +
		"**Anything already shown as source above is never repeated in CONTEXT.**\n\n")
	if cfg.LineRangeIdentification() {
		b.WriteString("Each CONTEXT entry carries the exact span the declaration occupies, in " +
			"parentheses after its name -- ready to read:\n\n" +
			"```\n" +
			"# CONTEXT:\n" +
			"## mycrate::shapes::Circle (src/shapes.rs:12-48): Description\n" +
			"    mycrate::shapes::Circle::new (src/shapes.rs:50-58): Description\n" +
			"## mycrate::factory::make_circle (src/factory.rs:9-31): Description\n" +
			"```\n\n")
	} else {
		for _, p := range profiles {
			if p.Output != "" {
				b.WriteString(p.Output + "\n")
			}
		}
		b.WriteString("Each CONTEXT entry is keyed by the declaration's FULL ID -- pass one " +
			"straight back to read to drill in. An indented line is a member of the entry " +
			"above it; a parenthetical says how the two are related.\n\n")
	}
	b.WriteString("**A CONTEXT entry is usually already the answer.** It carries the " +
		"neighbour's description, which is what tells you whether that neighbour matters. Do " +
		"NOT walk the graph reading every entry: drill in only when the task requires " +
		"changing or deeply understanding that specific dependency.\n\n")
	return b.String()
}

// highDescriptionGeneration is the description-writing workflow, and the one section gated on
// something other than the mode's vocabulary: it names two tools, so it may only render where
// those tools exist.
//
// That is ModeMCP alone. In the other three the same job is a command (`arac descriptions
// generate`) run by a person, and the model has no `node_list_no_description` to call -- so
// this paragraph there would be a workflow whose first step is a tool that is not in the list.
//
// And even there each tool is named only if the MAIN agent is served it. The default main
// profile is `read` and `warnings_list` -- the two tools are the descriptions executor's, not
// the main agent's -- so naming them sent the model to a first step it could not take. Where
// the main agent lacks one, the section names the `arac` command that does the same job.
func highDescriptionGeneration(cfg *helper.Config) string {
	if !cfg.MCPEnabled() {
		return ""
	}
	list := "`arac resource list --no-description`"
	if mainAgentServes(cfg, "node_list_no_description") {
		list = "`node_list_no_description`"
	}
	record := "`arac update-description <id> <kind> \"<description>\"`"
	if mainAgentServes(cfg, "update_description") {
		record = "`update_description`"
	}
	return "## Writing descriptions\n\n" +
		"When asked to document the codebase, start from " + list + " -- it is " +
		"the list of resources the graph is missing a description for. Split it into batches " +
		"of `max-batch-size` (`llm.<any>.agents.descriptions-generation-executor.params` in " +
		"`.aracne/config.json`, default 5) and give each batch to one descriptions-generation-executor sub-agent " +
		"where the platform has sub-agents; otherwise work the batches yourself. Re-check " +
		list + " when the batches finish and retry anything still listed.\n\n" +
		"For each batch: read every assigned ID in ONE call, then write each description by " +
		"hand -- 1-3 lines for a function, type or interface, 1 line for a variable, file or " +
		"package -- and record it with " + record + ". Process every targeted resource; " +
		"do not skip any.\n\n"
}

// mainAgentServes reports whether the main agent is served the MCP tool name on every harness
// the contract is written for. One contract goes to both CLAUDE.md and AGENTS.md, so a tool only
// one of the two main agents has is not one it may promise.
func mainAgentServes(cfg *helper.Config, name string) bool {
	for _, harness := range []string{"claude_code", "opencode"} {
		served := false
		for _, tool := range cfg.EffectiveAgent(harness, "main").MCPTools {
			served = served || tool == name
		}
		if !served {
			return false
		}
	}
	return true
}

// highLanguageSemantics is the one language section that is never mode-gated.
//
// Nothing in it is vocabulary the model has to type: it is what the edges MEAN -- what
// implements what, how visibility works, what the scanner could and could not resolve. An
// agent that does not know a language's call edges are best-effort reads an empty CONTEXT as
// "nothing calls this" and deletes live code.
func highLanguageSemantics(profiles []languageProfile) string {
	var b strings.Builder
	for _, p := range profiles {
		if p.Semantics == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("## How the graph models this codebase\n\n")
		}
		b.WriteString("**" + p.Display + ".** " + p.Semantics + "\n\n")
	}
	return b.String()
}

// highGuidelines is the long form of behavioralRulesSection.
//
// The terse contract's four rules are all here; the extra ones are the habits that cost the
// most when they go wrong and that no tool schema teaches -- the recursive read-everything
// walk, and the re-read of a file that has already been answered.
func highGuidelines() string {
	return `## Guidelines

1. **Prefer a declaration over a file, and a read over a search.** A symbol read is precise
   and comes with its
   neighbours; a whole file is for a config, an unsupported language, or when you genuinely
   need all of it.
2. **Ask for everything you need at once.** Every call re-sends the whole conversation, so one
   read of three declarations costs far less than three reads of one. The same goes for edits.
3. **Descriptions are usually sufficient.** The ` + "`# CONTEXT:`" + ` block already tells you what a
   neighbour is for. Drill into one only when the task requires changing or deeply
   understanding it.
4. **Do not re-read.** If it is already in your context, use it.
5. **Trust the topology.** It is the source of truth and it re-syncs after every edit. Never
   parse code by hand to work out what calls what, and never ask for a re-scan.
6. **Act on the warnings.** An edit that breaks a reference reports it. Those warnings name
   real work somewhere else in the codebase; do not leave them behind.
7. **Do not guess.** Report an empty result or an error as what it is. Never invent code,
   descriptions or relationships.
8. **Be concise.** Report what you found and what you changed, not how you did it.

`
}
