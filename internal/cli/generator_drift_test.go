package cli

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// The generators write tool NAMES into files the harness reads as allow-lists: Claude's
// `tools:` frontmatter, OpenCode's `permission:` block, and the `## Tools` prose in every
// agent body. The MCP server registers its own names independently. When the two disagree,
// nothing fails loudly -- the harness simply never routes that tool to the agent.
//
// That is exactly what happened: the generators emitted the catalog name `read` while the
// server registered `read_resource` (the name it takes whenever the harness keeps its own
// read tool), so every generated Claude sub-agent silently lost the aracne read tool.
//
// TestMCPConstructorsMatchToolspecCatalog already pins constructors against the catalog, but
// it compares two sets of CATALOG KEYS and never builds a tool, so it cannot see Tool.Name().
// These tests close that gap by comparing what the generators emit against what a real
// registry actually serves.

// Every test here runs in ModeMCP, and has to: it is the only mode with MCP tools, so it is the
// only one where "the name a generator writes must be a name the server registers" is a claim
// with two sides. In the other three modes both sides are empty and the invariant is vacuous.

// generatedAgents lists the agents `arac init` writes an ALLOW-LIST for, per flag state.
//
// "main" is deliberately absent. It has no agent file, its contract names no tool since the
// mode rework (each tool's own description does that), and the only artifact left that mentions
// tool names for it -- the harness permission block -- is a deliberate SUPERSET: it pre-approves
// every catalog name and both spellings of the read tool, because per-agent servers differ and
// allowing only the resolved name would leave the others prompting. Checking a superset against
// a registry is not this invariant; it is a different, false one.
func generatedAgents(bugManagement bool) []string {
	agents := []string{"descriptions-generation-executor"}
	if bugManagement {
		agents = append(agents, "bug-hunter", "bug-judge", "bug-solver")
	}
	return agents
}

// servedNames returns the runtime names the MCP server registers for one (harness, agent).
func servedNames(t *testing.T, cfg *helper.Config, harness, agentName string) map[string]bool {
	t.Helper()
	mgr := topology.New()
	// Load is a pure setter and no tool constructor touches the file, so a path in a temp
	// dir is enough -- nothing is created.
	if err := mgr.Load(filepath.Join(t.TempDir(), "topology.db")); err != nil {
		t.Fatalf("load manager: %v", err)
	}
	registry := BuildToolRegistry(mgr, NewScannerRegistry(), cfg, harness, serverToolProfile(harness, agentName))
	served := map[string]bool{}
	for _, tool := range registry.List() {
		served[tool.Name()] = true
	}
	return served
}

var (
	claudeToolsLine  = regexp.MustCompile(`(?m)^tools:\s*(.+)$`)
	openCodeAllowKey = regexp.MustCompile(`(?m)^\s*"aracne_([A-Za-z0-9_]+)":\s*allow\s*$`)
	toolsListingLine = regexp.MustCompile(`(?m)^- ` + "`" + `([A-Za-z0-9_]+)` + "`" + ` -- `)
)

// emittedNames extracts every aracne tool name the generated artifact for one agent
// contains. It PARSES THE REAL OUTPUT rather than re-deriving the list, so the test cannot
// pass by making the same mistake twice.
func emittedNames(t *testing.T, cfg *helper.Config, harness, agentName string) map[string]bool {
	t.Helper()
	eff := cfg.EffectiveAgent(harness, agentName)
	names := map[string]bool{}

	add := func(n string) {
		if n != "" {
			names[n] = true
		}
	}

	if harness == "claude_code" {
		content := claudeAgentContent(agentName, "d", eff, "Body paragraph.\n\nRest.")
		if m := claudeToolsLine.FindStringSubmatch(content); m != nil {
			for _, tool := range strings.Split(m[1], ",") {
				tool = strings.TrimSpace(tool)
				if rest, ok := strings.CutPrefix(tool, "mcp__aracne__"); ok {
					add(rest)
				}
			}
		}
		for _, m := range toolsListingLine.FindAllStringSubmatch(content, -1) {
			add(m[1])
		}
		return names
	}

	content := openCodeAgentContent("d", eff, "", "Body paragraph.\n\nRest.")
	for _, m := range openCodeAllowKey.FindAllStringSubmatch(content, -1) {
		add(m[1])
	}
	for _, m := range toolsListingLine.FindAllStringSubmatch(content, -1) {
		add(m[1])
	}
	return names
}

// TestGeneratedToolNamesAreServed is the core invariant: every aracne tool name that lands in
// a generated file is a name the server for that agent actually registers.
func TestGeneratedToolNamesAreServed(t *testing.T) {
	for _, bugManagement := range []bool{false, true} {
		cfg := helper.DefaultConfig()
		cfg.Mode = helper.ModeMCP
		cfg.Mode = helper.ModeMCP
		cfg.Features.BugManagement = bugManagement
		for _, harness := range []string{"claude_code", "opencode"} {
			for _, agentName := range generatedAgents(bugManagement) {
				served := servedNames(t, cfg, harness, agentName)
				emitted := emittedNames(t, cfg, harness, agentName)
				if len(emitted) == 0 {
					t.Fatalf("bug_management=%v %s/%s: parsed no tool names -- the extractor is broken, not the code",
						bugManagement, harness, agentName)
				}
				for name := range emitted {
					if !served[name] {
						t.Errorf("bug_management=%v %s/%s emits %q but its --tool-profile %q serves %v",
							bugManagement, harness, agentName, name,
							serverToolProfile(harness, agentName), keysOf(served))
					}
				}
			}
		}
	}
}

