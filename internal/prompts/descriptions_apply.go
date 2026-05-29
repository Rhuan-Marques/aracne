package prompts

func DescriptionsApplyCommand() string {
	return `Run ` + "`ltp descriptions apply`" + ` to write all topology descriptions back into the source files as Go doc comments. Report any files that were modified.`
}