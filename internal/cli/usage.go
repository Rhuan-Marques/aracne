package cli

import "fmt"

func PrintUsage() {
	fmt.Println(`ltp - Go/Python project topology analyzer

Usage:
  ltp scan    [flags]    Incremental scan (changed files only); --all for full re-scan, --hard for full rebuild
  ltp agent   [prompt]   Run the AI coding agent
  ltp serve   [flags]    Start MCP server (for OpenCode / Claude Code integration)
  ltp init    [flags]    Initialize topology integration (--claude, --opencode, --global)
  ltp descriptions generate [flags]  Generate descriptions for targeted undocumented resources
  ltp descriptions apply            Write topology descriptions back into source as doc comments
  ltp read_function <name>  Show a function's source code and its interconnected context
  ltp read_struct <name>    Show a struct/class source code and interconnected context
  ltp update-file <path>  Re-parse a file and update the topology database (--db to specify db path)
  ltp read-resource-and-cut <id> <kind>  Get a resource's source code cut (kind: Function, Struct, Interface, ExternalVar, File, Package)
  ltp update-description <id> <kind> <desc>  Update a resource's description in the topology DB
  ltp list-undocumented     List all resources without descriptions
  ltp warnings list [flags] List outstanding topology warnings
  ltp bug report  [flags] Report a bug on a resource node
  ltp bug list    [flags] List known bugs (filterable by node or state)
  ltp bug acknowledge <bugID>  Mark a bug as acknowledged
  ltp bug dismiss    <bugID>   Mark a bug as dismissed
  ltp bug delete     <bugID>   Delete a bug from the database
  ltp edit                 Edit a file (reads JSON from stdin: {"file_path", "old_string", "new_string"})
  ltp write                Write a file (reads JSON from stdin: {"file_path", "content"})
  ltp check-updates        Check which files were added, modified, or deleted since last scan
  ltp read_file <path>     Read a file by path, returning filename and full content

Flags for "scan":
  -root <path>    Root folder of the project (default ".")
  -output <file>  Output SQLite database path (default ".ltp/topology.db")
  --all           Re-scan all files (preserves descriptions)
  --hard          Force full rebuild from scratch (clears descriptions and bugs)
  --default       Force default incremental scan (overrides .ltp/config.json scan_mode)
  --debug         Compare warnings before and after scan, print differences

Flags for "serve":
  --tool-profile <profile>  Tool profile: default, descriptor, bug-hunter, bug-judge, bug-solver, or all

Flags for "descriptions generate":
  --targets <kinds>  Comma-separated resource kinds overriding config describe_targets (default: function,type,method,interface,file)

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
  -y                    Auto-confirm all replacement prompts

  Without flags, initializes both Claude Code and OpenCode.

  Config file: .ltp/config.json supports "scan_mode", "tool_modes", and "describe_targets".
    tool_modes.read:  "native", "mcp", or "terminal"
    tool_modes.edit:  "native", "mcp", or "terminal"
    tool_modes.other: "mcp" or "terminal"
    describe_targets: ["function", "type", "method", "interface", "file"]

  Examples:
    ltp scan -root ./myproject -output myproject.db
    ltp agent "list all structs"
    ltp init --read-mode mcp --edit-mode native --other-mode mcp
    ltp init --claude
    ltp init --opencode
    ltp serve --tool-profile descriptor
    ltp descriptions generate
    ltp read_function ReadFunction
    ltp read_struct TopologyManager`)
}
