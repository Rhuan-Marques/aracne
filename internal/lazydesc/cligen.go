package lazydesc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"aracne/internal/prompts"
	"aracne/internal/topology/domain"
)

// ProviderClaudeCLI runs a fill through the Claude Code CLI (`claude --print`) instead of an
// HTTP API.
//
// WHY. The API providers need an API key, which is a separate thing to buy from the Claude
// Code subscription a user is already paying for -- and the benchmark harness has always
// generated its descriptions this way (bench/gen_descriptions.py drives `claude --print`), so
// a project that can sweep descriptions could not lazily fill them. This closes that gap: the
// same auth, the same quota, no key.
//
// It is the LAST resort in the probe order, because it is slower per batch (a process launch
// and a full CLI startup, versus one HTTP request) and its quota is the interactive one the
// user is also typing into. An API key, where present, stays preferred.
const ProviderClaudeCLI = "claude_cli"

// claudeCLIModel maps a configured model onto what `claude --model` accepts. The CLI takes its
// own short aliases, so a config that already says "haiku" needs no translation, while a full
// API id has to be handed over as-is for the CLI to resolve or reject.
func claudeCLIModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch m {
	case "", "haiku", "claude-haiku-4-5":
		return "haiku"
	case "sonnet", "claude-sonnet-4-6":
		return "sonnet"
	case "opus", "claude-opus-4-8":
		return "opus"
	}
	return model
}

// claudeCLIAvailable reports whether the CLI can be used as a generator.
func claudeCLIAvailable() bool {
	_, err := exec.LookPath(claudeCLIBinary())
	return err == nil
}

// claudeCLIBinary is the executable to run, overridable for tests and for a project that
// ships the CLI somewhere unusual.
func claudeCLIBinary() string {
	if v := strings.TrimSpace(os.Getenv("ARACNE_CLAUDE_CLI")); v != "" {
		return v
	}
	return "claude"
}

// cliGenerator describes a batch by shelling out to `claude --print`.
type cliGenerator struct {
	bin   string
	model string
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

	// --max-turns 1 keeps this a single completion: the batch already carries every resource's
	// source inline, so there is nothing for a tool call to fetch, and a fill that started
	// exploring the repository would be a fill that can trigger a fill.
	cmd := exec.CommandContext(ctx, g.bin,
		"--print",
		"--model", g.model,
		"--max-turns", "1",
		"--append-system-prompt", prompts.LazyDescriptionsPrompt(),
	)
	cmd.Stdin = strings.NewReader(prompts.LazyDescriptionsInput(resources, batch.Exemplars))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(errb.String())
		if len(detail) > 200 {
			detail = detail[:200]
		}
		return nil, fmt.Errorf("claude cli: %w: %s", err, detail)
	}
	return prompts.ParseLazyDescriptions(out.String(), wanted), nil
}
