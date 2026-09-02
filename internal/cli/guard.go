package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"aracne/internal/helper"
	"aracne/internal/toolspec"
)

// RunGuard is the `arac guard` entry point. It supports the Claude Code
// PreToolUse/PostToolUse hook invocation (`--claude-hook`), which reads the hook
// event JSON on stdin and writes a decision on stdout, and `--pre-scan`, the
// bare scan.pre_tool scan for harnesses whose plugin API hands the guard no
// event -- OpenCode's `tool.execute.before`, which needs the freshness half of
// the hook and has no decision to make.
func RunGuard(args []string) {
	if len(args) == 1 {
		switch args[0] {
		case "--claude-hook":
			runClaudeGuardHook(os.Stdin, os.Stdout)
			return
		case "--pre-scan":
			runPreToolScanCommand()
			return
		}
	}
	fmt.Fprintln(os.Stderr, "Usage: arac guard --claude-hook | arac guard --pre-scan")
	os.Exit(1)
}

// runPreToolScanCommand runs the configured pre-tool scan against the project the working
// directory belongs to. It prints nothing and always exits 0: the caller is a plugin running
// in front of an agent's tool call, and a scan that failed must not turn into a tool call that
// failed.
func runPreToolScanCommand() {
	dbPath := guardDBPath("")
	preToolScan(dbPath, helper.LoadConfig(helper.ConfigPath(dbPath)))
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
		// Cwd is the SESSION directory Claude Code reports on every hook event. It is the
		// one working directory the agent's own `cd` cannot move, which is why guardDBPath
		// prefers it over the process cwd -- see guardDBPath for what that cost us.
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || event.ToolName == "" {
		return
	}

	dbPath := guardDBPath(event.Cwd)
	cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
	blocked, exemptPiped := loadGuardConfig(dbPath)
	switch event.HookEventName {
	case "PreToolUse":
		// Freshness first: everything below reads the topology -- interception asks whether
		// the target is indexed, a proxied read serves its spans -- and answering from a
		// stale graph is worse than not answering at all. A no-op when scan.pre_tool is
		// "none", and an incremental diff otherwise.
		preToolScan(dbPath, cfg)
		// Interception comes first, and takes precedence over any denial the same command
		// would have earned. A rewrite gives the model the aracne answer in the call it
		// already made; a denial gives it a pointer and costs it another turn. When both
		// could apply, the cheaper one wins.
		if event.ToolName == "Bash" {
			if command, _ := event.ToolInput["command"].(string); command != "" {
				if rewritten, ok := interceptCommand(command, dbPath, cfg); ok {
					emitPreToolRewrite(output, event.ToolInput, rewritten)
					return
				}
			}
		}
		// blocked_tools stays a working knob on BOTH surfaces. What changes is where the
		// refusal SENDS the model: naming `mcp__aracne__…` to an agent with no MCP server is
		// the one failure mode guaranteed to cost a turn, so on the terminal surface the
		// reason names the shell forms aracne does answer and the `arac` subcommands behind
		// them. A denial reached here has already survived interception, which means aracne
		// could not serve the command as written -- so telling the model which spelling it
		// CAN serve is the whole value of the refusal.
		if d := decideGuard(event.ToolName, event.ToolInput, blocked, exemptPiped, dbPath, !cfg.MCPEnabled()); d.Deny {
			emitPreToolDeny(output, d.Message)
		}
		// Note: decideGuard already folded in the proxied content when it could, so the
		// denial either carries the file or carries the pointer -- never both.
	case "PostToolUse":
		keys := implicatedKeys(event.ToolName, event.ToolInput, exemptPiped)
		parts := []string{}
		// On the terminal surface a Bash read was either already answered by aracne (the
		// PreToolUse rewrite) or is one aracne cannot answer at all; nudging it is bytes
		// spent advertising something the agent just got, or something that does not exist.
		// A NATIVE Read/Grep/Edit/Write is different: interception never sees it, so the
		// nudge is the only place the model learns the shell forms are the cheaper question.
		terminal := cfg.EffectiveInterceptShell()
		if !terminal || event.ToolName != "Bash" {
			if msg := warningMessage(keys, !blocked[toolspec.ReadToolName], terminal); msg != "" {
				parts = append(parts, msg)
			}
		}
		// The backstop for everything the classifier does not know how to refuse. Only Bash
		// needs it: a native Edit/Write is already followed by the update-file hook.
		//
		// `arac` itself is exempt: it is unclassified (not a shell read/edit command), so the
		// backstop would otherwise run a full incremental scan after EVERY aracne call. That
		// matters most for the bug pipeline, whose slash commands orchestrate through
		// `arac bug list` several times per fan-out round -- and whose scans would each run
		// the orphan-bug cleanup. Any arac subcommand that touches source already syncs the
		// topology itself.
		if event.ToolName == "Bash" && mayHaveWrittenSource(keys) && !isAracCommand(event.ToolInput) {
			if msg := driftCheck(dbPath); msg != "" {
				parts = append(parts, msg)
			}
		}
		if len(parts) > 0 {
			emitPostToolWarning(output, strings.Join(parts, "\n\n"))
		}
	}
}

