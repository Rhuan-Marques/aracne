# AGENTS.md

# === LLM Topology (LTP) Integration ===
This repository supports `ltp`. This means you should navigate by the repository in a clean way by focusing on reading `functions` and `structs` when possible.
These are the supported Languages for `ltp` integration:
- Golang

## How to read:
When acting within these languages you should cleverly use `read_function`, `read_struct` or `read` (files) when most relevant.

`read_function` - Will give you the function and also short descriptions of the resources it uses
`read_struct` - Will give you the struct, its field, constructor, and also short descriptions of resources it uses
`read` - The entire file, but no information on external resources

Here is some examples:
| Objective | Use | Why |
| --- | --- | --- |
| "I need to check how UpdatePlayer works" | read_function | Most relevant |
| "What is the AvgRating do in Player" | read_struct | Most relevant |
| "I need to check how UpdatePlayer works" | read_function | Most relevant |
| "I need to check the Makefile" | read | (not golang) |

## When to read:
You should call the `read`, `read_function` and `read_struct` functions only when necessary for your main objective. *Most of the time, the descriptions are enough*, but if you need to *look at the implementation* or *edit* something, you will need to *read* it.

# === Three Integration Modes ===

`llm-topology` supports three distinct integration modes. All three share the same underlying topology engine and must maintain **full feature parity** — any capability added to one must be available in all three.

## 1. Full CLI Integration (Terminal)

Direct command-line usage without any intermediate LLM. All topology operations available via `ltp` subcommands:

| CLI Command | Purpose |
|-------------|---------|
| `ltp scan` | Incremental update (preserves descriptions) |
| `ltp scan --hard` | Full rebuild from scratch |
| `ltp read_function <name>` | Function source + context |
| `ltp read_struct <name>` | Struct source + context |
| `ltp read-resource-and-cut <id> <kind>` | Source cut for any resource |
| `ltp update-description <id> <kind> <desc>` | Update a description |
| `ltp list-undocumented` | List all resources missing descriptions |
| `ltp update-file <path>` | Re-parse a file, update topology in-place |
| `ltp generate-descriptions` | Bulk auto-generate descriptions via LLM |
| `ltp edit` (via tooling) | File editing with topology auto-update |

**Use case:** Power users, CI/CD pipelines, scripting, batch operations, and when no LLM integration is needed.

## 2. MCP OpenCode Integration

The `ltp serve` command exposes all topology tools as MCP (Model Context Protocol) tools over stdio. OpenCode and other MCP-compatible platforms consume these as native tools.

**Available MCP tools:**

| Tool | Source | Description |
|------|--------|-------------|
| `ls` | `internal/llm/tools/ls.go` | List files and directories |
| `read` | `internal/llm/tools/read.go` | Read raw file contents |
| `read_function` | `internal/llm/tools/languages/gotools/read_function.go` | Function source + context |
| `read_struct` | `internal/llm/tools/languages/gotools/read_struct.go` | Struct source + context |
| `read_resource_and_cut` | `internal/llm/tools/languages/gotools/read_resource_and_cut.go` | Source cut + description instructions |
| `update_description` | `internal/llm/tools/languages/gotools/update_description.go` | Update a resource description |
| `edit` | Custom tool via `ltp install` (`edit.ts`) | Edit file, topology auto-updates |

**Setup:** `ltp install` generates two artifacts:
- `opencode.json` — MCP server config pointing to `ltp serve`
- `.opencode/tools/edit.ts` — custom OpenCode tool that wraps file editing with `ltp update-file` for topology auto-update

**Use case:** Full-featured AI-assisted development inside OpenCode with topology-aware tools.

## 3. Internal Agent (`ltp agent`)

A self-contained REPL agent that connects directly to DeepSeek API with the full topology-aware system prompt. No external platform required.

