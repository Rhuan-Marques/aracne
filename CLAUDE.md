# Aracne Project Integration

This project uses **aracne** for codebase navigation. The topology database provides a pre-analyzed graph of all functions, structs/classes, interfaces, variables, and their relationships.

## Navigation Model

The topology is a directed graph can enhance your information about the repository you're using if you use it correctly.

**Navigation Flow:**
1. Use `ls` to understand the project file layout
2. Use lookup MCP tools to get a resource's full context with interconnected relationships

**Note: Never try to use `read` native tool, use MCP lookups instead**

## MCP Lookup tools:
- `mcp__aracne__read_function`: Reads the function and context for resources it uses, receives a function ID.
- `mcp__aracne__read_struct`: Reads the struct and context for resources it uses, receives a struct ID.
- `mcp__aracne__read_interface`: Reads the interface and context for which resources it is implemented by, receives an interface ID.
- `mcp__aracne__read_file`: Reads the content of a file, receives the file path.

Note: Do *not* use "cat", "Get-Content" or any other OS command to read files## Grep/Search

Use the MCP tool `mcp__aracne__grep` for content search. It returns `path:line:match` plus `ResourceID` and `Description` when a match maps to a topology resource.

Do *not* use your native `grep` tool.
Do not use `grep`, `Select-String` or `rg` in the terminal## Resource Context

When you call a MCP Lookup Tool, the output has two sections:

**Code Block:** The resource's full source code, plus relevant imports and enclosing type (for methods).

**`# CONTEXT:` Section:** A structured hierarchical listing of everything the resource touches. Each entry is keyed by the resource's full ID, which you can pass directly to a lookup tool to drill deeper:

```
# CONTEXT:
## pkg.InterfaceName: Description
    pkg.ImplStruct: Description
        pkg.(ImplStruct).Method: Description
## pkg.OtherStruct: Description
    pkg.(OtherStruct).Method: Description
## pkg.CalledFunction: Description
## pkg.ExtVarName = value
```

Use the CONTEXT section to understand relationships **without making additional tool calls**.

## Edit and Write:

You can edit files using the MCP tool `mcp__aracne__edit`.
You can write files using the MCP tool `mcp__aracne__write`.
After editing or writing, the context for the topology will be automatically updated to reflect your actions.

**Note: NEVER try to edit or write using your native tools**

## Other:

- If you find a bug that is not relevant to your task, *do not fix it*. Instead, report it using `mcp__aracne__bug_report`
- If you want to check for any topology warnings, you can do it using `mcp__aracne__warnings_list`

## Tool Guard

An `arac guard` hook watches your tool calls. Whenever you use a native tool (`Read`/`Grep`/`Edit`/`Write`) or a shell equivalent (`cat`/`head`/`tail`/`less`/`grep`/`rg`/`sed`/`awk`, or PowerShell `Get-Content`/`Select-String`), you are reminded to use the matching aracne MCP tool instead.

Any tool listed in `blocked_tools` for the `claude_code` harness is blocked outright. Blocking `grep` also blocks `grep`/`rg`/`Select-String` run through the Bash tool; blocking `bash` blocks the Bash tool entirely. The guard uses the main agent's `blocked_tools`; per-sub-agent blocking is enforced by each sub-agent's tool allow-list, not by this hook.

## How to Navigate:

### 1: Explore Topology, NOT Files
Use the topology manager to your advantage, only read entire files when:
    - They are NOT supported language files (.go and .py)
    - Your tasks requires you to know all the information from the entire file
    - You don't know the other resources IDs yet

### 2: Let Descriptions Guide You
- A resource's description can tell you whether it is relevant to your task
- If the resource's description makes it look irrelevant to your task, skip it
- If you only need to understand what a resource does / is, descriptions can be *enough*. You don't need to read resources when the descriptions already gave you the necessary context

### 3: Go Deeper with Intent
- When exploring, ask yourself *what* you need to discover and understand fully.
- Which resources do you need to **know the code** of.
- These questions should guide you to navigate deeper in the topology to do your task to its best

### 4: Beware of TopologyWarnings
- When **editing**, you'll usually receive helpful warnings on resources that might have been affected by your changes. Keep those in mind and solve them as they come up

## Behavioral Rules

1. **Be concise** — Prefer short answers. Show what you found and what you changed, not how you did it.
2. **Do not parse code yourself** — Always use topology tools. The database is the source of truth.
3. **Do not guess** — If a tool returns no results or an error, report it accurately. Do not fabricate code or relationships.
4. **One level deep** — Read the CONTEXT section and only drill deeper when essential. Descriptions are designed to answer most questions at the surface level.
5. **Topology is always current** — After any `edit`, the topology updates automatically. You never need to request a re-scan.
6. **Avoid circular exploration** — If you already read a resource, do not re-read it in the same session. Trust your context.

Good Luck in your task.
