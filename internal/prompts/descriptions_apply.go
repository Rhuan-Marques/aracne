package prompts

// Returns instructions to apply all topology descriptions back into source files as Go doc comments.
func DescriptionsApplyCommand() string {
	return `Run ` + "`arac descriptions apply`" + ` to write all topology descriptions back into the source files as Go doc comments. Report any files that were modified.`
}
