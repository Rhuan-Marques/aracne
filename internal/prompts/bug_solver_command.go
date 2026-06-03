package prompts

func BugSolverCommand() string {
	return `Launch Bug Solver sub-agents to fix acknowledged bugs.

The Bug Solver agent must use only its restricted tools: read/read_file, edit, write, read_function, read_struct, and bug_delete. It should fix acknowledged bugs with minimal changes and delete the bug report after the fix is complete.`
}
