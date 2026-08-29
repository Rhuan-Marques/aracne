package prompts

// Command string to launch Bug Judge sub-agents for triaging pending bugs.
func BugJudgeCommand() string {
	return `Launch Bug Judge sub-agents to triage all pending bugs.

The Bug Judge agent must use only its restricted tools: read, grep, bug_list, bug_acknowledge, bug_dismiss, and bug_delete. It should acknowledge real bugs, dismiss false positives, and delete duplicates of known dismissed patterns.`
}
