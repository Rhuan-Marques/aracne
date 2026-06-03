# LTP Integration

This project uses **llm-topology** for codebase navigation. The topology database provides a pre-analyzed graph of all functions, structs/classes, interfaces, variables, and their relationships.

## MCP Tools

Use these llm-topology MCP tools for MCP-mode topology operations:

| Tool | Purpose |
|------|---------|
| `llm-topology_read_file` | Read raw file contents |
| `llm-topology_edit` | Edit files and update topology automatically |
| `llm-topology_write` | Write files and update topology automatically |
| `llm-topology_read_function` | Function source + connected context |
| `llm-topology_read_struct` | Struct source + methods/interfaces/context |
| `llm-topology_warnings_list` | List topology warnings |
| `llm-topology_bug_report` | Report a confirmed bug on a resource node |



This is it for ltp integration
