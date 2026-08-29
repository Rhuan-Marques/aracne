package cli

import "fmt"

// Prints the full CLI usage documentation for all aracne commands and flags.
func PrintUsage() {
	fmt.Println(`arac - Go/Python/JavaScript/TypeScript project topology analyzer

Usage:
  Aracne scan    [flags]    Incremental scan (changed files only); --all for full re-scan, --hard for full rebuild
  Aracne agent   [prompt]   Run the AI coding agent
  Aracne serve   [flags]    Start MCP server (for OpenCode / Claude Code integration)
  Aracne viz serve [flags]  Start local topology graph visualization UI
  Aracne init    [flags]    Initialize topology integration (--claude, --opencode, --global)
  Aracne descriptions generate [flags]  Generate descriptions for targeted undocumented resources
  Aracne descriptions apply            Write topology descriptions back into source as doc comments
  Aracne descriptions clear [flags]    Clear stored topology descriptions
  Aracne descriptions export [flags]   Back up descriptions to an ID-independent JSONL sidecar
  Aracne descriptions import [flags]   Restore descriptions from a sidecar after a re-scan
  arac read [--kind <kind>] <resource-id>...  Read one or more resources by ID; --kind forces exact kind (function, method, struct, named_type, interface, variable, file, package, dependency)
  arac resource list [query] [--kind <kind>]... [--no-description]  List resources, optionally filtered by query, kind, or missing description
  arac grep [flags] <pattern> [path]  Search node names, node descriptions and file contents (ranked in that order)
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
  -y                    Auto-confirm all replacement prompts

  Without flags, initializes both Claude Code and OpenCode.
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
    arac read internal/cli.RunRead internal/cli.parseReadArgs`)
}
