package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// Prints the full CLI usage documentation for all aracne commands and flags.
//
// The bug-pipeline blocks are omitted unless features.bug_management is on. `arac bug`
// stays dispatchable either way -- it is the orchestration channel the generated slash
// commands use and the debugging path -- but an undocumented command is the "unlisted"
// half of gating an unshipped feature.
func PrintUsage() { PrintUsageTo(os.Stdout) }

// PrintUsageTo writes the banner to a chosen stream. An unknown subcommand sends it to stderr,
// where a diagnostic belongs: on stdout it is indistinguishable from a command's output.
func PrintUsageTo(w io.Writer) {
	cfg := helper.LoadConfig(helper.ConfigPath(ProjectDBPath(DefaultDBRelative)))
	text := gateBugUsage(usageText, cfg.BugManagementEnabled())
	text = gateAgentUsage(text, cfg.AgentEnabled())
	fmt.Fprintln(w, gateVizUsage(text, vizBuilt))
}

// gateVizUsage removes the `viz serve` lines and their flag block when this binary was
// built without the visualizer. The Basic build must not advertise a command whose only
// possible answer is "wrong binary".
func gateVizUsage(text string, hasViz bool) string {
	if hasViz {
		return text
	}
	return dropUsageBlock(text, `Flags for "viz serve"`, func(trimmed string) bool {
		return strings.HasPrefix(trimmed, "arac viz ")
	})
}

// gateAgentUsage removes the `arac agent` lines when the feature is off. Like `arac bug`,
// the command stays dispatchable -- this hides it, it does not remove it.
func gateAgentUsage(text string, agent bool) string {
	if agent {
		return text
	}
	return dropUsageBlock(text, "", func(trimmed string) bool {
		return strings.HasPrefix(trimmed, "arac agent ")
	})
}

