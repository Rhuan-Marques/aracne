package cli

import "fmt"

func PrintUsage() {
	fmt.Println(`ltp - Go project topology analyzer

Usage:
  ltp scan    [flags]    Scan a Go project and build topology database
  ltp agent   [prompt]   Run the AI coding agent
  ltp serve              Start MCP server (for OpenCode / Claude Code integration)
  ltp init   [flags]    Initialize topology integration (--claude, --opencode, --global)
  ltp descriptions generate [flags]  Generate descriptions for all undocumented resources
  ltp descriptions apply            Write topology descriptions back into source as doc comments
  ltp read_function <name>  Show a function's source code and its interconnected context
  ltp read_struct <name>    Show a struct's source code and its interconnected context
  ltp update-file <path>    Re-parse a file and update the topology database
  ltp read-resource-and-cut <id> <kind>  Get a resource's source code cut (kind: Function, Struct, Interface, ExternalVar, File, Package)
  ltp update-description <id> <kind> <desc>  Update a resource's description in the topology DB
  ltp list-undocumented     List all resources without descriptions
  ltp warnings list [flags] List outstanding topology warnings
  ltp edit                 Edit a file (reads JSON from stdin: {"file_path", "old_string", "new_string"})
  ltp read_file <path>     Read a file by path, returning filename and full content

Flags for "scan":
  -root <path>    Root folder of the Go project (default ".")
  -output <file>  Output SQLite database path (default ".ltp/topology.db")
  --hard          Force full rebuild instead of incremental update
  --debug         Compare warnings before and after scan, print differences

Flags for "warnings list":
  --db <path>     Topology database path (default ".ltp/topology.db")
Flags for "warnings list":
  --target <id>   Filter by target resource ID
  --kind <kind>    Filter by warning kind (use_missing_node, node_removed, signature_changed)

Flags for "init":
  --claude        Initialize Claude Code integration (.mcp.json, CLAUDE.md, .claude/commands/)
  --opencode      Initialize OpenCode integration (.opencode/opencode.json, custom tools, commands)
  --global        Install to user-level (applies across all projects)

  Without flags, initializes both Claude Code and OpenCode.

  Examples:
    ltp scan -root ./myproject -output myproject.db
    ltp agent "list all structs"
    ltp init                   # init for both Claude Code and OpenCode
    ltp init --claude          # init for Claude Code only
    ltp init --opencode        # init for OpenCode only
    ltp init --global          # global config (both platforms)
    ltp serve                 # start MCP server (used by OpenCode)
    ltp descriptions generate # generate descriptions for all resources
    ltp descriptions apply    # write descriptions into source as doc comments
    ltp read_function ReadFunction
    ltp read_struct TopologyManager`)
}