**Architecture:**
- `internal/llm/agent/agent.go` — Agent loop (max 20 iterations, tool-call loop)
- `internal/llm/agent/prompt.go` — `BuildPrompt()` constructs system prompt from `LLM_INTEGRATION_CHARTER.md` + language-specific prompt
- `internal/llm/languages/gotools/prompt.go` — Go-specific tool definitions and guidelines
- `internal/llm/providers/deepseek.go` — DeepSeek API provider

**Available tools (same as MCP, registered in `main.go`):**

| Tool | Purpose |
|------|---------|
| `ls` | List files and directories |
| `read` | Read raw file contents |
| `read_function` | Function source + context |
| `read_struct` | Struct source + context |
| `read_resource_and_cut` | Source cut + description instructions |
| `update_description` | Update a resource description |
| `edit` | Edit file (auto-updates topology via `TopologyManager.UpdateFile`) |

Additionally supports the descriptor sub-agent workflow:
1. `list_undocumented_resources` — get all undocumented resource IDs
2. Dispatch sub-agents that call `read_resource_and_cut` + `update_description`
3. All processed in a single session

**Use case:** Standalone AI coding assistant without OpenCode, direct DeepSeek integration, single-prompt or REPL mode.

## Feature Parity Matrix

Every feature must be available in all three modes:

| Feature | CLI | MCP | Agent |
|---------|:---:|:---:|:-----:|
| Topology scan (`scan`) | ✓ | auto on connect | auto on start |
| Read function (`read_function`) | ✓ | ✓ | ✓ |
| Read struct (`read_struct`) | ✓ | ✓ | ✓ |
| Read resource + cut (`read_resource_and_cut`) | ✓ | ✓ | ✓ |
| Update description (`update_description`) | ✓ | ✓ | ✓ |
| List undocumented (`list-undocumented`) | ✓ | ✓ | ✓ |
| Update file / topology (`update-file`) | ✓ | via edit tool | via edit tool |
| Generate descriptions (`generate-descriptions`) | ✓ | via agent prompt | via agent prompt |
| LLM system prompt integration | — | n/a (external LLM) | prepends LLM_INTEGRATION_CHARTER.md |

# === Repository Description ===

## Project Overview

`llm-topology` is a Go static analysis tool that recursively scans Go source trees, parses `.go` files using `go/ast`/`go/parser`, and builds a comprehensive graph model ("topology") of the project's structure: packages, files, structs, interfaces, functions (with call graphs), external variables, and dependencies. Output is stored in an SQLite database. It includes an AI coding agent powered by DeepSeek and an MCP server for integration with OpenCode and other LLM platforms. A React + Vite + TailwindCSS frontend (`frontend/`) uses @xyflow/react for graph visualization.

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
.\ltp generate-descriptions [--concurrency N]  # Generate descriptions for undocumented resources
```

## Commands

| Command | Description |
|---------|-------------|
| `go build -o ltp.exe .` | Build binary |
| `go run . scan -root <path>` | Run topology scan |
| `go run . agent` | Run AI agent (requires DEEPSEEK_API_KEY) |
| `go run . serve` | Start MCP server (stdio transport) |
| `go run . install` | Configure OpenCode MCP in opencode.json |
| `go run . generate-descriptions --concurrency 5` | Auto-generate descriptions via LLM |
| `go test ./internal/topology/` | Run topology tests |
| `go vet ./...` | Check for suspicious constructs |
| `gofmt -l -w .` | Format code |
| `go mod tidy` | Tidy dependencies |
| `go mod verify` | Verify checksums |

## Project Structure

```
main.go                         # CLI entry point (scan / agent / serve / install / generate-descriptions)
opencode.json                   # MCP plugin configuration
frontend/                       # React + Vite + TailwindCSS + @xyflow/react graph viz
internal/
  helper/
    db.go                       # SQLite persistence layer (schema, write, read)
    json.go                     # JSON persistence layer (alternative serialization)
  mcp/
    protocol.go                 # JSON-RPC 2.0 + MCP protocol types
    server.go                   # MCP server (stdio loop, delegates to tool instances)
  topology/
    manager.go                  # TopologyManager (FullScan, Load, Write, ReadAll, Cut, UpdateFile, FindResourcesByName, UpdateDescription)
    manager_test.go             # Tests for Cut, ReadFunction, ReadStruct
    options.go                  # TopologyOption, WithResourceFilter, WithHasDescription
    domain/
      resource.go               # Generic domain types: ResourceKind, Resource
      topology.go               # Topology, Resource, TopologyWarning
      location.go               # Location (StartsAt, EndsAt, Path)
      cut.go                    # CodeEntry (Location + Cut string)
    scanner/
      scanner.go                # LanguageScanner interface
      registry.go               # Scanner Registry (multi-language support)
      goscanner/
        scanner.go              # Go Scanner implementation (Scan, UpdateFile, helpers)
        parser.go               # Go source AST parser (struct, interface, func extraction)
        resolver.go             # Function body reference resolution (call graphs)
        matcher.go              # Struct-to-interface matching
    golang/
      resources.go              # Go-specific types: GolangFunction, GolangStruct, GolangInterface, GolangExternalVar, GolangFile, GolangPackage
      types.go                  # Output types: FunctionCut, StructCut, SimplifiedFunction, GoFunctionContext, GoStructContext, etc.
      connections.go            # ConnectionKind constants + typed accessor methods
      mapper.go                 # FromGeneric / ToGeneric (domain <-> golang)
      manager.go                # GoManager wrapping TopologyManager (ReadFunction, ReadStruct, FindFunctionsByName, FindStructsByName, ReadResourceAndCut, UpdateDescription)
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
    languages/
      gotools/
        register.go             # Register Go-specific tools in tool registry
        read_function.go        # "read_function" tool (name-based lookup → rich context)
        read_struct.go          # "read_struct" tool (name-based lookup → rich context)
        read_resource_and_cut.go # "read_resource_and_cut" tool (source cut + description instructions)
        update_description.go   # "update_description" tool
        format.go               # FormatGoFunctionContext, FormatGoStructContext formatters
        prompt.go               # BuildGoSystemPrompt for agent
