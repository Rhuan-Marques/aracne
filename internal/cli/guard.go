package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/toolspec"
)

// RunGuard is the `arac guard` entry point. It only supports the Claude Code
// PreToolUse/PostToolUse hook invocation (`--claude-hook`), which reads the
// hook event JSON on stdin and writes a decision on stdout.
func RunGuard(args []string) {
	if len(args) == 1 && args[0] == "--claude-hook" {
		runClaudeGuardHook(os.Stdin, os.Stdout)
		return
	}
	fmt.Fprintln(os.Stderr, "Usage: arac guard --claude-hook")
	os.Exit(1)
}

// runClaudeGuardHook reads a Claude Code hook event and either blocks the call
// (PreToolUse, when an implicated tool is in blocked_tools) or warns the model
// to use the aracne MCP equivalent (PostToolUse, always). Any read/parse error
// fails open: nothing is emitted, so the tool call proceeds and the session is
// never broken.
//
// The guard runs in the main Claude Code session, so it consults the
// claude_code "main" agent's blocked_tools. Per-sub-agent blocked_tools are
// not reachable here (the hook payload carries no sub-agent identity); those
// are enforced by each sub-agent's frontmatter `tools:` allow-list at init.
func runClaudeGuardHook(input io.Reader, output io.Writer) {
	raw, err := io.ReadAll(input)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return
	}
	var event struct {
		HookEventName string                 `json:"hook_event_name"`
		ToolName      string                 `json:"tool_name"`
		ToolInput     map[string]interface{} `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || event.ToolName == "" {
		return
	}

	switch event.HookEventName {
	case "PreToolUse":
		if d := decideGuard(event.ToolName, event.ToolInput, loadGuardBlockedSet()); d.Deny {
			emitPreToolDeny(output, d.Message)
		}
	case "PostToolUse":
		if msg := warningMessage(implicatedKeys(event.ToolName, event.ToolInput)); msg != "" {
			emitPostToolWarning(output, msg)
		}
	}
}

// guardDecision is the result of evaluating a tool call against blocked_tools.
type guardDecision struct {
	Deny    bool
	Message string // deny reason shown to the model (empty when not denied)
}

// decideGuard reports whether a tool call must be blocked and the reason.
func decideGuard(toolName string, toolInput map[string]interface{}, blocked map[string]bool) guardDecision {
	keys := implicatedKeys(toolName, toolInput)
	var denied []string
	for _, k := range keys {
		if blocked[k] {
			denied = append(denied, k)
		}
	}
	if len(denied) == 0 {
		return guardDecision{}
	}
	reason := fmt.Sprintf("Blocked by aracne config (blocked_tools: %s). ", strings.Join(denied, ", "))
	if warn := warningMessage(keys); warn != "" {
		reason += warn
	} else {
		reason += "Use the aracne MCP tools instead of this native/shell command."
	}
	return guardDecision{Deny: true, Message: strings.TrimSpace(reason)}
}

// implicatedKeys returns the aracne tool keys a tool call stands in for. For
// the Bash tool it parses the shell command for stand-in commands and always
// includes "bash" (so a whole-Bash block via blocked_tools:["bash"] applies).
func implicatedKeys(toolName string, toolInput map[string]interface{}) []string {
	if toolName == "Bash" {
		cmd, _ := toolInput["command"].(string)
		return append(commandKeys(cmd), "bash")
	}
	if key, ok := toolspec.NativeToolKey(toolName); ok {
		return []string{key}
	}
	return nil
}

// warningMessage joins the per-key guidance for the given keys, skipping keys
// without an MCP equivalent (e.g. "bash") and de-duplicating.
func warningMessage(keys []string) string {
	var parts []string
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		if w := toolspec.WarningFor(k); w != "" {
			parts = append(parts, w)
		}
	}
	return strings.Join(parts, " ")
}

// loadGuardBlockedSet resolves the claude_code main agent's blocked_tools. A
// missing/unparseable config fails open (empty set → warn-only, never block).
func loadGuardBlockedSet() map[string]bool {
	cfg, ok := helper.LoadConfigStrict(helper.ConfigPath(".aracne/topology.db"))
	if !ok {
		return map[string]bool{}
	}
	return toolNameSet(cfg.EffectiveAgent("claude_code", "main").BlockedTools)
}

// ---------------------------------------------------------------------------
// Shell command parsing
// ---------------------------------------------------------------------------

// commandKeys returns the unique aracne tool keys implied by the command names
// invoked in a shell string. It is a pragmatic tokenizer, not a real shell
// parser: it splits on the common separators, then classifies only the command
// WORD of each segment. This makes `git grep x` safe (the word is `git`, not
// `grep`) and ignores command names that appear only inside quoted arguments.
// Residual false positives (heredocs, aliases, complex subshells) are
// accepted; the worst case is an extra warning.
func commandKeys(command string) []string {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	var keys []string
	seen := make(map[string]bool)
	for _, seg := range splitCommandSegments(command) {
		word := commandWord(seg)
		if word == "" {
			continue
		}
		if key, ok := toolspec.ShellCommandKey(word); ok && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

// splitCommandSegments splits a shell command into simple-command candidates on
// unquoted separators (pipes, list operators, subshell/group delimiters,
// backticks, newlines). Quote characters are dropped; their inner text is kept
// so a quoted pipe never causes a split.
func splitCommandSegments(command string) []string {
	var segs []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		segs = append(segs, cur.String())
		cur.Reset()
	}
	for _, r := range command {
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '|', ';', '&', '\n', '(', ')', '`', '{', '}':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return segs
}

// commandWord returns the base command name of a single segment, skipping
// leading environment assignments (FOO=bar) and wrapper commands
// (sudo/env/...). Returns "" when the segment has no command word.
func commandWord(segment string) string {
	fields := strings.Fields(segment)
	i := 0
	for i < len(fields) && (isEnvAssignment(fields[i]) || isCommandWrapper(fields[i])) {
		i++
	}
	if i >= len(fields) {
		return ""
	}
	return baseName(fields[i])
}

func isEnvAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for j, r := range tok[:eq] {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case j > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func isCommandWrapper(tok string) bool {
	switch strings.ToLower(baseName(tok)) {
	case "env", "sudo", "doas", "command", "nohup", "time", "exec", "builtin":
		return true
	}
	return false
}

// baseName strips a leading escape backslash, any directory prefix, and a
// trailing .exe so that /usr/bin/grep, \grep and grep.exe all resolve to grep.
func baseName(tok string) string {
	tok = strings.TrimPrefix(tok, "\\")
	if i := strings.LastIndexAny(tok, "/\\"); i >= 0 {
		tok = tok[i+1:]
	}
	return strings.TrimSuffix(tok, ".exe")
}

// ---------------------------------------------------------------------------
// Decision emission (the single place that knows the hook wire format)
// ---------------------------------------------------------------------------

func emitPreToolDeny(output io.Writer, reason string) {
	json.NewEncoder(output).Encode(map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	})
}

func emitPostToolWarning(output io.Writer, msg string) {
	json.NewEncoder(output).Encode(map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":     "PostToolUse",
			"additionalContext": msg,
		},
	})
}
