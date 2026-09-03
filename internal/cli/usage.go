package cli

import (
	"fmt"
	"strings"

	"aracne/internal/helper"
)

// Prints the full CLI usage documentation for all aracne commands and flags.
//
// The bug-pipeline blocks are omitted unless features.bug_management is on. `arac bug`
// stays dispatchable either way -- it is the orchestration channel the generated slash
// commands use and the debugging path -- but an undocumented command is the "unlisted"
// half of gating an unshipped feature.
func PrintUsage() {
	cfg := helper.LoadConfig(helper.ConfigPath(".aracne/topology.db"))
	fmt.Println(gateBugUsage(usageText, cfg.BugManagementEnabled()))
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
		if strings.HasPrefix(trimmed, "arac bug ") || strings.HasPrefix(trimmed, "Aracne bug ") {
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

const usageText = `arac - Go/Python/JavaScript/TypeScript project topology analyzer

Usage:
  Aracne scan    [flags]    Incremental scan (changed files only); --all for full re-scan, --hard for full rebuild
  Aracne agent   [prompt]   Run the AI coding agent
  Aracne serve   [flags]    Start MCP server (for OpenCode / Claude Code integration)
  Aracne viz serve [flags]  Start local topology graph visualization UI
  Aracne init    [flags]    Initialize topology integration (--claude, --opencode, --global)
  Aracne descriptions generate [flags]  Generate descriptions for targeted undocumented resources
  Aracne descriptions apply            Write topology descriptions back into source as doc comments
  Aracne descriptions clear [flags]    Clear stored topology descriptions (--oversized: only over-budget ones)
  Aracne descriptions export [flags]   Back up descriptions to an ID-independent JSONL sidecar
  Aracne descriptions import [flags]   Restore descriptions from a sidecar after a re-scan
  arac read [--kind <kind>] [--full] <resource-id>...  Read one or more resources by ID; --kind forces exact kind (function, method, struct, named_type, interface, variable, file, package, dependency); --full returns whole file bodies under read.file_mode "skeleton"
  arac resource list [query] [--kind <kind>]... [--no-description]  List resources, optionally filtered by query, kind, or missing description
  arac grep [flags] <pattern> [path]  Search node names, node descriptions and file contents (ranked in that order)
  arac cmd -- <command> [args...]  Run a shell read or search (cat/head/tail/sed -n/grep...), answered from the topology where aracne can and by the real command where it cannot
  arac update-file <path>  Re-parse a file and update the topology database (--db to specify db path)
  Aracne read-resource-and-cut <id> <kind>  Get a resource's source code cut (kind: Function, Struct, Interface, ExternalVar, File, Package)
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
  Aracne analyze dead-code [flags]  Find unused functions, structs, interfaces, named types, and variables

Flags for "scan":
  -root <path>    Root folder of the project (default ".")
  -output <file>  Output SQLite database path (default ".aracne/topology.db")
  --all           Re-scan all files (preserves descriptions)
  --hard          Force full rebuild from scratch (clears descriptions and bugs)
  --default       Force default incremental scan (overrides .aracne/config.json scan.mode)
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
  --targets <kinds>       Comma-separated resource kinds overriding config descriptions.kinds (default: function,method,struct,interface,file)
  --batch-size <n>        Maximum resources assigned to each description executor (default 5)
  --parallel <n>          Maximum description executors to run concurrently (default 4)
  --max-retries <n>       Maximum executor attempts per resource (default 3)
  --regen_oversized       Rewrite existing descriptions that overrun their kind's character
                          budget (function/method 120, type 100, variable 80) instead of
                          describing undocumented resources

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
  --kind <kind>   Filter by warning kind (use_missing_node, node_removed, signature_changed)

Flags for "bug report":
  --db <path>            Topology database path (default ".aracne/topology.db")
  --node <id>            Resource node ID (required)
  --description <text>   Bug description (required)

Flags for "bug list":
  --db <path>     Topology database path (default ".aracne/topology.db")
  --node <id>     Filter by resource node ID
  --state <state> Filter by state: pending, acknowledged, dismissed

Flags for "init":
  --claude              Initialize Claude Code integration
  --opencode            Initialize OpenCode integration
  --global              Install to user-level (applies across all projects)
  --mcp                 Wire the MCP server (sets "mode" to "mcp" in .aracne/config.json)
  -y                    Auto-confirm all replacement prompts

  Without flags, initializes both Claude Code and OpenCode.
  The "mode" key in .aracne/config.json decides what init writes:
    mcp           a single "read" MCP tool; shell reads run as themselves
    aracne_read   no MCP tools; the contract points at "arac read <id>"  (default)
    intercept_id  cat/head/tail/sed -n are answered from the topology and take a resource ID
    line_range    the same, with every declaration named by the exact lines it spans
  Shell "grep" is answered by aracne and edits re-sync the topology in all four.
  Agent tools, blocked native tools, plugins, and models are configured in
  .aracne/config.json under "llm" (per-agent mcp_tools / blocked_tools /
  plugins, merged from "<any>" + the per-harness "claude_code"/"opencode"
  blocks). Edit that file to customize what each agent can do.

Flags for "analyze dead-code":
  --db <path>           Topology database path (default ".aracne/topology.db")
  --kind <kind>         Resource kind filter: function, struct, interface, named_type, variable
  --package <path>      Package path filter (e.g. aracne/internal/cli)
  --certain-only        Only report unexported dead code (safe to delete)
  --exported-only       Only report exported dead code (may have external users)
  --json                Output as JSON

  Examples:
    Aracne scan -root ./myproject -output myproject.db
    Aracne agent "list all structs"
    Aracne init --claude
    Aracne init --opencode
    Aracne viz serve
    Aracne serve --tool-profile descriptions-generation-executor
    Aracne descriptions generate
    Aracne descriptions clear --target function,type
    arac read internal/topology/golang.GoManager
    arac read internal/cli/read.go
    arac read internal/cli.RunRead internal/cli.parseReadArgs`
