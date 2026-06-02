package prompts

func BugSolverCommand() string {
	return `Launch Bug Solver sub-agents to fix all acknowledged bugs.

## Workflow

1. Call ` + "`" + `bug_list` + "`" + ` with state=acknowledged to get all bugs needing fixes
2. For each acknowledged bug:
   a. Launch a Bug Solver sub-agent with the bug ID and description
   b. The Bug Solver tools: ` + "`" + `ls` + "`" + `, ` + "`" + `read` + "`" + `, ` + "`" + `read_function` + "`" + `, ` + "`" + `read_struct` + "`" + `, ` + "`" + `edit` + "`" + `, ` + "`" + `bug_delete` + "`" + `
   c. After fixing, it calls ` + "`" + `bug_delete` + "`" + ` to remove the bug
3. Continue launching Bug Solvers until all acknowledged bugs are resolved
4. Run Bug Solvers in parallel when possible
`
}
