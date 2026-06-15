package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAgentKindMarkdown_Valid(t *testing.T) {
	content := `---
name: bug-hunter
description: Find bugs in the codebase
tools: read, grep, bug_report
---
You are a bug hunter. Inspect resources for bugs.`

	kind, err := parseAgentKindMarkdown(content, "test.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind.Name != "bug-hunter" {
		t.Errorf("Name = %q, want %q", kind.Name, "bug-hunter")
	}
	if kind.Description != "Find bugs in the codebase" {
		t.Errorf("Description = %q, want %q", kind.Description, "Find bugs in the codebase")
	}
	if len(kind.Tools) != 3 || kind.Tools[0] != "read" || kind.Tools[1] != "grep" || kind.Tools[2] != "bug_report" {
		t.Errorf("Tools = %v, want [read grep bug_report]", kind.Tools)
	}
	if !strings.Contains(kind.SystemPrompt, "bug hunter") {
		t.Errorf("SystemPrompt missing expected text: %q", kind.SystemPrompt)
	}
}

func TestParseAgentKindMarkdown_UnderscoreName(t *testing.T) {
	content := `---
name: "bug_hunter"
description: Find bugs
tools: read
---
Prompt body.`

	kind, err := parseAgentKindMarkdown(content, "test.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind.Name != "bug-hunter" {
		t.Errorf("Name = %q, want %q", kind.Name, "bug-hunter")
	}
}

func TestParseAgentKindMarkdown_BOMAndCRLF(t *testing.T) {
	content := "\ufeff---\r\nname: custom-agent\r\ndescription: My custom agent\r\ntools: read\r\n---\r\nSystem prompt here.\r\n"

	kind, err := parseAgentKindMarkdown(content, "test.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind.Name != "custom-agent" {
		t.Errorf("Name = %q, want %q", kind.Name, "custom-agent")
	}
}

func TestParseAgentKindMarkdown_MissingFrontmatter(t *testing.T) {
	_, err := parseAgentKindMarkdown("no frontmatter", "test.md")
	if err == nil || !strings.Contains(err.Error(), "missing frontmatter") {
		t.Errorf("expected missing frontmatter error, got: %v", err)
	}
}

func TestParseAgentKindMarkdown_Unterminated(t *testing.T) {
	content := "---\nname: test"
	_, err := parseAgentKindMarkdown(content, "test.md")
	if err == nil || !strings.Contains(err.Error(), "unterminated") {
		t.Errorf("expected unterminated error, got: %v", err)
	}
}

func TestParseAgentKindMarkdown_MissingName(t *testing.T) {
	content := "---\ndescription: desc\ntools: read\n---\nbody"
	_, err := parseAgentKindMarkdown(content, "test.md")
	if err == nil || !strings.Contains(err.Error(), "missing name") {
		t.Errorf("expected missing name error, got: %v", err)
	}
}

func TestParseAgentKindMarkdown_MissingDescription(t *testing.T) {
	content := "---\nname: test\ntools: read\n---\nbody"
	_, err := parseAgentKindMarkdown(content, "test.md")
	if err == nil || !strings.Contains(err.Error(), "missing description") {
		t.Errorf("expected missing description error, got: %v", err)
	}
}

func TestParseAgentKindMarkdown_MissingTools(t *testing.T) {
	content := "---\nname: test\ndescription: desc\n---\nbody"
	_, err := parseAgentKindMarkdown(content, "test.md")
	if err == nil || !strings.Contains(err.Error(), "missing tools") {
		t.Errorf("expected missing tools error, got: %v", err)
	}
}

func TestParseAgentKindMarkdown_MissingBody(t *testing.T) {
	content := "---\nname: test\ndescription: desc\ntools: read\n---"
	_, err := parseAgentKindMarkdown(content, "test.md")
	if err == nil || !strings.Contains(err.Error(), "missing system prompt") {
		t.Errorf("expected missing system prompt error, got: %v", err)
	}
}