// TestGeneratedReadNameIsExactlyOne pins the converse for the read family. A one-directional
// check would pass on an agent that emitted BOTH names, which is not a valid allow-list.
func TestGeneratedReadNameIsExactlyOne(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	cfg.Features.BugManagement = true
	for _, harness := range []string{"claude_code", "opencode"} {
		for _, agentName := range generatedAgents(true) {
			emitted := emittedNames(t, cfg, harness, agentName)
			if emitted[toolspec.ReadToolName] && emitted[toolspec.ReadResourceToolName] {
				t.Errorf("%s/%s emits both read names; an allow-list must name the one the server registers",
					harness, agentName)
			}
		}
	}
}

// TestHarnessesDisagreeOnTheReadName records that the inversion between the two harnesses is
// CORRECT, not an oversight.
//
// Claude Code gives each agent its own server and leaves the native Read in place, so aracne's
// read registers as `read_resource`. OpenCode serves every agent from one --tool-profile all
// server, and the "all" profile has no native read to collide with, so it stays `read`. A
// future "cleanup" that unified the two on a single bool would silently re-break one of them.
func TestHarnessesDisagreeOnTheReadName(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	claude := servedNames(t, cfg, "claude_code", "main")
	opencode := servedNames(t, cfg, "opencode", "main")

	if !claude[toolspec.ReadResourceToolName] {
		t.Errorf("Claude Code main agent should serve %q, got %v", toolspec.ReadResourceToolName, keysOf(claude))
	}
	if !opencode[toolspec.ReadToolName] {
		t.Errorf("OpenCode main agent should serve %q, got %v", toolspec.ReadToolName, keysOf(opencode))
	}
}

// servesAnyBugTool reports whether a profile registers any bug_* tool.
func servesAnyBugTool(t *testing.T, cfg *helper.Config, harness, profile string) bool {
	t.Helper()
	for name := range servedNames(t, cfg, harness, profile) {
		if toolspec.IsBugTool(name) {
			return true
		}
	}
	return false
}

// TestBugToolsFollowTheFeatureFlag pins that the gate reaches the MCP surface, including
// OpenCode's shared "all" profile -- the path a hand-written mcp_tools entry would otherwise
// use to sneak a bug tool through with the feature off.
func TestBugToolsFollowTheFeatureFlag(t *testing.T) {
	for _, bugManagement := range []bool{false, true} {
		cfg := helper.DefaultConfig()
		cfg.Mode = helper.ModeMCP
		cfg.Mode = helper.ModeMCP
		cfg.Features.BugManagement = bugManagement
		for _, harness := range []string{"claude_code", "opencode"} {
			if got := servesAnyBugTool(t, cfg, harness, "all"); got != bugManagement {
				t.Errorf("bug_management=%v %s profile all: bug tools served = %v, want %v",
					bugManagement, harness, got, bugManagement)
			}
		}
	}
}

// TestMainProfileNeverServesBugTools pins the orchestration decision: the main agent gets the
// bug list by running `arac bug list --json` in the shell, NOT from an MCP tool -- in either
// flag state.
//
// The reason is context cost. A tool schema is unconditional: it rides on every request in the
// project whether or not the workflow is ever run. A CLI invocation is summoned by the slash
// command's own text, so it costs nothing until the moment it is relevant. That is why
// enabling the feature must NOT quietly hand the main agent bug_list.
func TestMainProfileNeverServesBugTools(t *testing.T) {
	for _, bugManagement := range []bool{false, true} {
		cfg := helper.DefaultConfig()
		cfg.Mode = helper.ModeMCP
		cfg.Mode = helper.ModeMCP
		cfg.Features.BugManagement = bugManagement
		for _, harness := range []string{"claude_code", "opencode"} {
			// OpenCode's main agent is served by the shared "all" profile, so its restriction
			// is the permission block, not the registry -- assert on what init emits there.
			if harness == "opencode" {
				perms := openCodePermissionsForAgent(cfg.EffectiveAgent(harness, "main"))
				for _, m := range openCodeAllowKey.FindAllStringSubmatch(perms, -1) {
					if toolspec.IsBugTool(m[1]) {
						t.Errorf("bug_management=%v: OpenCode main agent must not be granted %q", bugManagement, m[1])
					}
				}
				continue
			}
			if servesAnyBugTool(t, cfg, harness, "main") {
				t.Errorf("bug_management=%v: %s main profile must not serve bug tools", bugManagement, harness)
			}
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
