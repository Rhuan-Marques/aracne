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

func TestClaudeCLIModelAliases(t *testing.T) {
	for in, want := range map[string]string{
		"": "haiku", "haiku": "haiku", "claude-haiku-4-5": "haiku",
		"sonnet": "sonnet", "claude-sonnet-4-6": "sonnet",
		"opus": "opus", "claude-opus-4-8": "opus",
		"deepseek-v4-flash": "deepseek-v4-flash", // passed through for the CLI to reject
	} {
		if got := claudeCLIModel(in); got != want {
			t.Errorf("claudeCLIModel(%q) = %q, want %q", in, got, want)
		}
	}
}

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
	g := &cliGenerator{bin: bin, model: "haiku"}
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
	g := &cliGenerator{bin: filepath.Join(t.TempDir(), "does-not-exist"), model: "haiku"}
	if _, err := g.Describe(context.Background(), Batch{Resources: []Request{
		{ID: "x", Name: "x", Kind: domain.ResourceKind("function"), Source: "f"},
	}}); err == nil {
		t.Error("expected an error from a missing binary")
	}
}

// An empty batch must not launch a process at all.
func TestCLIGeneratorSkipsAnEmptyBatch(t *testing.T) {
	g := &cliGenerator{bin: filepath.Join(t.TempDir(), "does-not-exist"), model: "haiku"}
	got, err := g.Describe(context.Background(), Batch{})
	if err != nil || got != nil {
		t.Errorf("empty batch: got (%v, %v), want (nil, nil)", got, err)
	}
}

// With no API key and no explicit provider the feature stays OFF, even where the CLI is
// installed: it spends interactive quota, so it must be asked for. (The positive case is
// TestFactoryHonoursAnExplicitCLIProvider.)
func TestFactoryDoesNotSilentlyUseTheCLI(t *testing.T) {
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "DEEPSEEK_API_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("ARACNE_CLAUDE_CLI", fakeCLI(t, "x :: y\n"))
	g, err := GeneratorFactory(helper.ResolvedLazyDescriptions{})
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
	t.Setenv("ARACNE_CLAUDE_CLI", fakeCLI(t, "x :: y\\n"))
	g, err := GeneratorFactory(helper.ResolvedLazyDescriptions{Provider: ProviderClaudeCLI})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if _, ok := g.(*cliGenerator); !ok {
		t.Fatalf("expected *cliGenerator, got %T", g)
	}
}
