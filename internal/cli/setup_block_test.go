package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/prompts"
)

// contractFor renders the contract a project in `mode` would get.
func contractFor(mode string) string {
	cfg := helper.DefaultConfig()
	cfg.Mode = mode
	return prompts.ClaudeMdForConfig(cfg, nil)
}

// A `# Aracne` heading the TEAM wrote is not the generated block. The first one in the file used
// to be taken as the block's start; with no closing line after it the bounds ran to the next
// top-level heading, and `arac setup` replaced the team's notes with the contract, unprompted.
func TestSetupKeepsAProjectsOwnAracneSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	own := "# My Project\n\nSome notes.\n\n# Aracne\n\nWe use aracne here. See docs/aracne.md.\n" +
		"IMPORTANT: never run `arac scan --hard`.\n\n# Build\n\nRun `make build`.\n"
	if err := os.WriteFile(path, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}

	writeMarkdownIntegrationFile(path, "CLAUDE.md", contractFor(helper.ModeCLI))
	first, _ := os.ReadFile(path)
	for _, want := range []string{"We use aracne here.", "never run `arac scan --hard`", "Run `make build`.",
		prompts.AracneReadClosingLine} {
		if !strings.Contains(string(first), want) {
			t.Errorf("after setup the file is missing %q:\n%s", want, first)
		}
	}
	if n := strings.Count(string(first), "\n# Aracne\n"); n != 2 {
		t.Errorf("want the team's heading plus exactly one generated block, got %d `# Aracne` headings:\n%s", n, first)
	}

	// Re-rendering -- here into a different mode -- replaces the generated block and only it.
	writeMarkdownIntegrationFile(path, "CLAUDE.md", contractFor(helper.ModeMCP))
	second, _ := os.ReadFile(path)
	if !strings.Contains(string(second), "We use aracne here.") {
		t.Errorf("a re-render removed the team's section:\n%s", second)
	}
	if strings.Contains(string(second), prompts.AracneReadClosingLine) {
		t.Errorf("the previous contract was not replaced:\n%s", second)
	}
	if n := strings.Count(string(second), "\n# Aracne\n"); n != 2 {
		t.Errorf("a re-render stacked a second block: %d `# Aracne` headings:\n%s", n, second)
	}

	// And a third run with nothing changed is a no-op.
	writeMarkdownIntegrationFile(path, "CLAUDE.md", contractFor(helper.ModeMCP))
	if third, _ := os.ReadFile(path); string(third) != string(second) {
		t.Errorf("an unchanged re-render rewrote the file:\n--- before\n%s\n--- after\n%s", second, third)
	}
}

// The recognition rules must still find every block aracne DID write: the current wording at
// either verbosity, a block whose closing line the reader deleted, and a block an older binary
// wrote in words this one no longer renders.
func TestSetupStillReplacesBlocksItWrote(t *testing.T) {
	for name, existing := range map[string]string{
		"current, low":        "# Title\n\n" + contractFor(helper.ModeInterceptID) + "\n# After\n",
		"closing line gone":   "# Title\n\n" + strings.Replace(contractFor(helper.ModeMCP), "Good Luck in your task.\n", "", 1) + "\n# After\n",
		"older wording":       "# Title\n\n# Aracne\n\nOld prose from an earlier release.\n\nGood Luck in your task.\n\n# After\n",
		"older wording (cli)": "# Title\n\n# Aracne\n\nOld prose.\n\n" + prompts.AracneReadClosingLine + "\n\n# After\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "AGENTS.md")
			if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
				t.Fatal(err)
			}
			writeMarkdownIntegrationFile(path, "AGENTS.md", contractFor(helper.ModeCLI))
			got, _ := os.ReadFile(path)
			if n := strings.Count(string(got), "# Aracne\n"); n != 1 {
				t.Errorf("want the block replaced in place, got %d `# Aracne` headings:\n%s", n, got)
			}
			if strings.Contains(string(got), "Old prose") {
				t.Errorf("the old block survived:\n%s", got)
			}
			if !strings.Contains(string(got), "# Title") || !strings.Contains(string(got), "# After") {
				t.Errorf("content around the block was lost:\n%s", got)
			}
		})
	}

	cfg := helper.DefaultConfig()
	cfg.ContractVerbosity = helper.ContractVerbosityHigh
	high := prompts.ClaudeMdForConfig(cfg, []string{"go"})
	if start, _, ok := aracIntegrationBounds("# Title\n\n" + high); !ok || start == 0 {
		t.Errorf("the high-verbosity contract is not recognised as aracne's block")
	}
}

// `arac disable` removes the generated block and nothing else -- in particular not a team's own
// `# Aracne` section, which it used to take for the block and strip to the next heading.
func TestDisableLeavesAProjectsOwnAracneSection(t *testing.T) {
	own := "# Mine\n\n# Aracne\n\nOur own notes on using aracne.\n\n# Build\n\nRun make.\n"
	if got := stripAracneIntegrationSegment(own); got != own {
		t.Errorf("disable changed a file holding no generated block:\n--- before\n%s\n--- after\n%s", own, got)
	}
	withBlock := own + "\n" + contractFor(helper.ModeCLI)
	got := stripAracneIntegrationSegment(withBlock)
	if !strings.Contains(got, "Our own notes on using aracne.") {
		t.Errorf("disable took the team's section with it:\n%s", got)
	}
	if strings.Contains(got, "This repository supports Aracne") {
		t.Errorf("disable left the generated block behind:\n%s", got)
	}
}
