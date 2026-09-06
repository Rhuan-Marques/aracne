package cli

import (
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// First-run setup for `arac descriptions generate`.
//
// WHY THIS EXISTS. Description generation needs two facts nothing can derive: who writes the
// descriptions, and how to reach them. The config used to answer both by accident -- the
// descriptions executor was pinned to "haiku", the provider was inferred from that model, and
// so a project that had never been asked anything still resolved to Anthropic. It then failed
// with "no LLM provider configured" (or, on the lazy path, with silence), naming a vendor the
// user never chose and a key they had no reason to hold. The default was doing the choosing
// and the error was blaming the user for it.
//
// So the defaults are gone and the questions are real. Blank config means unanswered, and the
// sweep -- which is a person at a terminal waiting for descriptions, the one place where
// asking is cheap -- asks:
//
//	API key or a command?  ->  which wire format?  ->  which environment variable?
//	                       ->  which command? (with what it spends spelled out)
//
// The answers are written to .aracne/config.json, so this is a first-run flow and not a
// per-run interrogation. A value that goes missing later is asked for again on its own: a
// project that has chosen `openai` and lost only `api_key_env` is asked one question, not
// four.
//
// The lazy fill on the read path deliberately never reaches this. A read is not the place to
// stop and ask which vendor to bill.

// defaultDescribeCLICommand is what a bare `--cli`, and the CLI question below, offer.
//
// `claude -p` and not the fuller `claude --print --model haiku --max-turns 1`: a default is a
// suggestion, and the moment it starts pinning a model it is making a spending decision on the
// user's behalf that they did not type. A project that wants the tuned invocation writes it,
// in the flag or in cli_provider_command, where it can read what it is paying for.
const defaultDescribeCLICommand = "claude -p"

// The answers, as the questions that fill them. Ordered: the provider first, because which of
// the others are even asked depends on it.
const (
	answerProvider   = "provider"
	answerAPIKeyEnv  = "api_key_env"
	answerCLICommand = "cli_provider_command"
)

// missingDescriptionAnswers lists what is still unanswered, in the order it must be asked.
//
// The old `descriptions.lazy.provider` spelling counts as an answer. A project that named its
// provider before the key moved onto the section has answered the question, and re-asking it
// would be the migration path punishing the people who took it.
func missingDescriptionAnswers(d *helper.DescriptionsSection) []string {
	provider := effectiveConfiguredProvider(d)
	if provider == "" {
		// Everything downstream depends on this one, so it is the only thing to report:
		// which of the two follow-ups applies is not known until it is answered.
		return []string{answerProvider}
	}
	var missing []string
	switch {
	case strings.EqualFold(provider, helper.ProviderNameCLI):
		if argv, err := helper.SplitCommand(d.CLIProviderCommand); err != nil || len(argv) == 0 {
			missing = append(missing, answerCLICommand)
		}
	case helper.IsAPIProvider(provider):
		if strings.TrimSpace(d.APIKeyEnv) == "" {
			missing = append(missing, answerAPIKeyEnv)
		}
	}
	return missing
}

// effectiveConfiguredProvider is the provider the config states, in either spelling, or "".
func effectiveConfiguredProvider(d *helper.DescriptionsSection) string {
	if p := strings.ToLower(strings.TrimSpace(d.Provider)); p != "" {
		return p
	}
	return strings.ToLower(strings.TrimSpace(d.Lazy.Provider))
}

// unansweredSetupError is what an unconfigured sweep gets: the command that answers these
// questions properly, and underneath it the keys that answer them by hand.
//
// Both halves matter. `arac init` is the answer for a person, and naming it is what stops this
// from being an error that describes a problem without a fix. The keys are the answer for
// everything else -- a Dockerfile, a CI job, a config templated by a repo's own tooling -- none
// of which is going to run a full-screen wizard.
//
// It is the same error on a terminal and off one, which is new: this used to be the
// non-interactive half of a flow whose interactive half asked. There is no interactive half
// here any more, so there is nothing for the presence of a terminal to change.
func unansweredSetupError(d *helper.DescriptionsSection) error {
	var b strings.Builder
	b.WriteString("descriptions generation is not configured.\n\nRun `arac init` to set it up, " +
		"or write it into .aracne/config.json yourself:\n")
	for _, missing := range missingDescriptionAnswers(d) {
		switch missing {
		case answerProvider:
			fmt.Fprintf(&b, "\n  \"descriptions\": {\"provider\": \"anthropic\", \"api_key_env\": \"ANTHROPIC_API_KEY\"}\n"+
				"  \"descriptions\": {\"provider\": \"cli\", \"cli_provider_command\": %q}\n"+
				"\n(providers: %s, %s)\n",
				defaultDescribeCLICommand,
				strings.Join(helper.APIProviderNames(), ", "), helper.ProviderNameCLI)
		case answerAPIKeyEnv:
			provider := effectiveConfiguredProvider(d)
			fmt.Fprintf(&b, "\nprovider %q needs the variable its key comes from:\n"+
				"  \"descriptions\": {\"api_key_env\": %q}\n",
				provider, helper.DefaultAPIKeyEnv(provider))
		case answerCLICommand:
			fmt.Fprintf(&b, "\nprovider %q needs the command to run:\n"+
				"  \"descriptions\": {\"cli_provider_command\": %q}\n"+
				"or pass it for this run only: --cli %q\n",
				helper.ProviderNameCLI, defaultDescribeCLICommand, defaultDescribeCLICommand)
		}
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}
