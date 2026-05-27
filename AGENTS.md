# AGENTS.md

## Project Overview

`llm-topology` is a Go static analysis tool that recursively scans Go source trees, parses `.go` files using `go/ast`/`go/parser`, and builds a comprehensive graph model ("topology") of the project's structure: packages, files, structs, interfaces, functions (with call graphs), external variables, and dependencies. Output is stored in an SQLite database. It includes an AI coding agent powered by DeepSeek and an MCP server for integration with OpenCode and other LLM platforms.

## Build & Run

```powershell
go build -o ltp.exe .
.\ltp scan                              # scan current dir → .ltp/topology.db
.\ltp scan -root <path> -output out.db
.\ltp agent                             # AI agent mode (REPL, requires DEEPSEEK_API_KEY)
.\ltp agent "list all structs"          # single-prompt agent mode
.\ltp serve                             # Start MCP server (for OpenCode plugin)
.\ltp install                           # Configure OpenCode to use llm-topology
.\ltp install --global                  # Configure globally
```

## Commands

| Command | Description |
|---------|-------------|
| `go build -o ltp.exe .` | Build binary |
| `go run . scan -root <path>` | Run topology scan |
| `go run . agent` | Run AI agent (requires DEEPSEEK_API_KEY) |
| `go run . serve` | Start MCP server (stdio transport) |
| `go run . install` | Configure OpenCode MCP in opencode.json |
| `go test ./internal/topology/` | Run topology tests |
| `go vet ./...` | Check for suspicious constructs |
| `gofmt -l -w .` | Format code |
| `go mod tidy` | Tidy dependencies |
| `go mod verify` | Verify checksums |

## Project Structure

```
main.go                         # CLI entry point (scan / agent / serve / install subcommands)
internal/
  helper/
    db.go                       # SQLite persistence layer (schema, write, read)
    json.go                     # JSON persistence layer (alternative serialization)
  mcp/
    protocol.go                 # JSON-RPC 2.0 + MCP protocol types
    server.go                   # MCP server (stdio loop, delegates to tool instances)
  topology/
    manager.go                  # TopologyManager orchestrator (FullScan, Write, Load, UpdateFile, Cut, ReadFunction, ReadStruct, ReadAll, FindFunctionsByName, FindStructsByName)
    manager_test.go             # Tests for Cut, ReadFunction, ReadStruct
    options.go                  # TopologyOption, WithResourceFilter
    scanner/
      scanner.go                # Core scanning pipeline + UpdateFileInTopology
      parser.go                 # Go source AST parser (struct, interface, func extraction)
      resolver.go               # Function body reference resolution (call graphs)
      matcher.go                # Struct-to-interface matching
    domain/
      resources.go              # Domain types / data model
  llm/
    provider.go                 # LLM Provider interface + Message/Tool types
    providers/
      deepseek.go               # DeepSeek API implementation
    agent/
      agent.go                  # AI agent loop (max 20 iterations)
      prompt.go                 # System prompt (topology-aware)
    tools/
      tool.go                   # Tool interface + Registry
      read.go                   # "read" tool (raw file read)
      edit.go                   # "edit" tool (replace text, auto-updates topology)
      ls.go                     # "ls" tool (list files/directories)
      read_function.go          # "read_function" tool (name-based lookup → rich context)
      read_struct.go            # "read_struct" tool (name-based lookup → rich context)
      format.go                 # formatFunctionContext, formatStructContext formatters
```

## Code Conventions

- **Module name:** `llm-topology` (imports use `llm-topology/...`)
- **ID types:** String aliases (`FunctionID`, `StructID`, `InterfaceID`, etc.)
- **Resource interface:** All entity types implement `Resource` with `ResourceName() ResourceName`
- **Generics:** `unique[T]()`, `contains[T]()`, `removeFromSlice[T]()` helpers in `scanner.go`
- **No comments on trivial code** — keep it that way
- **Pure Go standard library** for AST analysis; only external dep is `modernc.org/sqlite`
- **Errors:** Accumulated in `Topology.Errors` map (per-file), never fatal to the scan

