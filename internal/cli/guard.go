package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
)

// RunGuard is the `arac guard` entry point. It supports the Claude Code
// PreToolUse/PostToolUse hook invocation (`--claude-hook`), which reads the hook
// event JSON on stdin and writes a decision on stdout, and two halves of the same
// job for a harness whose plugin API hands the guard no event to answer:
// `--pre-scan`, the bare scan.pre_tool scan, and `--rewrite`, the interception
// decision on its own.
func RunGuard(args []string) {
	switch {
	case len(args) == 1 && args[0] == "--claude-hook":
		runClaudeGuardHook(os.Stdin, os.Stdout)
		return
	case len(args) == 1 && args[0] == "--pre-scan":
		runPreToolScanCommand()
		return
	case len(args) == 2 && args[0] == "--rewrite":
		runRewriteCommand(args[1], os.Stdout)
		return
	}
	fmt.Fprintln(os.Stderr,
		"Usage: arac guard --claude-hook | arac guard --pre-scan | arac guard --rewrite <command>")
	os.Exit(1)
}

// runRewriteCommand answers `arac guard --rewrite <command>`: the interception decision on its
// own, for a harness that hands the guard the tool's ARGUMENTS to mutate rather than a hook
// event to answer.
//
// WHY IT EXISTS. Interception was Claude Code only, because it was written against the one
// mechanism Claude Code offers -- a PreToolUse hook returning
// `hookSpecificOutput.updatedInput`. OpenCode has the same capability spelled differently:
// `tool.execute.before` receives a mutable `output.args`. Without this, an OpenCode project on
// either intercepting mode had no read surface at all and a generated AGENTS.md telling it that
// `cat`, `head -40` and `sed -n` came back enriched, and every mode's contract promised an
// annotated `grep` that nothing delivered -- a contract naming a capability the surface does
// not have, which is the failure internal/prompts/contract.go says it exists to prevent.
//
// Both surfaces go through interceptCommand, so they cannot decide differently -- the same
// reason `arac cmd` is a real verb rather than logic inside the hook.
//
// It prints `{"command": "..."}` when there is a rewrite and `{}` when there is not, and always
// exits 0: a plugin standing in front of a tool call must never turn a decision it could not
// make into a tool call that failed. The freshness scan is deliberately NOT run here -- the
// plugin has already run `--pre-scan` for this same call, and scanning twice would double the
// cost of every tool call on that harness.
func runRewriteCommand(command string, output io.Writer) {
	answer := map[string]string{}
	dbPath := guardDBPath("")
	if rewritten, ok := interceptCommand(command, dbPath, helper.LoadConfig(helper.ConfigPath(dbPath))); ok {
		// Recorded for the same reason the Claude Code path records it: the rewrite reaches the
		// model as the tool's own arguments, so the transcript keeps the command the model
		// WROTE and nothing downstream can tell the two apart. See GuardLogEnv.
		logGuardDecision(guardRewrote, "bash", command)
		answer["command"] = rewritten
	}
	json.NewEncoder(output).Encode(answer)
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
		// A WHOLE-BASH BLOCK IS THE ONE ENTRY NOTHING MAY BYPASS, and it is decided before
		// anything else for that reason.
		//
		// It used to be decided last, inside decideGuard, where two earlier rules reached it
		// first and neither is wrong on its own. Interception takes precedence over a denial
		// (below), so `grep foo .` was rewritten and RAN under a config that had disabled the
		// Bash tool. operatesOutsideProject returns an empty decision for a command touching
		// nothing indexed, so `rm -rf /tmp/x` was permitted while `ls -la` was refused. Both
		// exemptions are about routing a capability to a better surface; `bash` in
		// blocked_tools is not a routing decision, it is the operator switching the tool off.
		if event.ToolName == "Bash" && blocked["bash"] {
			logGuardDecision(guardDenied, event.ToolName, guardLoggedCommand(event.ToolInput))
			emitPreToolDeny(output, bashBlockedReason(cfg.Surface()))
			return
		}
		// Interception comes first, and takes precedence over any denial the same command
		// would have earned. A rewrite gives the model the aracne answer in the call it
		// already made; a denial gives it a pointer and costs it another turn. When both
		// could apply, the cheaper one wins.
		if event.ToolName == "Bash" {
			if command, _ := event.ToolInput["command"].(string); command != "" {
				if rewritten, ok := interceptCommand(command, dbPath, cfg); ok {
					// Recorded here rather than inferred downstream: the rewrite reaches the
					// model as `updatedInput`, and the transcript keeps the command the model
					// WROTE -- so this is the only place that knows a command was answered
					// from the topology instead of run. See GuardLogEnv.
					logGuardDecision(guardRewrote, event.ToolName, command)
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
		if d := decideGuard(event.ToolName, event.ToolInput, blocked, exemptPiped, dbPath, cfg.Surface()); d.Deny {
			logGuardDecision(guardDenied, event.ToolName, guardLoggedCommand(event.ToolInput))
			emitPreToolDeny(output, d.Message)
		} else {
			// The passthrough count is what makes the rewrite count readable: "2 rewrites"
			// means nothing without "out of how many commands the guard saw".
			logGuardDecision(guardPassedT, event.ToolName, guardLoggedCommand(event.ToolInput))
		}
		// Note: decideGuard already folded in the proxied content when it could, so the
		// denial either carries the file or carries the pointer -- never both.
	case "PostToolUse":
		keys := implicatedKeys(event.ToolName, event.ToolInput, exemptPiped)
		parts := []string{}
		// A Bash read was either already answered by aracne (the PreToolUse rewrite) or is one
		// aracne cannot answer at all; nudging it is bytes spent advertising something the
		// agent just got, or something that does not exist. A NATIVE Read/Grep is different:
		// interception never sees it, so the nudge is the only place the model learns which
		// spelling is the cheaper question. What is left of each key set after nudgeKeys is
		// what still has something to say.
		switch {
		case event.ToolName != "Bash":
			// A native Read/Grep. Interception never sees these, so the nudge is the only
			// place the model learns the shell forms are the cheaper question -- but only
			// where aracne has something to offer in exchange. See worthNudging.
			if !worthNudging(event.ToolInput, dbPath) {
				break
			}
			if msg := warningMessage(nudgeKeys(keys, false), !blocked[toolspec.ReadToolName], cfg.Surface()); msg != "" {
				parts = append(parts, msg)
			}
		case cfg.EffectiveMode() == helper.ModeMCP:
			// ModeMCP: a Bash read is refused rather than rewritten, and the refusal already
			// names the tool, so this is the fallback for the ones blocked_tools let through.
			//
			// Tested on the MODE, not on `!InterceptReads()`. That predicate is also true in
			// ModeCLI, where a shell read is deliberately left alone and earns nothing --
			// testing the mode is what keeps cli from falling through and printing the MCP
			// pointer at a read it was never going to refuse.
			//
			// It needs the same evidence the native arm above needs, and had none: this arm
			// emitted unconditionally, so `cat CHANGELOG.md`, `cat nonexistent.txt` and
			// `cat /etc/hostname` all came back telling the model to use
			// `mcp__aracne__read_resource` on something the tool cannot answer better, or at
			// all. See worthNudgingBash.
			if !worthNudgingBash(event.ToolInput, dbPath) {
				break
			}
			if msg := warningMessage(nudgeKeys(keys, true), !blocked[toolspec.ReadToolName], cfg.Surface()); msg != "" {
				parts = append(parts, msg)
			}
		}
		if driftCheckApplies(event.ToolName, keys, event.ToolInput) {
			// The tool name is threaded in for the log alone: driftCheck fires after a native
			// Edit/Write as well as after a shell write, and the warning telemetry recorded
			// every batch as "Bash" -- which reports zero for the native path, the one that
			// has no other signal. See logGuardWarnings.
			if msg := driftCheck(dbPath, event.ToolName); msg != "" {
				parts = append(parts, msg)
			}
		}
		if len(parts) > 0 {
			logGuardDecision(guardNudged, event.ToolName, guardLoggedCommand(event.ToolInput))
			emitPostToolWarning(output, strings.Join(parts, "\n\n"))
		}
	}
}

// nudgeKeys narrows the implicated keys to the ones a nudge still has something to say about.
//
// A MUTATION SAYS IT ITSELF. The `arac edit` pointer sold the topology SYNC, and the sync
// reports itself now: driftCheck attaches the warnings an edit caused to the edit that caused
// them, on the native and the shell path alike. Nudging as well spends bytes on every edit
// advertising a second spelling of what the model just got -- and on an edit that breaks
// nothing the nudge was the ONLY thing attached, a hook firing with no finding behind it.
// blocked_tools still names `arac edit` when it REFUSES one; that reason comes from
// decideGuard, which reads the full key set and is untouched by this.
//
// A BASH GREP IS ALREADY ANSWERED. InterceptGrep is true in every mode, so the search came
// back topology-annotated and the pointer would be describing output the model is holding. A
// NATIVE grep is never rewritten -- the hook sees it too late -- so that one keeps its nudge,
// and so does a bash read in ModeMCP, the one surface where nothing intercepts one.
//
// "bash" needs no case: it has no entry in any guidance table, so warningMessage skips it.
func nudgeKeys(keys []string, isBash bool) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		switch k {
		case "edit", "write":
			continue
		case toolspec.GrepToolName:
			if isBash {
				continue
			}
		}
		out = append(out, k)
	}
	return out
}

