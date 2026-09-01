package prompts

import (
	"fmt"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/toolspec"
)

// Returns CLAUDE.md content tailored to the effective agent configuration for claude_code.
func ClaudeMdContent() string {
	return ClaudeMdContentForAgent(helper.DefaultConfig().EffectiveAgent("claude_code", "main"))
}

// Returns the agents.md content for the default OpenCode agent configuration.
func AgentsMdContent() string {
	return AgentsMdContentForAgent(helper.DefaultConfig().EffectiveAgent("opencode", "main"))
}

// ClaudeMdContentForAgent renders the CLAUDE.md guidance for a resolved agent
// config, describing each capability as an MCP tool or a native tool depending
// on the agent's mcp_tools / blocked_tools.
func ClaudeMdContentForAgent(eff helper.AgentConfig) string {
	return agentInstructionsContent(eff, "mcp__aracne__")
}

// AgentsMdContentForAgent renders the OpenCode AGENTS.md guidance.
func AgentsMdContentForAgent(eff helper.AgentConfig) string {
	return agentInstructionsContent(eff, "aracne_")
}

// Wraps a string in backticks to format it as inline code.
func bt(s string) string {
	return "`" + s + "`"
}

// --- tool-membership predicates -------------------------------------------

// Checks if a string exists in a list of strings.
func inList(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

// Checks if a given MCP tool name is enabled in the agent config.
func hasMCPTool(eff helper.AgentConfig, name string) bool { return inList(eff.MCPTools, name) }

// Checks whether a native tool is allowed by verifying it's not in the agent config's blocked tools list.
func nativeAllowed(eff helper.AgentConfig, key string) bool { return !inList(eff.BlockedTools, key) }

// usesMCPRead reports whether the agent has aracne's read tool.
func usesMCPRead(eff helper.AgentConfig) bool { return hasMCPTool(eff, "read") }

// readToolName is the name the read tool actually registers under for this agent. It takes the
// short name only when the harness's own read is blocked; otherwise the two would be
// confusable in one session.
func readToolName(eff helper.AgentConfig) string {
	return toolspec.ResolveReadToolName(nativeAllowed(eff, "read"))
}

// agentInstructionsContent assembles the contract the agent sees on every turn.
//
// Every byte here is re-sent on each request, so the guiding rule is: say only
// what the tool schemas cannot. Per-tool behaviour (arguments, match semantics,
// result caps) already ships in each tool's own description; repeating it here
// bought nothing and was the bulk of the old 5.8 KB contract. What survives is
// what no schema can express -- how the pieces fit together, how to spend
// turns, and which native tools this project actually allows.
func agentInstructionsContent(eff helper.AgentConfig, mcpToolPrefix string) string {
	var b strings.Builder

	b.WriteString(introductionSection())
	b.WriteString(navigationModelSection(eff))
	b.WriteString(lookupToolsSection(eff, mcpToolPrefix))
	b.WriteString(grepSection(eff, mcpToolPrefix))
	b.WriteString(resourceContextSection(eff))
	b.WriteString(editWriteSection(eff, mcpToolPrefix))
	b.WriteString(otherSection(eff, mcpToolPrefix))
	// The guard hook is a Claude Code feature (PreToolUse/PostToolUse);
	// OpenCode enforces the same intent through its permission block instead.
	if mcpToolPrefix == "mcp__aracne__" {
		b.WriteString(guardNoteSection())
	} else if blocksShellReadOrGrep(eff) {
		b.WriteString(openCodeGuardNoteSection())
	}
	b.WriteString(howToNavigateSection())
	b.WriteString(behavioralRulesSection())
	b.WriteString(endingSection())

	return b.String()
}

// Returns introductory text explaining aracne's role in codebase navigation.
func introductionSection() string {
	return "# Aracne\n\n" +
		"`.aracne/topology.db` holds a pre-analyzed graph of this repo's functions, types, " +
		"interfaces, variables and how they reference each other.\n\n"
}

// Generates the "Navigation Model" section: how to pick between an aracne
// lookup and the native read.
func navigationModelSection(eff helper.AgentConfig) string {
	b := &strings.Builder{}
	b.WriteString("## Navigation Model\n\n")

	if !usesMCPRead(eff) {
		b.WriteString("Use `ls` for the file layout. Use your `read` tool for files and resources.\n\n")
		return b.String()
	}

	b.WriteString("Use `ls` for the file layout, then a lookup tool to pull a resource with its " +
		"connected context in one call.\n\n")
	if nativeAllowed(eff, "read") {
		b.WriteString("**Which to use:** the aracne lookup for a *symbol* (its code plus its neighbours " +
			"and their descriptions -- usually cheaper than the whole file); your native read for a file " +
			"as a whole, a config, or a line range.\n\n")
	} else {
		b.WriteString("**The native read tool is blocked in this project** -- use the lookup. " +
			"It covers whole files too.\n\n")
	}
	return b.String()
}

// nativeGrepNote says whether the native grep remains available. Getting this wrong is
// expensive in both directions: claiming it works when the guard denies it costs a wasted
// turn per attempt, and forbidding it when it is allowed is what drove redundant reads.
func nativeGrepNote(eff helper.AgentConfig) string {
	if nativeAllowed(eff, "grep") {
		return " Your native grep also works; use whichever fits."
	}
	return " **The native grep tool is blocked in this project** -- use this one."
}

// lookupToolsSection names the available lookup tools and the one thing their
// schemas cannot state: how resource IDs resolve. Each tool's own description
// already says what it reads, so it is not repeated per bullet.
func lookupToolsSection(eff helper.AgentConfig, mcpToolPrefix string) string {
	if !usesMCPRead(eff) {
		return ""
	}

	b := &strings.Builder{}
	b.WriteString("## MCP Lookup tool:\n\n")

	fmt.Fprintf(b, "%s takes a LIST of resource IDs -- functions, methods, types, interfaces, files. "+
		"Pass every ID you need in one call: results are grouped by file under a single context "+
		"section, so one batched call costs far less than one call per ID.\n\n",
		bt(mcpToolPrefix+readToolName(eff)))

	b.WriteString("IDs are forgiving: a unique trailing part (`Flask.register_blueprint`) is enough, " +
		"and a miss returns the nearest candidates rather than an error.\n\n")
	return b.String()
}

// grepSection documents what aracne's grep adds over a plain one. The argument
// list lives in the tool's own schema.
func grepSection(eff helper.AgentConfig, mcpToolPrefix string) string {
	if hasMCPTool(eff, "grep") {
		return fmt.Sprintf("## Grep/Search\n\n%s searches node names, node descriptions and file "+
			"contents, ranked in that order, and names the enclosing node above its matches -- "+
			"which often answers the question with no follow-up read. Descriptions live only in "+
			"the topology, so a plain-English query finds nodes whose code never says it.%s\n\n",
			bt(mcpToolPrefix+"grep"), nativeGrepNote(eff))
	}
	if nativeAllowed(eff, "grep") {
		return "## Grep/Search\n\nUse your native `grep`. To search node names and descriptions " +
			"too, and get topology metadata on the matches, run `arac grep <pattern> [path]`.\n\n"
	}
	return ""
}

// resourceContextSection teaches the "# CONTEXT:" output format. The shape is
// worth its bytes: it is what lets the agent answer follow-ups without another
// call, which is the single largest turn saving available.
func resourceContextSection(eff helper.AgentConfig) string {
	// The "# CONTEXT:" output is produced only by the MCP read tools.
	if !usesMCPRead(eff) {
		return ""
	}

	return "## Resource Context\n\n" +
		"A lookup returns the source, then a `# CONTEXT:` tree of everything it touches, " +
		"keyed by resource ID and indented by depth:\n\n" +
		"```\n" +
		"# CONTEXT:\n" +
		"## pkg.Iface: description\n" +
		"    pkg.Impl: description\n" +
		"        pkg.(Impl).Method: description\n" +
		"## pkg.Callee: description\n" +
		"## pkg.Var = value\n" +
		"```\n\n" +
		"Answer follow-up questions from this tree instead of calling again. Pass an ID back to a " +
		"lookup only when you need that resource's actual code.\n\n"
}

// editWriteSection states which edit path is available. Match semantics and
// the empty-new_string delete are in the edit tool's own schema.
func editWriteSection(eff helper.AgentConfig, mcpToolPrefix string) string {
	if hasMCPTool(eff, "edit") || hasMCPTool(eff, "write") {
		note := "Your native edit/write also work -- an `arac update-file` hook re-syncs the topology " +
			"afterwards -- so the graph stays correct either way."
		if !nativeAllowed(eff, "edit") || !nativeAllowed(eff, "write") {
			note = "**The native edit/write tools are blocked in this project** -- use these."
		}
		batch := ""
		if hasMCPTool(eff, "edit") {
			// Benchmarking found the cost of the MCP edit path was not the topology sync but
			// the CALL COUNT: agents sent one edit per hunk while the same agent, given a
			// shell, batched ten replacements into a single heredoc. The tool takes an
			// `edits` array for exactly this, but a model that never reads past the first
			// parameter will not find it.
			batch = fmt.Sprintf(" Put every replacement for a change in ONE %s call via its "+
				"`edits` array -- it spans files, applies in order, and rolls back as a unit. "+
				"A call per hunk is the most expensive way to use it.", bt(mcpToolPrefix+"edit"))
		}
		return fmt.Sprintf("## Edit and Write:\n\n%s and %s update the topology inline. %s%s\n\n",
			bt(mcpToolPrefix+"edit"), bt(mcpToolPrefix+"write"), note, batch)
	}
	if nativeAllowed(eff, "edit") {
		return "## Edit and Write:\n\nUse your native `edit` and `write` tools; the topology re-syncs " +
			"automatically afterwards.\n\n"
	}
	return ""
}

// Generates the "Other" section with bug report and topology warnings guidance
// when those tools are available.
func otherSection(eff helper.AgentConfig, mcpToolPrefix string) string {
	hasBugReport := hasMCPTool(eff, "bug_report")
	hasWarnings := hasMCPTool(eff, "warnings_list")
	if !hasBugReport && !hasWarnings {
		return ""
	}

	b := &strings.Builder{}
	b.WriteString("## Other:\n\n")
	if hasBugReport {
		fmt.Fprintf(b, "- Found a bug outside your task? Do not fix it -- file it with %s.\n", bt(mcpToolPrefix+"bug_report"))
	}
	if hasWarnings {
		fmt.Fprintf(b, "- %s lists outstanding topology warnings.\n", bt(mcpToolPrefix+"warnings_list"))
	}
	b.WriteString("\n")
	return b.String()
}

// guardNoteSection tells the agent what the guard does when it fires. The full
// blocked_tools/pipe-exemption policy is configuration documentation, not
// per-turn agent guidance, and lives in the project README instead -- the agent
// only needs to know that a nudge is not a denial and that pipes are fine.
func guardNoteSection() string {
	return "## Tool Guard\n\n" +
		"An `arac guard` hook watches your calls. A native `Read`/`Grep`/`Edit`/`Write`, or a shell " +
		"equivalent (`cat`, `head`, `tail`, `less`, `grep`, `rg`, in-place `sed`/`awk`, `Get-Content`, " +
		"`Select-String`), returns a one-line pointer to the matching aracne tool. That is a " +
		"suggestion, not a denial -- anything this file has not called blocked still runs. Commands " +
		"reading piped output (`git log | tail`) are exempt.\n\n"
}

// openCodeGuardNoteSection is the OpenCode counterpart to guardNoteSection:
// OpenCode has no PreToolUse hook, so the same intent is enforced by the
// permission block, which denies the direct read/grep shell forms.
func openCodeGuardNoteSection() string {
	return "## Tool Guard\n\n" +
		"This project's OpenCode permissions deny `cat`/`head`/`tail`/`less`/`grep`/`rg` when run " +
		"directly on a file -- use the matching aracne tool. Reading piped output (`cmd | head`) is " +
		"still allowed.\n\n"
}

// blocksShellReadOrGrep reports whether the agent blocks the read or grep tool,
// which is what makes the OpenCode permission block deny those shell commands.
func blocksShellReadOrGrep(eff helper.AgentConfig) bool {
	for _, t := range eff.BlockedTools {
		if t == "read" || t == "grep" {
			return true
		}
	}
	return false
}

// howToNavigateSection is the turn-budget section.
//
// Benchmarking found the aracne arm spending 25% more turns than the baseline
// for the same work, and that -- not the size of any single result -- was the
// dominant cost: every extra turn re-sends the whole accumulated transcript.
// Two habits caused most of it, so both are named explicitly here: reading a
// file in successive small line ranges, and issuing lookups one turn at a time.
func howToNavigateSection() string {
	return `## How to Navigate:

1. **Prefer symbols over files.** Read a whole file when you want the file itself -- unsupported
   language, config, or you genuinely need all of it.
2. **Let descriptions decide.** A neighbour's description tells you whether it matters; if you only
   need to know *what* something does, the description is already the answer -- do not read it.
3. **Spend turns wide, not deep.** Every call re-sends the whole conversation, so one call for
   three IDs beats three calls for one: put every lookup you already know you need into a single
   read.
4. **Act on warnings.** An edit reports resources your change may have broken; resolve them as they
   appear.

`
}

// behavioralRulesSection carries only rules that change behaviour and are not
// stated anywhere else in the contract.
func behavioralRulesSection() string {
	return `## Behavioral Rules

1. **Be concise** -- report what you found and what you changed, not how you did it.
2. **Trust the topology** -- it is the source of truth and re-syncs after every edit. Never parse
   code by hand, and never ask for a re-scan.
3. **Do not guess** -- report an empty result or an error as what it is; never invent code or
   relationships.
4. **Do not re-read** -- if it is already in your context, use it.

`
}

// Returns closing goodbye text for generated prompt markdown
func endingSection() string {
	return "Good Luck in your task.\n"
}
