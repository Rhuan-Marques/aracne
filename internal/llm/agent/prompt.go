package agent

import (
	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/prompts"
)

// BuildPrompt is the system prompt aracne's own harness sends.
//
// It is `prompts.ContractContent` and nothing else. This function used to dispatch on the
// topology's language to one of five `Build<Lang>SystemPrompt` constants under
// internal/llm/languages, each of which re-said what a read returns, how an ID is spelled and
// how to spend a context window -- the same claims CLAUDE.md and AGENTS.md were already
// making, in different words, with no way for a change to one to reach the other. The "multi"
// case concatenated all five, which sent the model six competing descriptions of one read
// format.
//
// The two things those prompts said that the contract did not are arguments now:
// `contract_verbosity: "high"` restores their length, and languages carries the per-language
// half. What is gone is the LLM_INTEGRATION_CHARTER.md prepend -- an undocumented optional file
// no other surface read, whose content would have had to land above the `# Aracne` heading
// that `arac setup` and `arac disable` locate a generated block by.
func BuildPrompt(cfg *helper.Config, languages []string) string {
	return prompts.SystemPromptForConfig(cfg, languages)
}
