package prompts

func DescriptionsGenerateCommand() string {
	return "Use the descriptor agent from `.claude/agents/descriptor.md` to generate descriptions for targeted undocumented resources from config.\n\n" +
		"The descriptor agent must call `list_undocumented_resources`, then for each returned resource call `read_resource_and_cut` and `update_description`. Process ALL resources listed and report how many descriptions were generated."
}
