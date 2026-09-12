package cli

import (
	"os"
	"strings"
	"testing"
)

// OpenCode turns a permission object into rules in the order its keys are written and resolves
// them with findLast, so KEY ORDER IS SEMANTIC: `{"git*":"allow","git push":"deny"}` denies
// `git push`, and the same two keys the other way round allow it. `arac setup` decoded the file
// into a Go map and re-encoded it with encoding/json, which sorts -- so merging aracne's server
// entry into an operator's config silently turned their deny into an allow.
func TestWriteJSONConfigPreservesKeyOrder(t *testing.T) {
	inProject(t)
	const original = `{
  "permission": {
    "bash": {
      "git*": "allow",
      "git push": "deny"
    }
  }
}
`
	writeOpenCodeConfig(t, original)

	config := readJSONConfig(".opencode/opencode.json")
	upsertMCPServer(config, "mcp", map[string]interface{}{"type": "local", "enabled": true})
	writeJSONConfig(".opencode/opencode.json", config)

	data, err := os.ReadFile(".opencode/opencode.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Index(text, `"git*"`) > strings.Index(text, `"git push"`) {
		t.Fatalf("setup re-sorted the bash rules; `git push` now matches the later `git*` allow:\n%s", text)
	}
	if !strings.Contains(text, `"aracne"`) {
		t.Fatalf("the merge itself was lost:\n%s", text)
	}
}

// The whole point of preserving the bytes: a rule the operator wrote to deny something still
// denies it after `arac setup` has merged into the file.
func TestSetupPreservesOpenCodeRuleOrder(t *testing.T) {
	inProject(t)
	writeOpenCodeConfig(t, `{"permission":{"bash":{"git*":"allow","git push":"deny"}}}`)
	if err := os.MkdirAll(".aracne", 0755); err != nil {
		t.Fatal(err)
	}

	runSetup(false, true, false, true)

	data, err := os.ReadFile(".opencode/opencode.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Index(text, `"git*"`) > strings.Index(text, `"git push"`) {
		t.Fatalf("`arac setup` reordered the operator's bash rules:\n%s", text)
	}
}

// OpenCode parses opencode.json as JSONC, so a commented file is a VALID file there -- and
// `arac setup` exited 1 on it, taking the Claude Code half of the run down with it (initOpenCode
// runs first). The comment is the operator's and has to survive the merge.
func TestSetupAcceptsCommentedOpenCodeConfig(t *testing.T) {
	inProject(t)
	writeOpenCodeConfig(t, `{
  // aracne must not choke on this
  "permission": {
    "bash": "ask", // nor on this
  }
}
`)
	if err := os.MkdirAll(".aracne", 0755); err != nil {
		t.Fatal(err)
	}

	runSetup(true, true, false, true)

	data, err := os.ReadFile(".opencode/opencode.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "aracne must not choke on this") {
		t.Fatalf("the operator's comment was dropped:\n%s", text)
	}
	if !strings.Contains(text, `"aracne"`) {
		t.Fatalf("no MCP server was merged in:\n%s", text)
	}
	// initOpenCode runs before initClaudeCode, so the exit took the Claude half with it.
	if _, err := os.Stat(".mcp.json"); err != nil {
		if _, mdErr := os.Stat("CLAUDE.md"); mdErr != nil {
			t.Fatalf("the Claude Code integration was never written: %v / %v", err, mdErr)
		}
	}
}

// A JSONC file is read for its VALUES too: comments and trailing commas must not change what
// aracne decodes, or `arac disable` would fail to find the entries it has to remove.
func TestParseJSONConfigToleratesJSONC(t *testing.T) {
	config, err := parseJSONConfig([]byte("{\n  /* block */\n  \"mcp\": {\n    \"aracne\": {\"type\": \"local\"}, // trailing\n  },\n}\n"))
	if err != nil {
		t.Fatalf("parseJSONConfig: %v", err)
	}
	servers, _ := config["mcp"].(map[string]interface{})
	if _, ok := servers["aracne"]; !ok {
		t.Fatalf("aracne server not decoded from JSONC: %v", config)
	}
	if _, err := parseJSONConfig([]byte("{not json")); err == nil {
		t.Fatal("a genuinely broken file must still be an error")
	}
}