// guardDecision is the result of evaluating a tool call against blocked_tools.
type guardDecision struct {
	Deny    bool
	Message string // deny reason shown to the model (empty when not denied)
}

// decideGuard reports whether a tool call must be blocked and the reason.
func decideGuard(toolName string, toolInput map[string]interface{}, blocked map[string]bool,
	exemptPiped bool, dbPath string, terminal bool) guardDecision {
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

	// A command that never touches the indexed project is not what blocked_tools is for: there
	// is no resource to read and no topology to keep in sync, so the aracne tools have nothing
	// to offer in exchange for the refusal. Nine of fifty denials in netguard-20260831b were
	// this -- an agent writing a throwaway repro script to /tmp -- each costing a turn and
	// answered with advice naming tools that cannot write there.
	if toolName == "Bash" {
		if cmd, _ := toolInput["command"].(string); operatesOutsideProject(cmd, projectRoot(dbPath)) {
			return guardDecision{}
		}
	}

	// A read of a file the topology does not model is the same trade in a different currency:
	// aracne would answer it with `rawFileUnit`, which is the identical raw bytes the shell
	// command asked for, minus its line window. Blocking it removes a capability and offers
	// nothing back -- 7 of the 35 denials in batched-20260901a were this, and they caused the
	// worst regression in that matrix. Reads only, and only when READ is the sole thing
	// denied: an edit must still go through the tool that re-syncs the topology, whether or
	// not the file is indexed today.
	if len(denied) == 1 && denied[0] == toolspec.ReadToolName {
		switch toolName {
		case "Bash":
			if cmd, _ := toolInput["command"].(string); readsOnlyUntrackedFiles(cmd, dbPath) {
				return guardDecision{}
			}
		default:
			if path, _ := toolInput["file_path"].(string); readsOnlyUntrackedPath(path, dbPath) {
				return guardDecision{}
			}
		}
	}

	reason := fmt.Sprintf("Blocked by aracne config (blocked_tools: %s). ", strings.Join(denied, ", "))

	// A denied READ can be answered rather than merely refused: the model has already said
	// which file it wants, and the guard can hand it over with its context attached for the
	// same one call. Reads only -- an edit or a write must go through the tool that re-syncs
	// the topology, which is the entire reason it was blocked.
	if toolName == "Bash" && len(denied) == 1 && denied[0] == toolspec.ReadToolName {
		cmd, _ := toolInput["command"].(string)
		if content := proxyRead(cmd, dbPath); content != "" {
			return guardDecision{Deny: true, Message: reason +
				"Reading it for you, with the context it connects to -- no follow-up call needed:\n\n" +
				content}
		}
	}

	if warn := warningMessage(keys, !blocked[toolspec.ReadToolName], terminal); warn != "" {
		reason += warn
	} else if terminal {
		reason += "Use the shell forms aracne answers, or an `arac` subcommand, instead of this command."
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

// isAracCommand reports whether every command word in a Bash invocation is `arac`.
//
// The drift backstop treats an unclassified command as "might have written source", which is
// right for an unknown tool and wrong for aracne's own CLI: `arac edit`/`arac write` already
// sync the topology, and the read-only subcommands touch nothing. Requiring EVERY segment to
// be arac keeps a chained `arac bug list && sed -i ...` classified normally.
func isAracCommand(toolInput map[string]interface{}) bool {
	cmd, _ := toolInput["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		return false
	}
	sawArac := false
	for _, seg := range splitCommandSegments(cmd) {
		fields := commandFields(seg.text)
		if len(fields) == 0 {
			continue
		}
		if filepath.Base(strings.TrimSuffix(strings.ToLower(fields[0]), ".exe")) != "arac" {
			return false
		}
		sawArac = true
	}
	return sawArac
}

// warningMessage joins the per-key guidance for the given keys, skipping keys
// without an MCP equivalent (e.g. "bash") and de-duplicating. nativeReadAvailable
// resolves the read tool's runtime name so the guidance never points at a name
// that is absent from this agent's tool list.
func warningMessage(keys []string, nativeReadAvailable, terminal bool) string {
	var parts []string
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		if w := toolspec.WarningForSurface(k, nativeReadAvailable, terminal); w != "" {
			parts = append(parts, w)
		}
	}
	return strings.Join(parts, " ")
}

// loadGuardConfig resolves the guard inputs from the claude_code main agent
// config: the blocked_tools set and whether piped read/grep commands are exempt
// (read.pipe_passthrough). A missing/unparseable config fails open: no blocks
// and the exemption on, so the session is never broken.
//
// Callers derive "is the native read still available" as !blocked[ReadToolName], which is
// NativeReadAvailable(cfg, "claude_code", "main") spelled inline -- the guard only ever runs
// for the Claude Code main agent, and the fail-open empty set yields the same `true`. Keep
// the two in step: the warning must name the read tool the agent actually has.
func loadGuardConfig(dbPath string) (blocked map[string]bool, exemptPiped bool) {
	cfg, ok := helper.LoadConfigStrict(helper.ConfigPath(dbPath))
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
		var key string
		var ok bool
		if toolspec.IsInterpreter(word) {
			// An interpreter's program is its argument, and splitCommandSegments cuts on
			// parentheses (it has to, for subshells) -- which shreds exactly the
			// `open('x.py','w')` text that says what the program does. Classify these
			// against the whole command line instead of the fragment.
			key, ok = toolspec.InterpreterProgramKey(word, command)
		} else {
			key, ok = toolspec.ShellCommandKeyForArgs(word, fields[1:], seg.redirectsOut)
		}
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

// quotedSpace stands in for a space inside a quoted run, so strings.Fields keeps the run
// as a single token. It is a control character precisely because no real command word or
// path can contain one, making the substitution unambiguous downstream.
const quotedSpace = '\x00'

// commandSegment is one simple-command candidate from a shell string, with
// whether it consumes piped stdin (its preceding unquoted separator was a
// single `|`).
type commandSegment struct {
	text      string
	pipedInto bool
	// start and end are this segment's byte offsets in the ORIGINAL command string.
	//
	// They exist so interception can rewrite one segment of a compound command in place and
	// leave every other byte alone. `text` cannot serve: the scanner drops quote characters
	// and substitutes a control byte for the spaces they held, so it is a classification
	// input, not something that can be spliced back into a shell line.
	start, end int
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
	// Byte offset of each rune, so a segment can be located in the original string.
	byteOf := make([]int, len(runes)+1)
	pos := 0
	for i, r := range runes {
		byteOf[i] = pos
		pos += utf8.RuneLen(r)
	}
	byteOf[len(runes)] = pos

	var segs []commandSegment
	var cur strings.Builder
	var quote rune
	curPiped := false    // is the segment currently accumulating downstream of a `|`?
	curRedirect := false // has this segment redirected stdout to a file?
	segStart := 0        // rune index where the current segment began
	flush := func(endRune, nextStart int, nextPiped bool) {
		segs = append(segs, commandSegment{
			text:         cur.String(),
			pipedInto:    curPiped,
			redirectsOut: curRedirect,
			start:        byteOf[segStart],
			end:          byteOf[endRune],
		})
		cur.Reset()
		curPiped = nextPiped
		curRedirect = false
		segStart = nextStart
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			switch {
			case r == quote:
				quote = 0
			case r == ' ' || r == '\t':
				// Hold a quoted run together as ONE field. The quote characters
				// themselves are dropped (so `sed -n '1,20p' f` still classifies), but
				// without this a quoted argument fragments and its words become
				// indistinguishable from real command words -- which is how
				// `echo "use grep here"` came to look like a grep.
				cur.WriteRune(quotedSpace)
			default:
				cur.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '|':
			if i+1 < len(runes) && runes[i+1] == '|' {
				flush(i, i+2, false) // `||` is logical OR, not a pipe
				i++
			} else {
				flush(i, i+1, true)
			}
		case ';', '&', '\n', '(', ')', '`', '{', '}':
			flush(i, i+1, false)
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
	flush(len(runes), len(runes), false)
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
	if j := wrappedCommandIndex(fields, i); j > i {
		i = j
	}
	out := make([]string, 0, len(fields)-i)
	out = append(out, baseName(fields[i]))
	out = append(out, fields[i+1:]...)
	return out
}

// wrappedCommandIndex handles the wrapper this guard has never heard of.
//
// isCommandWrapper knows the standard ones, but a benchmark run found a real read walking
// straight through as `rtk proxy sed -n '1280,1400p' src/parse/parser.rs` -- a local
// token-saving proxy that happens to be on PATH. Enumerating every such binary is a losing
// game, so when the leading word means nothing to the classifier, look a little further
// along for one that does.
//
// The scan is bounded to the words BEFORE the first flag. That is what keeps it honest:
// `grep -rn "cat" .` never reaches here (grep is already known), and a wrapper's own
// options cannot drag a filename that happens to be called `cat` into the command slot.
func wrappedCommandIndex(fields []string, start int) int {
	word := strings.ToLower(baseName(fields[start]))
	if _, known := toolspec.ShellCommandKey(word); known {
		return start
	}
	// `git` is not "unknown", it is deliberately unclassified so that `git grep` keeps
	// working -- ShellCommandKeyForArgs handles its subcommands itself. Scanning past it
	// would both resurrect the `git grep` refusal this design exists to avoid and, worse,
	// match the revision `HEAD` as the pager `head`.
	if word == "git" {
		return start
	}
	for j := start + 1; j < len(fields); j++ {
		if strings.HasPrefix(fields[j], "-") {
			return start
		}
		if _, known := toolspec.ShellCommandKey(strings.ToLower(baseName(fields[j]))); known {
			return j
		}
	}
	return start
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

// Returns true if a token is a command wrapper like env, sudo, doas, command, nohup, time,
// exec, builtin, or one of the scheduling/buffering wrappers that prefix a real command.
func isCommandWrapper(tok string) bool {
	switch strings.ToLower(baseName(tok)) {
	case "env", "sudo", "doas", "command", "nohup", "time", "exec", "builtin",
		"timeout", "stdbuf", "nice", "ionice", "xargs", "watch":
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
