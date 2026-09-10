package cli

import (
	"reflect"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// OpenCode's permission block and the Claude Code guard must refuse the SAME things in every
// mode. They used to diverge on one: `blocked_tools: ["grep"]` in ModeCLI, where the guard drops
// grep (search is answered by interception in every mode) and OpenCode denied `grep *` anyway.
func TestOpenCodePermissionsBlockWhatTheGuardBlocks(t *testing.T) {
	for _, mode := range []string{helper.ModeMCP, helper.ModeCLI, helper.ModeInterceptID, helper.ModeInterceptLineRanges} {
		for _, tool := range []string{"read", "grep", "edit", "write", "bash"} {
			cfg := helper.DefaultConfig()
			cfg.Mode = mode
			cfg.LLM.OpenCode.MainAgent.BlockedTools = []string{tool}
			cfg.LLM.ClaudeCode.MainAgent.BlockedTools = []string{tool}

			guard := cfg.BlockableInMode(toolNameSet(cfg.EffectiveAgent("claude_code", "main").BlockedTools))
			opencode := openCodeMainBlocked(cfg)
			if !reflect.DeepEqual(guard, opencode) {
				t.Errorf("mode %s, blocked %s: guard refuses %v, OpenCode refuses %v", mode, tool, guard, opencode)
			}
		}
	}

	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeCLI
	cfg.LLM.OpenCode.MainAgent.BlockedTools = []string{"grep"}
	if got := openCodeBashPermission(openCodeMainBlocked(cfg)); got != "allow" {
		t.Errorf("ModeCLI answers grep by interception; OpenCode's bash permission must not refuse it, got %v", got)
	}
	cfg.Mode = helper.ModeMCP
	rules, ok := openCodeBashPermission(openCodeMainBlocked(cfg)).(map[string]interface{})
	if !ok || rules["grep *"] != "deny" {
		t.Errorf("ModeMCP does block grep; the deny pattern must still be written, got %v", rules)
	}
}

// What initOpenCode actually WRITES follows the guard's rules: in ModeCLI `read` is blockable
// and `grep` is not, so the bash permission denies `cat *` and never `grep *`.
func TestInitOpenCodeWritesTheGuardsPolicy(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeCLI
	cfg.LLM.OpenCode.MainAgent.BlockedTools = []string{"grep", "read"}
	initOpenCode(false, cfg, true, nil)

	perm, _ := readJSONConfig(".opencode/opencode.json")["permission"].(map[string]interface{})
	bash, ok := perm["bash"].(map[string]interface{})
	if !ok {
		t.Fatalf("read is blocked in ModeCLI, so bash must carry deny patterns, got %v", perm["bash"])
	}
	if bash["cat *"] != "deny" {
		t.Errorf("a blocked read must deny `cat *`: %v", bash)
	}
	if _, denied := bash["grep *"]; denied {
		t.Errorf("ModeCLI answers grep by interception; OpenCode must not deny `grep *`: %v", bash)
	}
}
