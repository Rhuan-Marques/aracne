# CLAUDE.md

# Aracne Project Integration

This project uses **aracne** for codebase navigation. The topology database provides a pre-analyzed graph of all functions, structs/classes, interfaces, variables, and their relationships.

## Navigation Model

The topology is a directed graph. Resources have Kind, Name, ID, Description, Location, and Connections (edges to other resources).

**Navigation Flow:**
1. Use `ls` to understand the project file layout
2. Use named lookups to get a resource's full context with interconnected relationships
3. Use `read` for raw file contents when you need to see surrounding code

**Critical:** Prefer named lookups over `read`. Named lookups return not just the source code but also the interconnected context: called functions, related types, interfaces, dependencies, and more.

## Resource Context

When you call a read split tool (e.g. `read_function`), the output has two sections:

**Code Block:** The resource's full source code, plus relevant imports and enclosing type (for methods).

**`# CONTEXT:` Section:** A structured hierarchical listing of everything the resource touches:

```
# CONTEXT:
## InterfaceName: Description
    ImplStruct: Description
        ImplStruct.Method: Description
## OtherStruct: Description
    OtherStruct.Method: Description
## CalledFunction: Description
## ExtVarName = value
```

Use the CONTEXT section to understand relationships **without making additional tool calls**.

## Priorities

### Tier 1: Understand Before Acting
- Explore with `ls`, then use named lookups to understand the code you need to change
- Read the CONTEXT section thoroughly — it often answers your questions without extra tool calls
- Only drill deeper (another named lookup) when the description indicates something critical

### Tier 2: Prefer Rich Tools Over Raw Tools
- Named lookups > `read`
- Let the topology do the heavy lifting

### Tier 3: Let Descriptions Guide You
- A resource's description tells you whether it is relevant to your task
- If a description is empty and the resource is peripheral, skip it
- If a description is empty and the resource is central to your task, generate one using the descriptions command

### Tier 4: Edit, Then Verify
- `edit` auto-updates the topology — no manual re-scan needed
- After an edit, check for **TopologyWarning** output. These warn about removed functions, signature changes, and resources needing manual review
- Address warnings when they affect your task scope

## Description Generation

When asked to document the project or generate descriptions:

1. Use the `descriptions-generate` command to launch a sub-agent that processes undocumented resources
2. Each executor reads source code via `read` and generates concise descriptions (1-3 lines)
3. Descriptions are persisted with `update_description`
4. Run the command again to verify all targeted resources are documented

## Behavioral Rules

1. **Be concise** — Prefer short answers. Show what you found and what you changed, not how you did it.
2. **Do not parse code yourself** — Always use topology tools. The database is the source of truth.
3. **Do not guess** — If a tool returns no results or an error, report it accurately. Do not fabricate code or relationships.
4. **One level deep** — Read the CONTEXT section and only drill deeper when essential. Descriptions are designed to answer most questions at the surface level.
5. **Topology is always current** — After any `edit`, the topology updates automatically. You never need to request a re-scan.
6. **No circular exploration** — If you already read a resource, do not re-read it in the same session. Trust your context.

## MCP Tools

Use these Claude Code MCP tools for MCP-mode topology operations:

| Tool | Purpose |
|------|---------|
| `mcp__aracne__read` | Read any resource by ID |
| `mcp__aracne__edit` | Edit files and update topology automatically |
| `mcp__aracne__write` | Write files and update topology automatically |
| `mcp__aracne__warnings_list` | List topology warnings |
| `mcp__aracne__bug_report` | Report a confirmed bug on a resource node |

This is it for Aracne Project Integration
