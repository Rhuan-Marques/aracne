package cli

import "fmt"

func PrintUsage() {
	fmt.Println(`arac - Go/Python project topology analyzer

Usage:
  Aracne scan    [flags]    Incremental scan (changed files only); --all for full re-scan, --hard for full rebuild
  Aracne agent   [prompt]   Run the AI coding agent
  Aracne serve   [flags]    Start MCP server (for OpenCode / Claude Code integration)
  Aracne viz serve [flags]  Start local topology graph visualization UI
  Aracne init    [flags]    Initialize topology integration (--claude, --opencode, --global)
  Aracne descriptions generate [flags]  Generate descriptions for targeted undocumented resources
  Aracne descriptions apply            Write topology descriptions back into source as doc comments
  Aracne descriptions clear [flags]    Clear stored topology descriptions
  arac read [--kind <kind>] <resource-id>   Read a resource by ID; --kind forces exact kind (function, method, type, named_type, interface, variable, file, package, dependency)
  arac resource list [query] [--kind <kind>]... [--no-description]  List resources, optionally filtered by query, kind, or missing description
  arac grep [flags] <pattern> [path]  Search file contents and annotate topology resource matches
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
  Aracne check-updates        Check which files were added, modified, or deleted since last scan
  Aracne analyze dead-code [flags]  Find unused functions, structs, interfaces, named types, and variables

Flags for "scan":
  -root <path>    Root folder of the project (default ".")
  -output <file>  Output SQLite database path (default ".aracne/topology.db")
  --all           Re-scan all files (preserves descriptions)
  --hard          Force full rebuild from scratch (clears descriptions and bugs)
  --default       Force default incremental scan (overrides .aracne/config.json scan_mode)
  --debug         Compare warnings before and after scan, print differences

Flags for "serve":
  --tool-profile <profile>  Tool profile: default, descriptions-executor, bug-hunter, bug-judge, bug-solver, or all

Flags for "viz serve":
  --db <path>      Topology database path (default ".aracne/topology.db")
  --addr <addr>    HTTP listen address (default "127.0.0.1:7331")

Flags for "grep":
  --db <path>      Topology database path (default ".aracne/topology.db")

Flags for "descriptions generate":
  --targets <kinds>       Comma-separated resource kinds overriding config need_description (default: function,method,type,interface,file)
  --batch-size <n>        Maximum resources assigned to each description executor (default 20)
  --parallel <n>          Maximum description executors to run concurrently (default 4)
  --max-retries <n>       Maximum executor attempts per resource (default 3)

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
  --read-mode <mode>    native, mcp, or terminal (default from config: mcp)
  --edit-mode <mode>    native, mcp, or terminal; governs edit and write (default from config: native)
  --other-mode <mode>   mcp or terminal (default from config: mcp)
  --grep-mode <mode>    native, mcp, or terminal (default from config: native)
  -y                    Auto-confirm all replacement prompts

  Without flags, initializes both Claude Code and OpenCode.

  Config file: .aracne/config.json supports "scan_mode", "tool_modes", and "need_description".
    tool_modes.read:  "native", "mcp", or "terminal"
    tool_modes.edit:  "native", "mcp", or "terminal"
    tool_modes.other: "mcp" or "terminal"
    tool_modes.grep:  "native", "mcp", or "terminal"
    need_description: ["function", "method", "type", "interface", "file"]

Flags for "analyze dead-code":
  --db <path>           Topology database path (default ".aracne/topology.db")
  --kind <kind>         Resource kind filter: function, type, interface, named_type, variable
  --package <path>      Package path filter (e.g. aracne/internal/cli)
  --certain-only        Only report unexported dead code (safe to delete)
  --exported-only       Only report exported dead code (may have external users)
  --json                Output as JSON

  Examples:
    Aracne scan -root ./myproject -output myproject.db
    Aracne agent "list all structs"
    Aracne init --read-mode mcp --edit-mode native --other-mode mcp
    Aracne init --claude
    Aracne init --opencode
    Aracne viz serve
    Aracne serve --tool-profile descriptions-executor
    Aracne descriptions generate
    Aracne descriptions clear --target function,type
    arac read internal/topology/golang.GoManager
    arac read internal/cli/read.go`)
}