func TestNormalizeAgentKindName(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"bug-hunter", "bug-hunter"},
		{"bug_hunter", "bug-hunter"},
		{"  bug-hunter  ", "bug-hunter"},
		{`"bug-hunter"`, "bug-hunter"},
		{"'bug-hunter'", "bug-hunter"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeAgentKindName(c.input); got != c.want {
			t.Errorf("normalizeAgentKindName(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestDisplayAgentKindName(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"bug-hunter", "Bug Hunter"},
		{"descriptions-generation-executor", "Descriptions Generation Executor"},
		{"bug-judge", "Bug Judge"},
		{"bug-solver", "Bug Solver"},
		{"", ""},
	}
	for _, c := range cases {
		if got := displayAgentKindName(c.input); got != c.want {
			t.Errorf("displayAgentKindName(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestAgentKindIcon(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"descriptions-generation-executor", "file-text"},
		{"bug-hunter", "bug"},
		{"bug-judge", "scale"},
		{"bug-solver", "wrench"},
		{"unknown", "bot"},
	}
	for _, c := range cases {
		if got := agentKindIcon(c.name); got != c.want {
			t.Errorf("agentKindIcon(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAgentProfilesFromKinds(t *testing.T) {
	kinds := []AgentKind{
		{Name: "bug-hunter", Description: "Hunt bugs", Tools: []string{"read"}},
		{Name: "bug-solver", Description: "Fix bugs", Tools: []string{"read", "edit"}},
	}
	profiles := agentProfilesFromKinds(kinds)
	if len(profiles) != 2 {
		t.Fatalf("got %d profiles, want 2", len(profiles))
	}
	if profiles[0].ID != "bug-hunter" || profiles[0].Name != "Bug Hunter" || profiles[0].Icon != "bug" {
		t.Errorf("profile[0] mismatch: %+v", profiles[0])
	}
	if profiles[1].ID != "bug-solver" || profiles[1].Name != "Bug Solver" || profiles[1].Icon != "wrench" {
		t.Errorf("profile[1] mismatch: %+v", profiles[1])
	}
}

func TestEnsureDefaultAgentFiles(t *testing.T) {
	dir := t.TempDir()
	if err := ensureDefaultAgentFiles(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 files, got %d", len(entries))
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	for _, name := range []string{"explorer.md", "descriptions-generation-executor.md", "bug-hunter.md", "bug-judge.md", "bug-solver.md"} {
		if !names[name] {
			t.Errorf("missing file: %s", name)
		}
	}
	for _, name := range []string{"explorer.md", "bug-hunter.md", "bug-judge.md", "bug-solver.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			t.Errorf("file %s is empty", name)
		}
	}
}

func TestEnsureDefaultAgentFiles_SkipsExisting(t *testing.T) {
	dir := t.TempDir()
	customPath := filepath.Join(dir, "bug-hunter.md")
	os.WriteFile(customPath, []byte("custom content"), 0644)

	if err := ensureDefaultAgentFiles(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(customPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "custom content" {
		t.Errorf("existing file was modified: got %q", string(data))
	}
}

func TestLoadAgentKinds_Valid(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test-agent.md"), []byte(`---
name: test-agent
description: A test agent
tools: read, grep
---
You are a test agent.
`), 0644)

	kinds, err := loadAgentKinds(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, k := range kinds {
		if k.Name == "test-agent" {
			found = true
			if k.Description != "A test agent" {
				t.Errorf("description = %q", k.Description)
			}
			if len(k.Tools) != 2 || k.Tools[0] != "read" {
				t.Errorf("tools = %v", k.Tools)
			}
			if !strings.Contains(k.SystemPrompt, "test agent") {
				t.Errorf("system prompt = %q", k.SystemPrompt)
			}
			break
		}
	}
	if !found {
		t.Error("test-agent not found in loaded kinds")
	}
}

func TestLoadAgentKinds_DuplicateName(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a1.md"), []byte("---\nname: same\ndescription: first\ntools: read\n---\nbody"), 0644)
	os.WriteFile(filepath.Join(dir, "a2.md"), []byte("---\nname: same\ndescription: second\ntools: grep\n---\nbody"), 0644)
	_, err := loadAgentKinds(dir)
	if err == nil || !strings.Contains(err.Error(), "duplicate agent kind") {
		t.Errorf("expected duplicate error, got: %v", err)
	}
}

func TestResolveAgentKind_Found(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "finder.md"), []byte("---\nname: find-me\ndescription: Findable agent\ntools: read\n---\nFound me!"), 0644)
	kind, err := resolveAgentKind(dir, "find-me")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind.Name != "find-me" {
		t.Errorf("Name = %q", kind.Name)
	}
}

func TestResolveAgentKind_NotFound(t *testing.T) {
	_, err := resolveAgentKind(t.TempDir(), "non-existent")
	if err == nil {
		t.Error("expected error for non-existent agent")
	}
}

func TestAgentKindPrompt_WithKinds(t *testing.T) {
	prompt := agentKindPrompt(t.TempDir())
	if prompt == "" {
		t.Fatal("expected non-empty prompt when default agents exist")
	}
	if !strings.Contains(prompt, "Available CreateTasks Agent Kinds") {
		t.Error("prompt should list CreateTasks kinds")
	}
	if !strings.Contains(prompt, "explorer") {
		t.Error("prompt should list explorer")
	}
	if strings.Contains(prompt, "bug-hunter") {
		t.Error("prompt should not list workflow-only agents")
	}
	if !strings.Contains(prompt, "CreateTasks") {
		t.Error("prompt should mention CreateTasks")
	}
}

func TestAgentKindPrompt_NonExistentDir(t *testing.T) {
	prompt := agentKindPrompt(filepath.Join(t.TempDir(), "nonexistent"))
	if prompt != "" {
		t.Logf("got non-empty prompt (defaults created): %s", prompt)
	}
}
