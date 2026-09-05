package lazydesc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// fakeCLI writes a stub executable that echoes a canned batch reply, so the transport is
// exercised end to end without spending a real completion.
func fakeCLI(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claude")
	script := "#!/bin/sh\ncat > /dev/null\nprintf '%s'\n"
	if err := os.WriteFile(path, []byte(strings.Replace(script, "%s", body, 1)), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCLIGeneratorParsesABatch(t *testing.T) {
	bin := fakeCLI(t, "pkg.Alpha :: Normalizes a tag.\\npkg.Beta :: Registers a tag.\\n")
	g := &cliGenerator{argv: []string{bin}}
	got, err := g.Describe(context.Background(), Batch{Resources: []Request{
		{ID: "pkg.Alpha", Name: "Alpha", Kind: domain.ResourceKind("function"), Source: "func Alpha(){}"},
		{ID: "pkg.Beta", Name: "Beta", Kind: domain.ResourceKind("function"), Source: "func Beta(){}"},
	}})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no descriptions parsed from the CLI reply")
	}
	if d := got["pkg.Alpha"]; !strings.Contains(d, "Normalizes") {
		t.Errorf("pkg.Alpha = %q", d)
	}
}

func TestCLIGeneratorReportsAFailingBinary(t *testing.T) {
	g := &cliGenerator{argv: []string{filepath.Join(t.TempDir(), "does-not-exist")}}
	if _, err := g.Describe(context.Background(), Batch{Resources: []Request{
		{ID: "x", Name: "x", Kind: domain.ResourceKind("function"), Source: "f"},
	}}); err == nil {
		t.Error("expected an error from a missing binary")
	}
}

// An empty batch must not launch a process at all.
func TestCLIGeneratorSkipsAnEmptyBatch(t *testing.T) {
	g := &cliGenerator{argv: []string{filepath.Join(t.TempDir(), "does-not-exist")}}
	got, err := g.Describe(context.Background(), Batch{})
	if err != nil || got != nil {
		t.Errorf("empty batch: got (%v, %v), want (nil, nil)", got, err)
	}
}

// With no API key and no explicit provider the feature stays OFF, even where a CLI is
// installed: it spends interactive quota, so it must be asked for. (The positive case is
// TestFactoryHonoursAnExplicitCLIProvider.)
func TestFactoryDoesNotSilentlyUseTheCLI(t *testing.T) {
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "DEEPSEEK_API_KEY"} {
		t.Setenv(k, "")
	}
	g, err := GeneratorFactory(helper.ResolvedLazyDescriptions{CLICommand: []string{fakeCLI(t, "x :: y\n")}})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if g != nil {
		t.Fatalf("expected the switched-off no-op, got %T", g)
	}
}

// An explicit provider name must not be overridden by a key that happens to be present.
func TestFactoryHonoursAnExplicitCLIProvider(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-not-real")
	g, err := GeneratorFactory(helper.ResolvedLazyDescriptions{
		Provider:   ProviderCLI,
		CLICommand: []string{fakeCLI(t, "x :: y\\n")},
	})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if _, ok := g.(*cliGenerator); !ok {
		t.Fatalf("expected *cliGenerator, got %T", g)
	}
}

// The generic transport: any command that answers a prompt on stdout writes descriptions.
// The stub echoes a canned reply, so what is being pinned is the plumbing -- argv from the
// config, prompt on stdin, reply parsed by the same parser the API path uses.
func TestGenericCLIProviderRunsTheConfiguredCommand(t *testing.T) {
	bin := fakeCLI(t, "pkg.Alpha :: Normalizes a tag.\\n")
	gen, err := GeneratorFactory(helper.ResolvedLazyDescriptions{
		Provider:   ProviderCLI,
		CLICommand: []string{bin, "-p"},
	})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if gen == nil {
		t.Fatal("a configured cli provider must build a generator")
	}
	got, err := gen.Describe(context.Background(), Batch{Resources: []Request{
		{ID: "pkg.Alpha", Name: "Alpha", Kind: domain.ResourceKind("function"), Source: "func Alpha(){}"},
	}})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if d := got["pkg.Alpha"]; !strings.Contains(d, "Normalizes") {
		t.Errorf("pkg.Alpha = %q", d)
	}
}

// Without a command there is nothing to run, and the fill must decline rather than launch a
// truncated argv. (Validation catches this at init; this is the second line.)
func TestGenericCLIProviderWithoutACommand(t *testing.T) {
	if _, err := NewCLIGenerator(helper.ResolvedLazyDescriptions{Provider: ProviderCLI}); err == nil {
		t.Fatal("a cli provider with no command must be an error")
	}
	gen, err := GeneratorFactory(helper.ResolvedLazyDescriptions{Provider: ProviderCLI})
	if err != nil || gen != nil {
		t.Fatalf("the lazy path must decline silently, got (%v, %v)", gen, err)
	}
}

// The generic transport delivers the system prompt on stdin, because no flag is common to
// every CLI. A command that ignores stdin would describe nothing, so the prompt has to be
// there -- this stub proves it arrives by echoing what it was given.
func TestGenericCLIProviderSendsThePromptOnStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a shell script")
	}
	dir := t.TempDir()
	seen := filepath.Join(dir, "stdin.txt")
	bin := filepath.Join(dir, "echo-stdin")
	script := "#!/bin/sh\ncat > " + seen + "\nprintf 'pkg.Alpha :: Normalizes a tag.\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	gen, err := NewCLIGenerator(helper.ResolvedLazyDescriptions{
		Provider: ProviderCLI, CLICommand: []string{bin},
	})
	if err != nil {
		t.Fatalf("NewCLIGenerator: %v", err)
	}
	if _, err := gen.Describe(context.Background(), Batch{Resources: []Request{
		{ID: "pkg.Alpha", Name: "Alpha", Kind: domain.ResourceKind("function"), Source: "func Alpha(){}"},
	}}); err != nil {
		t.Fatalf("Describe: %v", err)
	}
	body, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the command received no stdin: %v", err)
	}
	for _, want := range []string{"One line per assigned resource", "pkg.Alpha", "func Alpha(){}"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("stdin is missing %q", want)
		}
	}
}

// A CLI provider must never fall through to an API key the project did not name, on either
// entry point: `arac descriptions generate` asked for `claude -p`, and holding a key is not a
// reason to bill a vendor instead.
func TestCLIProviderDoesNotFallBackToAnAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-not-real")
	cfg := helper.ResolvedLazyDescriptions{Provider: ProviderCLI, CLICommand: []string{"claude", "-p"}}
	if !IsCLIProvider(cfg) {
		t.Fatalf("%q should be a CLI provider", ProviderCLI)
	}
	if _, _, ok := ResolveDescriptionProvider(cfg); ok {
		t.Error("a cli provider must not resolve to an API provider")
	}
}

// The retired name is recognised only to be rejected, and the rejection has to carry the fix:
// a project that wrote `claude_cli` was told to, and "unknown provider" would leave it
// guessing at a command.
func TestRetiredClaudeCLIProviderNamesItsReplacement(t *testing.T) {
	if IsCLIProvider(helper.ResolvedLazyDescriptions{Provider: "claude_cli"}) {
		t.Error("claude_cli must no longer dispatch to the CLI transport")
	}
	err := helper.ValidateDescriptionProvider(helper.DescriptionsSection{Provider: "claude_cli"})
	if err == nil {
		t.Fatal("claude_cli must be rejected")
	}
	for _, want := range []string{"cli_provider_command", helper.ClaudeCLIReplacementCommand} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the rejection should name %q, got %v", want, err)
		}
	}
}
