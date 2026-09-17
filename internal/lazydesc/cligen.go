package lazydesc

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/prompts"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The CLI transport: describe a batch by running a command instead of calling an HTTP API.
//
// WHY. The API providers need an API key, which is a separate thing to buy from the Claude
// Code subscription a user is already paying for -- and the benchmark harness has always
// generated its descriptions this way (bench/gen_descriptions.py drives `claude --print`), so
// a project that can sweep descriptions could not lazily fill them. This closes that gap: the
// same auth, the same quota, no key.
//
// It is never reached by inference. An API key, where present, stays preferred, because a CLI
// run is slower per batch (a process launch and a full CLI startup, versus one HTTP request)
// and its quota is the interactive one the user is also typing into.
// ProviderCLI runs whatever `descriptions.cli_provider_command` names. The prompt goes in on
// stdin and the reply is read from stdout, which is the whole contract -- any command that
// answers a prompt on stdout can write this project's descriptions.
//
// There used to be a second name, `claude_cli`, that prefilled the Claude Code invocation. It
// is gone: it was one transport wearing two names, and the prefilled half could not be seen,
// tuned or pointed at anything else. `cli` with "claude --print --max-turns 1" is the same run
// with the command in the config where the project can read it.
const ProviderCLI = helper.ProviderNameCLI

// IsCLIProvider reports whether a config asks for a CLI transport rather than an API.
//
// Exported because the choice changes the SHAPE of a generation run, not just its transport:
// `arac descriptions generate` drives an agent loop over an llm.Provider, and there is no
// agent loop to drive over a command that answers once. The sweep asks this before it decides
// which of the two runners to build.
func IsCLIProvider(cfg helper.ResolvedLazyDescriptions) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.Provider), ProviderCLI)
}

// NewCLIGenerator builds the CLI transport for a config, or (nil, error) when it cannot run.
//
// A missing binary is an error rather than a silent nil so the sweep can say WHY it is not
// describing anything. The lazy fill, which has nowhere to print that, drops it -- see
// GeneratorFactory.
func NewCLIGenerator(cfg helper.ResolvedLazyDescriptions) (Generator, error) {
	if !IsCLIProvider(cfg) {
		return nil, fmt.Errorf("provider %q is not a CLI transport", cfg.Provider)
	}
	if len(cfg.CLICommand) == 0 {
		return nil, fmt.Errorf("descriptions provider %q needs cli_provider_command", ProviderCLI)
	}
	g := &cliGenerator{argv: append([]string(nil), cfg.CLICommand...)}
	if _, err := exec.LookPath(g.argv[0]); err != nil {
		return nil, fmt.Errorf("cli provider: %w", err)
	}
	return g, nil
}

// cliGenerator describes a batch by running a command: prompt on stdin, reply on stdout.
type cliGenerator struct {
	argv []string
}

// Describe sends one batch and parses the reply.
//
// The prompt construction and the parsing are shared verbatim with the HTTP generator, so the
// two transports cannot drift into producing different descriptions for the same batch.
func (g *cliGenerator) Describe(ctx context.Context, batch Batch) (map[string]string, error) {
	if len(batch.Resources) == 0 {
		return nil, nil
	}
	resources := make([]prompts.DescriptionResource, 0, len(batch.Resources))
	wanted := make(map[string]domain.ResourceKind, len(batch.Resources))
	for _, req := range batch.Resources {
		resources = append(resources, prompts.DescriptionResource{
			ID: req.ID, Name: req.Name, Kind: req.Kind, ReadOutput: req.Source,
		})
		wanted[req.ID] = req.Kind
	}

	// Instructions and batch go in as one stream. No flag for a system prompt is common to
	// every CLI, and one stdin is: a transport that works for anything the project points it
	// at is worth more here than a flag that works for one vendor.
	input := prompts.LazyDescriptionsPrompt() + "\n\n" +
		prompts.LazyDescriptionsInput(resources, batch.Exemplars)

	cmd := exec.CommandContext(ctx, g.argv[0], g.argv[1:]...)
	// The command leads its own process group, and cancelling kills that GROUP rather than just
	// the process.
	//
	// CommandContext on its own kills the child and nothing else, which is the wrong shape for
	// this particular child: the usual CLI provider is `claude -p`, which starts subprocesses of
	// its own -- its hooks, its own tool calls. Killing only the top of that tree leaves the
	// rest running, and spending, with nothing left that knows they exist. That is survivable
	// when a person is watching the run; it is a leak when the caller is a detached description
	// worker that has just been cut short by its own watchdog.
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessTree(cmd.Process) }
	cmd.Stdin = strings.NewReader(input)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(errb.String())
		if len(detail) > 200 {
			detail = detail[:200]
		}
		return nil, fmt.Errorf("%s: %w: %s", g.argv[0], err, detail)
	}
	return prompts.ParseLazyDescriptions(out.String(), wanted), nil
}
