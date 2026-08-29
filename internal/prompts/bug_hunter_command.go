package prompts

// Returns a command description for launching a Bug Hunter sub-agent to audit the codebase.
func BugHunterCommand() string {
	return `Launch a Bug Hunter sub-agent to scan the project topology for bugs.

The Bug Hunter agent must use only its restricted tools: read, grep, and bug_report. It should report only confirmed correctness, reliability, or security bugs.`
}