// dropUsageBlock removes every line drop() selects, plus -- when blockHeading is non-empty --
// the heading and the indented block that runs from it to the next blank line.
func dropUsageBlock(text, blockHeading string, drop func(trimmed string) bool) string {
	var kept []string
	skipBlock := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if blockHeading != "" && strings.HasPrefix(trimmed, blockHeading) {
			skipBlock = true
			continue
		}
		if skipBlock {
			if trimmed == "" {
				skipBlock = false
			}
			continue
		}
		if drop(trimmed) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// gateBugUsage removes the `arac bug` command lines and their two flag blocks from the
// usage banner when the bug pipeline is off.
func gateBugUsage(text string, bugManagement bool) string {
	if bugManagement {
		return text
	}
	var kept []string
	skipBlock := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		// A `Flags for "bug ..."` heading opens a block that runs to the next blank line.
		if strings.HasPrefix(trimmed, `Flags for "bug `) {
			skipBlock = true
			continue
		}
		if skipBlock {
			if trimmed == "" {
				skipBlock = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "arac bug ") {
			continue
		}
		// `--tool-profile` names the servable agent profiles; the bug ones are not servable
		// with the feature off (effectiveMCPToolSet strips their tools), so do not offer them.
		line = strings.Replace(line, "(bug-hunter, bug-judge, bug-solver, descriptions-generation-executor)",
			"(descriptions-generation-executor)", 1)
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

const usageText = `arac - Go/Python/JavaScript/TypeScript/Rust/Java project topology analyzer

Usage:
  arac scan     [flags]     Incremental scan (changed files only); --all for full re-scan, --hard for full rebuild
  arac agent    [prompt]    Run the AI coding agent
  arac serve    [flags]     Start MCP server (for OpenCode / Claude Code integration)
  arac viz serve [flags]    Start local topology graph visualization UI
  arac init     [flags]     Set this repository up: asks the setup questions, scans, writes the integration
  arac setup    [flags]     Write the integration files from the current config (--claude, --opencode, --global)
  arac disable  [flags]     Remove the aracne integration this project's setup wrote (--claude, --opencode, --global, --all)
  arac scanner run [--db <path>]  Watch the tree and keep the topology current in the background
  arac descriptions generate [flags]  Generate descriptions for targeted undocumented resources
  arac descriptions clear [flags]    Clear stored topology descriptions (--oversized: only over-budget ones)
  arac descriptions export [flags]   Back up descriptions to an ID-independent JSONL sidecar
  arac descriptions import [flags]   Restore descriptions from a sidecar after a re-scan
  arac read [--kind <kind>] [--full] <resource-id>...  Read one or more resources by ID; --kind forces exact kind (function, method, struct, named_type, interface, variable, file, package, dependency); --full returns whole file bodies under read.file_mode "skeleton"
  arac resource list [query] [--kind <kind>]... [--no-description]  List resources, optionally filtered by query, kind, or missing description
  arac grep [flags] <pattern> [path]  Search node names, node descriptions and file contents (ranked in that order)
  arac cmd -- <command> [args...]  Run a shell read or search (cat/head/tail/sed -n/grep...), answered from the topology where aracne can and by the real command where it cannot
  arac update-file <path>  Re-parse a file and update the topology database (--db to specify db path)
  arac update-description <id> <kind> <desc>  Update a resource's description in the topology DB
  arac node count            Print total node count
  arac node count --no-description  Print count of undocumented nodes
  arac warnings list [flags] List outstanding topology warnings
  arac bug report  [flags] Report a bug on a resource node
  arac bug list    [flags] List known bugs (filterable by node or state)
  arac bug acknowledge <bugID>  Mark a bug as acknowledged
  arac bug dismiss    <bugID>   Mark a bug as dismissed
  arac bug delete     <bugID>   Delete a bug from the database
  arac edit                 Edit a file (reads JSON from stdin: {"file_path", "old_string", "new_string"})
  arac write                Write a file (reads JSON from stdin: {"file_path", "content"})
  arac check-updates [--json] Index health: which files drifted from the topology since the last scan (exit 1 if stale)

Flags for "scan":
  -root <path>    Root folder of the project (default ".")
  -output <file>  Output SQLite database path (default ".aracne/topology.db")
  --all           Re-scan all files (preserves descriptions)
  --hard          Force full rebuild from scratch (clears descriptions and bugs)
  --default       Force the incremental scan (what a bare "arac scan" already does)
  --debug         Compare warnings before and after scan, print differences
  --verbose, -v   List the changed files detected during an incremental scan
  --workers <n>   Max files parsed concurrently during a full scan (0 = auto, one per CPU); lower to cap peak RAM
  --progress <m>  Progress bar: auto (default; on a terminal above 15 files), always, or never

Flags for "serve":
  --tool-profile <agent>    Agent whose tools to serve: main, all, or a configured agent name
                            (bug-hunter, bug-judge, bug-solver, descriptions-generation-executor)
  --harness <name>          Harness whose per-agent overrides apply: claude_code (default) or opencode

Flags for "viz serve":
  --db <path>      Topology database path (default ".aracne/topology.db")
  --addr <addr>    HTTP listen address (default "127.0.0.1:7331")

Flags for "grep":
  --db <path>      Topology database path (default ".aracne/topology.db")
  Which kinds may match on their description is set by grep.description_kinds in
  .aracne/config.json (default: function,method,struct,interface; [] disables it).

Flags for "descriptions generate":
  (Who writes the descriptions is asked by "arac init" and read from .aracne/config.json
   here. An unconfigured project is told to run it, and given the keys to write by hand;
   the sweep itself asks nothing. --cli answers for one run without saving.)
  --targets <kinds>       Comma-separated resource kinds overriding config descriptions.kinds (default: function,method,struct,interface,file)
  --batch-size <n>        Maximum resources assigned to each description executor (default 5)
  --parallel <n>          Maximum description executors to run concurrently (default 4)
  --max-retries <n>       Maximum executor attempts per resource (default 3)
  --progress <mode>       Progress bar: auto (on a terminal when there is anything to
                          describe), always, or never (default auto)
  --regen_oversized       Rewrite existing descriptions that overrun their kind's character
                          budget (function/method 120, type 100, variable 80) instead of
                          describing undocumented resources
  --cli [command]         Describe by running a command (prompt on stdin, descriptions on
                          stdout) instead of calling an API with a key. Quote a command with
                          flags: --cli "codex exec". Bare --cli runs "claude -p" and asks
                          first, because that spends your CLI subscription rather than an
                          API key -- possibly a separate pool of credits; check with your
                          provider. Overrides descriptions.provider for this run
  -y                      Auto-confirm the bare --cli prompt

Flags for "descriptions export":
  --out <path>            Sidecar to write (default .aracne/descriptions.jsonl)
  --db <path>             Topology database (default .aracne/topology.db)

Flags for "descriptions import":
  --in <path>             Sidecar to restore (default .aracne/descriptions.jsonl)
  --db <path>             Topology database (default .aracne/topology.db)
  --dry-run               Report what would be restored without writing
  --min-rate <0..1>       Exit 2 when the match rate falls below this
  --report <path>         Write unmatched records to this JSONL path

Flags for "descriptions clear":
  --target <kinds>        Comma-separated resource kinds to clear; omit to clear every description

Flags for "warnings list":
  --db <path>     Topology database path (default ".aracne/topology.db")
  --source <id>   Filter by source resource ID
  --target <id>   Filter by target resource ID
  --kind <kind>   Filter by warning kind (use_missing_node, node_removed, signature_changed, interface_conflict)

Flags for "bug report":
  --db <path>            Topology database path (default ".aracne/topology.db")
  --node <id>            Resource node ID (required)
  --description <text>   Bug description (required)

Flags for "bug list":
  --db <path>     Topology database path (default ".aracne/topology.db")
  --node <id>     Filter by resource node ID
  --state <state> Filter by state: pending, acknowledged, dismissed

Flags for "init":
  --global              Install the integration to user level rather than into this project

  A full-screen, interactive setup. It asks six questions -- which harness, which mode,
  who writes the descriptions and with which model, whether to describe the repository
  now or lazily as you read, and how much the generated contract should say -- then
  saves them to .aracne/config.json, scans the project, and writes the integration.
  Answer with the arrow keys and Enter; Escape cancels without writing anything.

  It needs a terminal. In a pipe, a cron job or CI it refuses and names "arac setup",
  which does the same writing with no questions.

Flags for "setup":
  --claude              Write the Claude Code integration
  --opencode            Write the OpenCode integration
  --global              Install to user-level (applies across all projects)
  -y                    Auto-confirm all replacement prompts

  Without flags, writes both. This is the command to re-run after editing
  .aracne/config.json: it renders the commands, the agents, the guard hook, the MCP
  entry and the contract in CLAUDE.md / AGENTS.md from whatever the config says.
  It READS the "mode" key and never writes it:
    mcp           a single "read" MCP tool; shell reads run as themselves
    cli   no MCP tools; the contract points at "arac read <id>"  (default)
    intercept_id  cat/head/tail/sed -n are answered from the topology and take a resource ID
    intercept_line_ranges    the same, with every declaration named by the exact lines it spans
  Shell "grep" is answered by aracne and edits re-sync the topology in all four.
  Agent tools, blocked native tools, plugins, and models are configured in
  .aracne/config.json under "llm" (per-agent mcp_tools / blocked_tools /
  plugins, merged from "<any>" + the per-harness "claude_code"/"opencode"
  blocks). Edit that file to customize what each agent can do.

  Examples:
    arac scan -root ./myproject -output myproject.db
    arac agent "list all structs"
    arac init
    arac setup --claude
    arac setup --opencode
    arac viz serve
    arac serve --tool-profile descriptions-generation-executor
    arac descriptions generate
    arac descriptions clear --target function,type
    arac read internal/topology/golang.GoManager
    arac read internal/cli/read.go
    arac read internal/cli.RunRead internal/cli.parseReadArgs`
