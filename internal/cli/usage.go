package cli

import "fmt"

func PrintUsage() {
	fmt.Println(`ltp - Go/Python project topology analyzer

Usage:
  ltp scan    [flags]    Incremental scan (changed files only); --all for full re-scan, --hard for full rebuild
  ltp agent   [prompt]   Run the AI coding agent
  ltp serve   [flags]    Start MCP server (for OpenCode / Claude Code integration)
  ltp viz serve [flags]  Start local topology graph visualization UI
  ltp init    [flags]    Initialize topology integration (--claude, --opencode, --global)
  ltp descriptions generate [flags]  Generate descriptions for targeted undocumented resources
  ltp descriptions apply            Write topology descriptions back into source as doc comments
  ltp descriptions clear [flags]    Clear stored topology descriptions
  ltp read [--kind <kind>] <resource-id>   Read a resource by ID; --kind forces exact kind (function, method, type, named_type, interface, variable, file, package, dependency)
  ltp search <string>      Search resource IDs and names, printing matching IDs one per line
  ltp grep [flags] <pattern> [path]  Search file contents and annotate topology resource matches
  ltp update-file <path>  Re-parse a file and update the topology database (--db to specify db path)
  ltp read-resource-and-cut <id> <kind>  Get a resource's source code cut (kind: Function, Struct, Interface, ExternalVar, File, Package)
  ltp update-description <id> <kind> <desc>  Update a resource's description in the topology DB
  ltp node list             List all nodes (IDs only)
  ltp node list --no-description  List undocumented nodes (IDs only)
  ltp warnings list [flags] List outstanding topology warnings
  ltp bug report  [flags] Report a bug on a resource node
  ltp bug list    [flags] List known bugs (filterable by node or state)
  ltp bug acknowledge <bugID>  Mark a bug as acknowledged
  ltp bug dismiss    <bugID>   Mark a bug as dismissed
  ltp bug delete     <bugID>   Delete a bug from the database
  ltp edit                 Edit a file (reads JSON from stdin: {"file_path", "old_string", "new_string"})
  ltp write                Write a file (reads JSON from stdin: {"file_path", "content"})
  ltp check-updates        Check which files were added, modified, or deleted since last scan
  ltp analyze dead-code [flags]  Find unused functions, structs, interfaces, named types, and variables

Flags for "scan":
  -root <path>    Root folder of the project (default ".")
  -output <file>  Output SQLite database path (default ".ltp/topology.db")
  --all           Re-scan all files (preserves descriptions)
  --hard          Force full rebuild from scratch (clears descriptions and bugs)
  --default       Force default incremental scan (overrides .ltp/config.json scan_mode)
  --debug         Compare warnings before and after scan, print differences

Flags for "serve":
  --tool-profile <profile>  Tool profile: default, descriptions-executor, bug-hunter, bug-judge, bug-solver, or all

Flags for "viz serve":
  --db <path>      Topology database path (default ".ltp/topology.db")
  --addr <addr>    HTTP listen address (default "127.0.0.1:7331")

Flags for "grep":
  --db <path>      Topology database path (default ".ltp/topology.db")

Flags for "descriptions generate":
  --targets <kinds>       Comma-separated resource kinds overriding config describe_targets (default: function,type,method,interface,file)
  --batch-size <n>        Maximum resources assigned to each description executor (default 20)
  --parallel <n>          Maximum description executors to run concurrently (default 4)
  --max-retries <n>       Maximum executor attempts per resource (default 3)

Flags for "descriptions clear":
  --target <kinds>        Comma-separated resource kinds to clear; omit to clear every description

Flags for "warnings list":
  --db <path>     Topology database path (default ".ltp/topology.db")
  --source <id>   Filter by source resource ID
  --target <id>   Filter by target resource ID
  --kind <kind>   Filter by warning kind (use_missing_node, node_removed, signature_changed)

Flags for "bug report":
  --db <path>            Topology database path (default ".ltp/topology.db")
  --node <id>            Resource node ID (required)
  --description <text>   Bug description (required)

Flags for "bug list":
  --db <path>     Topology database path (default ".ltp/topology.db")
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

  Config file: .ltp/config.json supports "scan_mode", "tool_modes", and "describe_targets".
    tool_modes.read:  "native", "mcp", or "terminal"
    tool_modes.edit:  "native", "mcp", or "terminal"
    tool_modes.other: "mcp" or "terminal"
    tool_modes.grep:  "native", "mcp", or "terminal"
    describe_targets: ["function", "type", "method", "interface", "file"]

Flags for "analyze dead-code":
  --db <path>           Topology database path (default ".ltp/topology.db")
  --kind <kind>         Resource kind filter: function, type, interface, named_type, variable
  --package <path>      Package path filter (e.g. ltp/internal/cli)
  --certain-only        Only report unexported dead code (safe to delete)
  --exported-only       Only report exported dead code (may have external users)
  --json                Output as JSON

  Examples:
    ltp scan -root ./myproject -output myproject.db
    ltp agent "list all structs"
    ltp init --read-mode mcp --edit-mode native --other-mode mcp
    ltp init --claude
    ltp init --opencode
    ltp viz serve
    ltp serve --tool-profile descriptions-executor
    ltp descriptions generate
    ltp descriptions clear --target function,type
    ltp read internal/topology/golang.GoManager
    ltp read internal/cli/read.go`)
}
