# CLAUDE.md

This project uses **llm-topology** for codebase navigation. The topology database provides a pre-analyzed graph of all functions, structs, interfaces, variables, and their relationships.

## Navigation Tools

Prefer these topology-aware tools over standard file reading:

| Tool | Purpose |
|------|---------|
| `read_function` | Function source + connected context (called functions, structs, interfaces) |
| `read_struct` | Struct source + methods, interfaces, constructor |
| `read_resource_and_cut` | Get source code + description instructions for any resource |
| `update_description` | Persist a description into the topology database |
| `list_undocumented_resources` | List all resources missing descriptions |
| `ls` | List files and directories |
| `read` | Read raw file contents (use only when topology tools aren't sufficient) |

## Bug Tracking Tools

| Tool | Purpose |
|------|---------|
| `bug_report` | Report a bug on a resource node (starts as pending) |
| `bug_list` | List known bugs (filterable by node or state) |
| `bug_acknowledge` | Mark a bug as acknowledged (confirmed, needs fixing) |
| `bug_dismiss` | Mark a bug as dismissed (false positive, kept for reference) |
| `bug_delete` | Delete a bug from the database |

## Bug Workflow

1. Use /bug-hunter to scan the topology for potential bugs
2. Use /bug-judge to triage pending bugs (acknowledge real ones, dismiss false positives)
3. Use /bug-solver to fix acknowledged bugs

## How to Use

1. Start with `ls` to explore the project structure
2. Use `read_function` or `read_struct` to investigate code — these return both source code AND a # CONTEXT: section showing all connected resources
3. After editing a file, run `ltp update-file <path>` to keep the topology in sync

## Description Generation

To document the project:
1. Call `list_undocumented_resources`
2. For each resource, call `read_resource_and_cut` followed by `update_description`

## Guidelines

- Prefer topology tools over raw file reads — they provide richer context
- The # CONTEXT: section in tool output often answers follow-up questions without extra calls
- Keep descriptions concise (1-3 lines for functions/structs/interfaces)
