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

	blocked, exemptPiped := loadGuardConfig()
	switch event.HookEventName {
	case "PreToolUse":
		if d := decideGuard(event.ToolName, event.ToolInput, blocked, exemptPiped); d.Deny {
			emitPreToolDeny(output, d.Message)
		}
	case "PostToolUse":
		keys := implicatedKeys(event.ToolName, event.ToolInput, exemptPiped)
		if msg := warningMessage(keys, !blocked[toolspec.ReadToolName]); msg != "" {
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
func decideGuard(toolName string, toolInput map[string]interface{}, blocked map[string]bool, exemptPiped bool) guardDecision {
	keys := implicatedKeys(toolName, toolInput, exemptPiped)
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
	if warn := warningMessage(keys, !blocked[toolspec.ReadToolName]); warn != "" {
		reason += warn
	} else {
		reason += "Use the aracne MCP tools instead of this native/shell command."
	}
	return guardDecision{Deny: true, Message: strings.TrimSpace(reason)}
}

// implicatedKeys returns the aracne tool keys a tool call stands in for. For
// the Bash tool it parses the shell command for stand-in commands and always
// includes "bash" (so a whole-Bash block via blocked_tools:["bash"] applies).
func implicatedKeys(toolName string, toolInput map[string]interface{}, exemptPiped bool) []string {
	if toolName == "Bash" {
		cmd, _ := toolInput["command"].(string)
		return append(commandKeys(cmd, exemptPiped), "bash")
	}
	if key, ok := toolspec.NativeToolKey(toolName); ok {
		return []string{key}
	}
	return nil
}

// warningMessage joins the per-key guidance for the given keys, skipping keys
// without an MCP equivalent (e.g. "bash") and de-duplicating. nativeReadAvailable
// resolves the read tool's runtime name so the guidance never points at a name
// that is absent from this agent's tool list.
func warningMessage(keys []string, nativeReadAvailable bool) string {
	var parts []string
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		if w := toolspec.WarningFor(k, nativeReadAvailable); w != "" {
			parts = append(parts, w)
		}
	}
	return strings.Join(parts, " ")
}

// loadGuardConfig resolves the guard inputs from the claude_code main agent
// config: the blocked_tools set and whether piped read/grep commands are exempt
// (read.pipe_passthrough). A missing/unparseable config fails open: no blocks
// and the exemption on, so the session is never broken.
func loadGuardConfig() (blocked map[string]bool, exemptPiped bool) {
	cfg, ok := helper.LoadConfigStrict(helper.ConfigPath(".aracne/topology.db"))
	if !ok {
		return map[string]bool{}, true
	}
	return toolNameSet(cfg.EffectiveAgent("claude_code", "main").BlockedTools), cfg.EffectivePipePassthrough()
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
// When exemptPiped is set, a read/grep command that consumes piped stdin
// (`cmd | tail`, `cmd | grep x`) is skipped: it views/filters command output,
// which the MCP file/grep tools cannot serve. Direct file reads (`cat foo.go`)
// are still classified.
//
// sed/awk are classified by what they DO, not by their name: they map to `edit`
// only when they write in place (-i) or redirect output to a file, and count as
// reads otherwise. That is what makes `git log | sed -n '30,60p'` exempt under
// the ordinary read rule instead of being refused as an edit.
func commandKeys(command string, exemptPiped bool) []string {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	var keys []string
	seen := make(map[string]bool)
	for _, seg := range splitCommandSegments(command) {
		fields := commandFields(seg.text)
		if len(fields) == 0 {
			continue
		}
		word := fields[0]
		key, ok := toolspec.ShellCommandKeyForArgs(word, fields[1:], seg.redirectsOut)
		if !ok {
			continue
		}
		if exemptPiped && seg.pipedInto && (key == "read" || key == "grep") {
			continue
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

// commandSegment is one simple-command candidate from a shell string, with
// whether it consumes piped stdin (its preceding unquoted separator was a
// single `|`).
type commandSegment struct {
	text      string
	pipedInto bool
	// redirectsOut records an unquoted `>` or `>>` in this segment. It is
	// captured here, during the scan, because quoting is still known: by the
	// time the segment text is re-split into fields the quotes are gone and a
	// `sed 's/a>b/c/'` expression is indistinguishable from a real redirect.
	redirectsOut bool
}

// splitCommandSegments splits a shell command into simple-command candidates on
// unquoted separators (pipes, list operators, subshell/group delimiters,
// backticks, newlines). Quote characters are dropped; their inner text is kept
// so a quoted pipe never causes a split. Each segment records whether it is the
// consumer side of a single `|` pipe; `||` (logical OR) and `&&`/`&` are list
// separators, not pipes.
func splitCommandSegments(command string) []commandSegment {
	runes := []rune(command)
	var segs []commandSegment
	var cur strings.Builder
	var quote rune
	curPiped := false    // is the segment currently accumulating downstream of a `|`?
	curRedirect := false // has this segment redirected stdout to a file?
	flush := func(nextPiped bool) {
		segs = append(segs, commandSegment{text: cur.String(), pipedInto: curPiped, redirectsOut: curRedirect})
		cur.Reset()
		curPiped = nextPiped
		curRedirect = false
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
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
		case '|':
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++ // `||` is logical OR, not a pipe
				flush(false)
			} else {
				flush(true)
			}
		case ';', '&', '\n', '(', ')', '`', '{', '}':
			flush(false)
		case '>':
			// `2>&1` duplicates a descriptor, it does not write a file.
			if i+1 >= len(runes) || runes[i+1] != '&' {
				curRedirect = true
			}
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	flush(false)
	return segs
}

// commandFields returns a segment's command word followed by its arguments,
// skipping leading environment assignments (FOO=bar) and wrapper commands
// (sudo/env/...). The word is base-named so /usr/bin/grep, \grep and grep.exe
// all resolve to grep. Returns nil when the segment has no command word.
//
// Arguments are kept because classification needs them: without them the guard
// cannot tell `sed -i` (an edit) from `sed -n '1,10p'` (a read).
func commandFields(segment string) []string {
	fields := strings.Fields(segment)
	i := 0
	for i < len(fields) && (isEnvAssignment(fields[i]) || isCommandWrapper(fields[i])) {
		i++
	}
	if i >= len(fields) {
		return nil
	}
	out := make([]string, 0, len(fields)-i)
	out = append(out, baseName(fields[i]))
	out = append(out, fields[i+1:]...)
	return out
}

// commandWord returns just the base command name of a segment, or "" when it
// has none.
func commandWord(segment string) string {
	fields := commandFields(segment)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// Returns true if a token is a valid environment variable assignment (VAR=value format).
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

// Returns true if a token is a command wrapper like env, sudo, doas, command, nohup, time, exec, or builtin.
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

// Emits a PreToolUse hook event that denies tool execution with a reason as JSON.

func emitPreToolDeny(output io.Writer, reason string) {
	json.NewEncoder(output).Encode(map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	})
}

// Emits a PostToolUse hook event with additional context message as JSON.
func emitPostToolWarning(output io.Writer, msg string) {
	json.NewEncoder(output).Encode(map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":     "PostToolUse",
			"additionalContext": msg,
		},
	})
}
