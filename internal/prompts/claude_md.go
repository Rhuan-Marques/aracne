package prompts

import (
	"aracne/internal/helper"
)

// ClaudeMdForConfig renders the contract for the mode this project is in.
//
// One function, four modes, no assembly: the old version built the MCP contract and then
// APPENDED a terminal section when both surfaces were on, which is how a project ended up being
// told about `mcp__aracne__grep` and an intercepted `cat` in the same document. Mode "both" is
// gone, and with it the only case that needed splicing.
func ClaudeMdForConfig(cfg *helper.Config) string {
	return ContractContent(cfg)
}

// AgentsMdForConfig is ClaudeMdForConfig for OpenCode. The contract is surface-shaped, not
// harness-shaped, so the two are the same document.
func AgentsMdForConfig(cfg *helper.Config) string {
	return ContractContent(cfg)
}

func behavioralRulesSection() string {
	return `## Behavioral Rules

1. **Be concise** -- report what you found and what you changed, not how you did it.
2. **Trust the topology** -- it is the source of truth and re-syncs after every edit. Never parse
   code by hand, and never ask for a re-scan.
3. **Do not guess** -- report an empty result or an error as what it is; never invent code or
   relationships.
4. **Do not re-read** -- if it is already in your context, use it.

`
}

// Returns closing goodbye text for generated prompt markdown
func endingSection() string {
	return "Good Luck in your task.\n"
}
