package prompts

// Command string to launch Bug Solver sub-agents for fixing acknowledged bugs with minimal changes.
func BugSolverCommand() string {
	return `Launch Bug Solver sub-agents to fix acknowledged bugs.

The Bug Solver agent must use only its restricted tools: read, grep, edit, write, warnings_list, and bug_delete. It should fix acknowledged bugs with minimal changes and delete the bug report after the fix is complete.`
}