// worthNudging reports whether a native read or search has anything to gain from the pointer.
//
// IT NEEDS EVIDENCE. For a file the topology holds no nodes for -- a
// CHANGELOG, a lockfile, a template, an unsupported language -- aracne cannot answer the read
// better, and the nudge tells the model that "`cat` on an indexed file is answered from the
// topology" about a file that is not indexed. The hook fires on every matching call and its
// text is injected into the transcript each time, so that is real cost spent teaching a rule
// that fails the next time the model tries it. namesAnIndexedFile is the evidence test, and it
// requires a positive: unlike interception, the nudge has no second check downstream -- what it
// decides is what the model reads.
//
// A MUTATION NEVER REACHES IT. nudgeKeys drops edit and write before the key set gets here,
// and carries the reasoning for that; the index test below is about reads and searches only.
func worthNudging(toolInput map[string]interface{}, dbPath string) bool {
	paths := claudeHookPaths(toolInput)
	if len(paths) == 0 {
		// A Grep names a pattern and maybe a path glob; with nothing to resolve there is no
		// counter-evidence, and the search guidance holds for the tree as a whole.
		return true
	}
	return namesAnIndexedFile(paths, dbPath)
}

// worthNudgingBash is worthNudging for a SHELL read, which reaches the nudge only in ModeMCP.
//
// Same two questions, because the same two answers make the pointer worthless. A command that
// touches nothing inside the project has no resource for an aracne tool to serve, and a file
// the topology holds no nodes for would come back as the identical raw bytes -- so in both
// cases the nudge teaches a rule that fails the moment the model follows it, and its text is
// injected into the transcript on every matching call.
//
// A command with no path-shaped operand at all keeps the nudge: `grep -r foo` and a `cat`
// reading stdin resolve to nothing, and "nothing to resolve" is not counter-evidence -- the
// same reading worthNudging applies to a pathless Grep.
func worthNudgingBash(toolInput map[string]interface{}, dbPath string) bool {
	command, _ := toolInput["command"].(string)
	if operatesOutsideProject(command, projectRoot(dbPath)) {
		return false
	}
	paths := commandPaths(command)
	if len(paths) == 0 {
		return true
	}
	return namesAnIndexedFile(paths, dbPath)
}

