# CLAUDE.md

# LTP Integration

This project uses **llm-topology** for codebase navigation. The topology database provides a pre-analyzed graph of all functions, structs/classes, interfaces, variables, and their relationships.

## MCP Tools

Use these llm-topology MCP tools for MCP-mode topology operations:

| Tool | Purpose |
|------|---------|
| `mcp__llm-topology__read_file` | Read raw file contents |
| `mcp__llm-topology__edit` | Edit files and update topology automatically |
| `mcp__llm-topology__write` | Write files and update topology automatically |
| `mcp__llm-topology__read_function` | Function source + connected context |
| `mcp__llm-topology__read_struct` | Struct source + methods/interfaces/context |
| `mcp__llm-topology__warnings_list` | List topology warnings |
| `mcp__llm-topology__bug_report` | Report a confirmed bug on a resource node |



This is it for ltp integration
