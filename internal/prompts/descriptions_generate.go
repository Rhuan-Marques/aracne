package prompts

func DescriptionsGenerateCommand(executorAgentRef string) string {
	return "Generate descriptions for targeted undocumented resources from config using main-session orchestration.\n\n" +
		"Important: do not launch an orchestrator subagent. The main session must coordinate the work because subagents may not be able to launch other subagents reliably.\n\n" +
		"Workflow:\n" +
		"1. Get the current targeted undocumented resources. Prefer `node_list_no_description` if it is available; otherwise run `ltp node list --no-description`.\n" +
		"2. Split the returned resources into deterministic batches of at most 20 resources. Each resource ID must appear in exactly one active batch.\n" +
		"3. Launch one `" + executorAgentRef + "` subagent for each batch. Give each executor only its assigned IDs, names, and kinds.\n" +
		"4. Executors must manually study each assigned resource with `read_resource_and_cut`, then call `update_description`. They must not automate by code and must not process unassigned resources.\n" +
		"5. After each wave of executors completes, get the undocumented resource list again. Treat the topology database as the source of truth, not executor self-reports.\n" +
		"6. Reassign any still-undocumented targeted resources to new executor subagents, again in batches of at most 20, without duplicating active assignments.\n" +
		"7. Continue until no targeted undocumented resources remain, or report any resources that still fail after repeated executor attempts."
}