// bashBlockedReason is the refusal for `blocked_tools: ["bash"]`.
//
// It must not name a shell form or an `arac` subcommand, and the generic reason it replaces
// named both: "Use the shell forms aracne answers, or an `arac` subcommand, instead of this
// command" -- every one of which needs the tool that was just denied. `arac read main.go` was
// itself refused with that sentence, so a model following the advice looped. What is left has
// to be a capability the agent still has.
func bashBlockedReason(surface toolspec.Surface) string {
	const reason = "Blocked by aracne config (blocked_tools: bash). The Bash tool is disabled " +
		"for this agent, so no shell command and no `arac` subcommand can run. "
	if surface == toolspec.SurfaceMCP {
		return reason + "Use the aracne MCP tools and this harness's own native tools instead."
	}
	return reason + "Use this harness's own native tools instead."
}

// driftCheckApplies reports whether a tool call is worth re-syncing the topology after.
//
// TWO KINDS OF CALL REACH IT, and the second was missing.
//
// A BASH command fires the check when the classifier saw a write, and ALSO when it
// recognized nothing at all: an unclassified command is precisely the case the backstop
// exists for, since a command the classifier understands is already governed by the guard's
// own rules. `arac` itself is exempt -- it is unclassified (not a shell read/edit command),
// so the backstop would otherwise run a full incremental scan after EVERY aracne call. That
// matters most for the bug pipeline, whose slash commands orchestrate through `arac bug list`
// several times per fan-out round -- and whose scans would each run the orphan-bug cleanup.
// Any arac subcommand that touches source already syncs the topology itself.
//
// A NATIVE Edit/Write/MultiEdit/NotebookEdit fires it too, and used not to. The reasoning was
// that the `arac update-file` hook already covers those -- but that hook is installed only
// when the project lists the edit-update-db-plugin, and NO shipped path lists it: the default
// config sets no plugins and `arac init` never asks. So a native edit produced no warning at
// all at the moment it broke something, and the warning the next PreToolUse scan discovered
// was delivered by whichever later Bash command happened to be unclassified -- an `ls`, a
// `go build` -- and attributed to it. The contract promises "act on any topology warning that
// comes back" in every mode; this is what makes that true for the default editing path.
func driftCheckApplies(toolName string, keys []string, toolInput map[string]interface{}) bool {
	if toolName == "Bash" {
		return mayHaveWrittenSource(keys) && !isAracCommand(toolInput)
	}
	// Reads and searches change nothing, so only the native mutators qualify. Asking
	// toolspec rather than listing the names keeps this in step with the hook matcher, which
	// is derived from the same map.
	key, ok := toolspec.NativeToolKey(toolName)
	return ok && (key == "edit" || key == "write")
}

