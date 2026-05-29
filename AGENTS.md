# AGENTS.md

# === LLM Topology (LTP) Integration ===
This repository supports `ltp`. This means you should navigate by the repository in a clean way by focusing on reading `functions` and `structs` when possible.

## How to read:
To read files or information you should use the MCP functions: `read_function`, `read_struct` or `read_file` when most relevant.

`read_function` - Will give you the function and also short descriptions of the resources it uses
`read_struct` - Will give you the struct, its field, constructor, and also short descriptions of resources it uses
`read_file` - The entire file, but no information on external resources

Here is some examples:
| Objective | Use | Why |
| --- | --- | --- |
| "I need to check how UpdatePlayer works" | read_function | Most relevant |
| "What is the AvgRating do in Player" | read_struct | Most relevant |
| "I need to check how UpdatePlayer works" | read_function | Most relevant |
| "I need to check the Makefile" | read | (not golang) |

## When to read:
You should call the `read_file`, `read_function` and `read_struct` functions only when necessary for your main objective. *Most of the time, the descriptions are enough*, but if you need to *look at the implementation* or *edit* something, you will need to *read* it.

## How to edit:
The `edit` mcp function is there for you to use. Use the `edit` **MCP** function to edit files.
# === Repository Description ===

## Project Overview

`llm-topology` is a static analysis tool that recursively scans Go and Python source trees, parses source files using language-specific parsers, and builds a comprehensive graph model ("topology") of the project's structure: packages, files, structs, interfaces, functions (with call graphs), external variables, and dependencies. Output is stored in an SQLite database. It includes an AI coding agent powered by DeepSeek and an MCP server for integration with OpenCode and other LLM platforms.

## Build & Run

```powershell
go build -o ltp.exe .
.\ltp scan                              # scan current dir → .ltp/topology.db
.\ltp scan -root <path> -output out.db
.\ltp agent                             # AI agent mode (REPL, requires DEEPSEEK_API_KEY)
.\ltp agent "list all structs"          # single-prompt agent mode
.\ltp serve                             # Start MCP server (for OpenCode plugin)
.\ltp init                           # Configure OpenCode to use llm-topology
.\ltp init --global                  # Configure globally
.\ltp generate-descriptions [--concurrency N]  # Generate descriptions for undocumented resources
```

## Commands

| Command | Description |
|---------|-------------|
| `go build -o ltp.exe .` | Build binary |
| `go run . scan -root <path>` | Run topology scan |
| `go run . agent` | Run AI agent (requires DEEPSEEK_API_KEY) |
| `go run . serve` | Start MCP server (stdio transport) |
| `go run . init` | Configure OpenCode MCP in opencode.json |
| `go run . generate-descriptions --concurrency 5` | Auto-generate descriptions via LLM |
| `go test ./internal/topology/` | Run topology tests |
| `go vet ./...` | Check for suspicious constructs |
| `gofmt -l -w .` | Format code |
| `go mod tidy` | Tidy dependencies |
| `go mod verify` | Verify checksums |

## Project Structure

```
main.go                         # CLI entry point (scan / agent / serve / init / generate-descriptions)
opencode.json                   # MCP plugin configuration
LLM_INTEGRATION_CHARTER.md      # Charter for LLM integration guidelines
internal/
  helper/
    db.go                       # SQLite persistence layer (schema, write, read)
    json.go                     # JSON persistence layer (alternative serialization)
    apply.go                    # Description injection into source files (comment insertion)
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
      pyscanner/
        scanner.go              # Python Scanner implementation (Scan, UpdateFile, helpers)
        parser.go               # Python source AST parser (class, function extraction)
        resolver.go             # Function body reference resolution (call graphs)
        matcher.go              # Class-to-interface/ABC matching
        parse_script.go         # Python source parsing via script subprocess
    golang/
      resources.go              # Go-specific types: GolangFunction, GolangStruct, GolangInterface, GolangExternalVar, GolangFile, GolangPackage
      types.go                  # Output types: FunctionCut, StructCut, SimplifiedFunction, GoFunctionContext, GoStructContext, etc.
      connections.go            # ConnectionKind constants + typed accessor methods
      mapper.go                 # FromGeneric / ToGeneric (domain <-> golang)
      manager.go                # GoManager wrapping TopologyManager (ReadFunction, ReadStruct, FindFunctionsByName, FindStructsByName, ReadResourceAndCut, UpdateDescription)
    python/
      resources.go              # Python-specific types: PythonFunction, PythonClass, PythonExternalVar, PythonFile, PythonModule
      types.go                  # Output types: FunctionCut, StructCut, SimplifiedFunction, PyFunctionContext, PyStructContext, etc.
      connections.go            # PythonConnectionKind constants + typed accessor methods
      mapper.go                 # FromGeneric / ToGeneric (domain <-> python)
      manager.go                # PyManager wrapping TopologyManager (ReadFunction, ReadStruct, FindFunctionsByName, FindClassesByName, ReadResourceAndCut, UpdateDescription)
      plan.md                   # Python support implementation plan
    py_testdata/
      sample.py                 # Python test fixture
      test_module.py            # Python test module fixture
  llm/
    provider.go                 # LLM Provider interface + Message/Tool types
    providers/
      deepseek.go               # DeepSeek API implementation
    agent/
      agent.go                  # AI agent loop (max 20 iterations)
      prompt.go                 # System prompt (topology-aware)
    tools/
      tool.go                   # Tool interface + Registry
      read_file.go              # "read" tool (raw file read)
      edit.go                   # "edit" tool (replace text, auto-updates topology)
      ls.go                     # "ls" tool (list files/directories)
    languages/
      gotools/
        register.go             # Register Go-specific tools in tool registry
        read_function.go        # "read_function" tool (name-based lookup -> rich context)
        read_struct.go          # "read_struct" tool (name-based lookup -> rich context)
        read_resource_and_cut.go # "read_resource_and_cut" tool (source cut + description instructions)
        update_description.go   # "update_description" tool
        list_undocumented.go    # "list_undocumented" tool (list resources needing descriptions)
        list_warnings.go        # "list_warnings" tool (list topology warnings)
        format.go               # FormatGoFunctionContext, FormatGoStructContext formatters
        prompt.go               # BuildGoSystemPrompt for agent
      pythontools/
        register.go             # Register Python-specific tools in tool registry
        read_function.go        # "read_function" tool (name-based lookup -> rich context)
        read_struct.go          # "read_struct" tool (class-based lookup -> rich context)
        read_resource_and_cut.go # "read_resource_and_cut" tool (source cut + description instructions)
        update_description.go   # "update_description" tool
        list_undocumented.go    # "list_undocumented" tool (list resources needing descriptions)
        format.go               # FormatPyFunctionContext, FormatPyStructContext formatters
        prompt.go               # BuildPySystemPrompt for agent
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
ltp init              # adds MCP config to opencode.json
ltp init --global     # adds to ~/.config/opencode/opencode.json
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
- The scanner architecture supports multiple languages via `LanguageScanner` interface and `Registry`; Go and Python are currently implemented.
- `generate-descriptions` subcommand uses the LLM to auto-generate descriptions for all undocumented resources.