```

## Code Conventions

- **Module name:** `llm-topology` (imports use `llm-topology/...`)
- **ID types:** String type aliases (`FunctionID`, `StructID`, etc. in `golang/resources.go`)
- **ResourceKind:** Categorized via `domain.ResourceKind` constants (`ResourceFunction`, `ResourceMethod`, `ResourceType`, `ResourceInterface`, `ResourceVariable`, `ResourceFile`, `ResourcePackage`, `ResourceDependency`)
- **Dual domain model:** Generic `domain.Resource` is stored in SQLite; Go-specific types in `golang/` with `FromGeneric`/`ToGeneric` mappers
- **Connections:** Typed via `golang.ConnectionKind` constants with typed accessor methods (`.Calls()`, `.UsesStruct()`, etc.)
- **No comments on trivial code** — keep it that way
- **Pure Go standard library** for AST analysis; only external dep is `modernc.org/sqlite`
- **Errors:** Accumulated in `Topology.Errors` map (per-file), never fatal to the scan

## TopologyManager

- **SQLite-driven:** No in-memory topology field. All data read/written via `.db` file.
- `FullScan(root, reg)` — scans project via scanner registry, writes result to configured dbPath
- `Load(path)` — sets dbPath for subsequent operations
- `Write(path)` — file-level copy of the database
- `ReadAll(opts ...TopologyOption)` — returns entire topology, optionally filtered
- `UpdateFile(path, reg) []TopologyWarning` — re-parses a file via registry, updates topology DB in-place, returns warnings
- `Cut(loc Location) *CodeEntry` — reads source file and returns lines between StartsAt and EndsAt
- `FindResourcesByName(name, kinds...)` — find resource IDs by name and optional kind filter
- `UpdateDescription(id, kind, description)` — update a resource's description in the DB
- `DbPath() string` — returns the current database path

## GoManager

Wraps `TopologyManager` with Go-specific context enrichment:

- `ReadFunction(id, opts...)` — returns `GoFunctionContext` with function cut, parent struct, called funcs, struct/interface usage, ext vars, deps, packages, sorted blocks
- `ReadStruct(id, opts...)` — returns `GoStructContext` with struct cut, constructor, interfaces (with NeedToImplement), methods, refs, sorted blocks
- `FindFunctionsByName(name) []FunctionID` — search by name
- `FindStructsByName(name) []StructID` — search by name
- `ReadResourceAndCut(id, kind) *CodeEntry` — source cut for any resource kind
- `UpdateDescription(id, kind, desc)` — delegates to manager

## TopologyWarning

Emitted during `UpdateFile` for removed functions or signature changes. Contains:
- `Resource ResourceKind` — type of affected element
- `AffectedResources []string` — resource IDs needing manual review
- `Message string` — description of the change

Description preservation: if the old topology had a non-empty description and the new source omits the doc comment, the old description is kept.

## GoFunctionContext Output

`ReadFunction` returns a `GoFunctionContext` with:
- `Function *FunctionCut` — full function data + source code cut
- `ParentStruct *StructCut` — full parent struct data + cut (if method)
- `CalledFunctions []SimplifiedFunction` — ID, signature, description only (no methods)
- `StructsUsed []StructUsage` — struct ID/desc, with method list if called
- `InterfacesUsed []InterfaceUsage` — interface + implementing structs + their called methods
- `ExtVarsUsed []SimplifiedExtVar` — ID, name, description, value (truncated at 500 chars)
- `Dependencies []DependancyPath` — union of function + parent struct
- `PackagesUsed []PackagePath` — union of function + parent struct
- `Blocks []ContextBlock` — flat ordered list sorted by (FilePath, Line) for proximity rendering

## GoStructContext Output

`ReadStruct` returns a `GoStructContext` with:
- `Struct *StructCut` — full struct data + source cut
- `Constructor *FunctionCut` — constructor function + cut (if exists)
- `Interfaces []SimplifiedInterface` — interfaces implemented, with NeedToImplement flag
- `Methods []SimplifiedFunction` — all methods of the struct
- `StructsUsed, InterfacesUsed, ExtVarsUsed, Dependencies, PackagesUsed` — union of struct + constructor references
- `Blocks` — flat sorted block list

## TopologyOption / WithResourceFilter / WithHasDescription

All Read methods accept optional `TopologyOption` arguments. `WithResourceFilter(ResourceKind...)` limits which resource categories are populated. `WithHasDescription(bool)` filters resources with/without descriptions. When no filter is passed, all data is returned (backward compatible).

## Testing

- `internal/topology/manager_test.go` — Cut, ReadFunction (raw + called funcs), ReadStruct
- Tests use `go test -v ./internal/topology/`
- Test databases are built in-package via `FullScan` using `goscanner.NewGoScanner()`

## MCP Server & OpenCode Plugin

The MCP server exposes the topology tools over stdio (JSON-RPC 2.0), allowing OpenCode and other LLM platforms to use the full set of topology-aware tools.

### Available tools (via MCP)

| Tool | Description |
|------|-------------|
| `ls` | List files and directories |
| `read` | Read raw file contents |
| `read_function` | Function source + interconnected context |
| `read_struct` | Struct source + interconnected context |
| `read_resource_and_cut` | Source cut + description instructions for any resource |
| `update_description` | Update a resource's description in the topology DB |
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
      "command": ["ltp", "serve"],
      "enabled": true
    }
  }
}
```

The MCP server auto-scans the project if `.ltp/topology.db` is missing on first connect. Tool output matches the same format as the internal agent mode — a code block followed by a `# CONTEXT:` section with hierarchical interface/struct/function/var descriptions.

## Notes

- AI agent mode requires `DEEPSEEK_API_KEY` environment variable.
- MCP server mode does not require an API key (the external LLM platform provides its own).
- The scanner architecture supports multiple languages via `LanguageScanner` interface and `Registry`; currently only Go is implemented.
- `generate-descriptions` subcommand uses the LLM to auto-generate descriptions for all undocumented resources.
