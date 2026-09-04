package prompts

import (
	"github.com/Rhuan-Marques/aracne/internal/helper"
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

// endingSection closes the generated block.
//
// It doubles as the end MARKER: `arac init` finds the block by its opening heading and this
// line so a re-run REPLACES it instead of stacking a second copy, and `arac disable` uses the
// same pair to remove it. Without a findable end both fall back to appending.
//
// ModeAracneRead closes on its own last instruction instead (prompts.AracneReadClosingLine),
// which cli matches as well -- see aracIntegrationEndMarkers for why the marker is always a
// real line of the contract rather than something invisible added for the parser.
func endingSection() string {
	return "Good Luck in your task.\n"
}