// guardLoggedCommand pulls the command text out of a tool input for the event log, or ""
// for a tool that has none (a native Read/Grep/Edit/Write).
func guardLoggedCommand(toolInput map[string]interface{}) string {
	if c, ok := toolInput["command"].(string); ok {
		return c
	}
	return ""
}

// guardDecision is the result of evaluating a tool call against blocked_tools.
type guardDecision struct {
	Deny    bool
	Message string // deny reason shown to the model (empty when not denied)
}

// decideGuard reports whether a tool call must be blocked and the reason.
func decideGuard(toolName string, toolInput map[string]interface{}, blocked map[string]bool,
	exemptPiped bool, dbPath string, surface toolspec.Surface) guardDecision {
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

	if warn := warningMessage(keys, !blocked[toolspec.ReadToolName], surface); warn != "" {
		reason += warn
	} else if surface == toolspec.SurfaceMCP {
		reason += "Use the aracne MCP tools instead of this native/shell command."
	} else {
		reason += "Use the shell forms aracne answers, or an `arac` subcommand, instead of this command."
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
func warningMessage(keys []string, nativeReadAvailable bool, surface toolspec.Surface) string {
	var parts []string
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		if w := toolspec.WarningForSurface(k, nativeReadAvailable, surface); w != "" {
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
	// This is the one place the blocked set is decided -- every denial, every proxied read and
	// every warning that names a tool flows from it. BlockableInMode drops the entries this
	// mode must not refuse: in the intercepting modes that is all of them (a block would
	// refuse a call aracne was about to answer), and in ModeCLI it is `grep` alone,
	// which aracne answers there too.
	blockedSet := toolNameSet(cfg.EffectiveAgent("claude_code", "main").BlockedTools)
	return cfg.BlockableInMode(blockedSet), cfg.EffectivePipePassthrough()
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
	// endedBy is the character the scanner cut this segment at, or 0 at the end of the string.
	//
	// It is the difference between "this command ended" and "this command continues", which
	// nothing else here records. A `;`, `&` or newline ENDS a command and a `|` ends it into a
	// consumer; a `(`, `)`, `{`, `}` or backtick is a nesting boundary INSIDE one, so the
	// segment after it is the same command still being written. Without it, the walk that asks
	// what a producer feeds had to guess -- and guessed wrong for
	// `grep X $(echo .) extra.go | wc -l`, where the operand after the substitution looks
	// exactly like a new command and made the pipe disappear. See pipelineStages.
	endedBy rune
	// depth is how many unclosed substitutions, subshells or groups this segment sits
	// inside: 0 for a command whose stdout the caller reads, 1 or more for one whose output
	// is a VALUE the enclosing command consumes.
	//
	// It is captured during the scan for the same reason redirectsOut is -- afterwards the
	// nesting is gone. The scanner has always had to cut on `(`, `)` and a backtick so that a
	// subshell's contents are classified, and the resulting list said nothing about where a
	// segment had come from. Interception then spliced `arac cmd --` into the body of a
	// `$(...)`, so `X=$(cat f)` captured aracne's rendering -- fences, elision markers and a
	// `# CONTEXT:` block -- in place of the file, and nothing downstream could tell.
	depth int
}

// splitCommandSegments splits a shell command into simple-command candidates on
// unquoted separators (pipes, list operators, subshell/group delimiters,
// backticks, newlines). Quote characters are dropped; their inner text is kept
// so a quoted pipe never causes a split. Each segment records whether it is the
// consumer side of a single `|` pipe; `||` (logical OR) and `&&`/`&` are list
// separators, not pipes.
//
// AN `&` THAT BELONGS TO A REDIRECTION IS NOT A SEPARATOR. `2>&1`, `>&2` and `&>log` are one
// operator each, and cutting them in half produced a segment list whose neighbours were no
// longer the pipeline's own stages: `cat f 2>&1 | grep x` scanned as
// ["cat f 2>", "1 ", " grep x"], so the check that asks what a producer feeds looked at the
// `1` fragment, decided the read fed no pipe, and let it be rewritten -- the grep then
// searched aracne's rendering and returned FEWER matches than the real command, with nothing
// to mark it as a different answer.
//
// Each segment also records the nesting depth it sits at, so a caller can tell a top-level
// command from the body of a substitution. See commandSegment.depth.
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
	depth := 0           // unclosed `(`, `{` and backticks around the current segment
	inBacktick := false  // a backtick is its own closer, so it toggles rather than nests
	segStart := 0        // rune index where the current segment began
	flush := func(endRune, nextStart int, nextPiped bool, by rune) {
		segs = append(segs, commandSegment{
			text:         cur.String(),
			pipedInto:    curPiped,
			redirectsOut: curRedirect,
			depth:        depth,
			endedBy:      by,
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
				flush(i, i+2, false, ';') // `||` is logical OR, not a pipe
				i++
			} else {
				flush(i, i+1, true, '|')
			}
		case ';', '\n':
			flush(i, i+1, false, ';')
		case '&':
			// `2>&1` and `&>log` are redirection operators; only a bare `&` separates.
			if isRedirectAmpersand(runes, i) {
				cur.WriteRune(r)
				continue
			}
			flush(i, i+1, false, '&')
		case '(', '{':
			// Flushed at the OUTER depth -- the segment ending here is the one around the
			// group, not the one inside it -- and everything after opens one level deeper.
			flush(i, i+1, false, r)
			depth++
		case ')', '}':
			flush(i, i+1, false, r)
			if depth > 0 {
				depth--
			}
		case '`':
			flush(i, i+1, false, '`')
			if inBacktick {
				if depth > 0 {
					depth--
				}
				inBacktick = false
			} else {
				depth++
				inBacktick = true
			}
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
	flush(len(runes), len(runes), false, 0)
	return segs
}

// isRedirectAmpersand reports whether the `&` at i is half of a redirection operator rather
// than a command separator.
//
// Two shapes, and both are ordinary in agent-written commands: `2>&1` / `>&2` duplicate a file
// descriptor, and `&>file` / `&>>file` send both streams to one place. Neither ends a command,
// and treating them as if they did is what broke the pipeline adjacency the interception rules
// depend on -- see splitCommandSegments.
//
// The look-back is at the RAW runes rather than at the accumulated segment text, which has had
// its quote characters dropped: `echo ">"&ls` really is a separator, and runes[i-1] there is
// the quote, not the `>`.
func isRedirectAmpersand(runes []rune, i int) bool {
	if i > 0 && runes[i-1] == '>' {
		return true
	}
	return i+1 < len(runes) && runes[i+1] == '>'
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
