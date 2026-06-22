package prompts

import "fmt"

// Returns instructions for orchestrating batch generation of descriptions via concurrent executor subagents.
func DescriptionsGenerateCommand(executorAgentRef string, batchSize int) string {
	if batchSize <= 0 {
		batchSize = 5
	}
	return "Generate descriptions for all targeted undocumented resources, orchestrated from the main session.\n\n" +
		"You orchestrate; do not delegate orchestration to a subagent (subagents can't reliably spawn subagents). Batch size: " + fmt.Sprint(batchSize) +
		"1. List targeted undocumented resources: prefer `node_list_no_description`, else run `arac resource list --no-description`.\n" +
		"2. Split them into deterministic, non-overlapping batches of at most the batch size — every id in exactly one batch.\n" +
		"3. Launch one `" + executorAgentRef + "` subagent per batch, all in a single concurrent wave. Give each only its assigned ids, names, and kinds.\n" +
		"4. When the wave finishes, re-list undocumented resources. The topology database is the source of truth, not executor self-reports.\n" +
		"5. Re-batch and re-launch any still-undocumented resources in another concurrent wave. Repeat until none remain, then report the ids that kept failing (if any)."
}