## TopologyManager

- **SQLite-driven:** No in-memory topology field. All data read/written via `.db` file.
- `FullScan(root)` — scans project, writes result to configured dbPath
- `Load(path)` — sets dbPath for subsequent operations
- `Write(path)` — file-level copy of the database
- `ReadAll(opts ...TopologyOption)` — returns entire topology, optionally filtered
- `UpdateFile(path) []TopologyWarning` — re-parses a file, updates topology DB in-place, returns warnings for removed/changed functions
- `Cut(loc Location) *CodeEntry` — reads source file and returns lines between StartsAt and EndsAt
- `ReadFunction(id string, opts ...TopologyOption) *FunctionContext` — full function context
- `ReadStruct(id string, opts ...TopologyOption) *StructContext` — full struct context

## TopologyWarning

Emitted during `UpdateFile` for removed functions or signature changes. Contains:
- `Resource ResourceName` — type of affected element
- `AffectedFunctions []FunctionID` — functions needing manual review
- `Message string` — description of the change

Description preservation: if the old topology had a non-empty description and the new source omits the doc comment, the old description is kept.

## ReadFunction Output

`ReadFunction` returns a `FunctionContext` with:
- `Function *FunctionCut` — full function data + source code cut
- `ParentStruct *StructCut` — full parent struct data + cut (if method)
- `CalledFunctions []SimplifiedFunction` — ID, signature, description only (no methods)
- `StructsUsed []StructUsage` — struct ID/desc, with method list if called
- `InterfacesUsed []InterfaceUsage` — interface + implementing structs + their called methods
- `ExtVarsUsed []SimplifiedExtVar` — ID, name, description, value (truncated at 500 chars)
- `Dependencies []DependancyPath` — union of function + parent struct
- `PackagesUsed []PackagePath` — union of function + parent struct
- `Blocks []ContextBlock` — flat ordered list sorted by (FilePath, Line) for proximity rendering

## ReadStruct Output

`ReadStruct` returns a `StructContext` with:
- `Struct *StructCut` — full struct data + source cut
- `Constructor *FunctionCut` — constructor function + cut (if exists)
- `Interfaces []SimplifiedInterface` — interfaces implemented, with NeedToImplement flag
- `Methods []SimplifiedFunction` — all methods of the struct
- `StructsUsed, InterfacesUsed, ExtVarsUsed, Dependencies, PackagesUsed` — union of struct + constructor references
- `Blocks` — flat sorted block list

## TopologyOption / WithResourceFilter

All Read methods accept optional `TopologyOption` arguments. `WithResourceFilter(ResourceName...)` limits which resource categories are populated. When no filter is passed, all data is returned (backward compatible).

## Testing

- `internal/topology/manager_test.go` — Cut, ReadFunction, ReadStruct
- Tests use `go test -v ./internal/topology/`
- Test databases are built in-package via `FullScan`

## MCP Server & OpenCode Plugin

The MCP server exposes the topology tools over stdio (JSON-RPC 2.0), allowing OpenCode and other LLM platforms to use the full set of topology-aware tools.

### Available tools (via MCP)

| Tool | Description |
|------|-------------|
| `ls` | List files and directories |
| `read` | Read raw file contents |
| `read_function` | Function source + interconnected context |
| `read_struct` | Struct source + interconnected context |
| `edit` | Edit file (topology auto-updated, warnings surfaced) |

### Setup

```bash
ltp install              # adds MCP config to opencode.json
ltp install --global     # adds to ~/.config/opencode/opencode.json
```

This inserts into opencode.json:
```json
{
  "mcp": {
    "llm-topology": {
      "type": "local",
      "command": "ltp",
      "args": ["serve"]
    }
  }
}
```

The MCP server auto-scans the project if `.ltp/topology.db` is missing on first connect. Tool output matches the same format as the internal agent mode — a code block followed by a `# CONTEXT:` section with hierarchical interface/struct/function/var descriptions.

## Notes

- AI agent mode requires `DEEPSEEK_API_KEY` environment variable.
- MCP server mode does not require an API key (the external LLM platform provides its own).
