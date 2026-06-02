package prompts

func BugHunterCommand() string {
	return `Launch a Bug Hunter sub-agent to scan the entire project topology for bugs.

## Workflow

1. First, call ` + "`" + `ls` + "`" + ` to understand the project structure
2. Launch the Bug Hunter as a sub-agent with these tools: ` + "`" + `ls` + "`" + `, ` + "`" + `read` + "`" + `, ` + "`" + `read_function` + "`" + `, ` + "`" + `read_struct` + "`" + `, ` + "`" + `read_resource_and_cut` + "`" + `, ` + "`" + `bug_report` + "`" + `
3. The Bug Hunter should systematically examine each resource in the topology
4. It should look for: nil dereferences, missing error checks, race conditions, off-by-one errors, dead code, security issues
5. For each bug found, it calls ` + "`" + `bug_report` + "`" + ` with the node ID and description
6. Report how many bugs were found
`
}
