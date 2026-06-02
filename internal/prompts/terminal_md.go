package prompts

func TerminalClaudeMdContent() string {
	return `# CLI Navigation via ltp

This project uses **llm-topology** for codebase navigation. Use the ltp CLI directly via terminal/bash.

## Navigation Commands

| Command | Purpose |
|---------|---------|
| ltp ls <path> | List files and directories |
| ltp read_file <path> | Read raw file contents |
| ltp read_function <name> | Function source + connected context (called functions, structs, interfaces) |
| ltp read_struct <name> | Struct source + methods, interfaces, constructor |
| ltp read-resource-and-cut <id> <kind> | Get source code for any resource |
| ltp edit | Edit a file (reads JSON from stdin) |
| ltp list-undocumented | List all resources missing descriptions |
| ltp update-description <id> <kind> <desc> | Update a resource description |
| ltp update-file <path> | Re-parse a file and update the topology |
| ltp warnings list | List topology warnings |
| ltp check-updates | List files changed since last scan |

## Bug Commands

| Command | Purpose |
|---------|---------|
| ltp bug report --node <id> --description <text> | Report a bug on a resource node |
| ltp bug list [--node <id>] [--state <state>] | List known bugs |
| ltp bug acknowledge <bugID> | Mark a bug as acknowledged |
| ltp bug dismiss <bugID> | Mark a bug as dismissed |
| ltp bug delete <bugID> | Delete a bug from the database |

## How to Use

1. Explore structure: ltp ls .
2. Investigate code: ltp read_function <name> or ltp read_struct <name>
3. Edit files: use standard file tools, then ltp update-file <path> to sync topology
4. Document: ltp list-undocumented then ltp read-resource-and-cut + ltp update-description

## Bug Workflow

1. ltp bug report --node <id> --description "..." — report a potential bug
2. ltp bug list — triage pending bugs
3. ltp bug acknowledge <id> — confirm real bugs
4. ltp bug dismiss <id> — dismiss false positives
5. ltp bug delete <id> — remove from database after fixing
`
}
