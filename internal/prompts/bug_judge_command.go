package prompts

func BugJudgeCommand() string {
	return `Launch Bug Judge sub-agents to triage all pending bugs.

The Bug Judge agent must use only its restricted tools: read, read_function, read_struct, bug_list, bug_acknowledge, bug_dismiss, and bug_delete. It should acknowledge real bugs, dismiss false positives, and delete duplicates of known dismissed patterns.`
}
