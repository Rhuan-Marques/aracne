// Package toolspec is the single source of truth for the tools that can be
// referenced by name in .aracne/config.json. Every tool has a canonical name
// (the value selected in config) and a short description used to render the
// "## Tools" listing in generated agent markdown. Config tool names are
// validated against this catalog so a typo errors out at init time.
package toolspec

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Spec describes a single tool.
type Spec struct {
	Name string
	Desc string // short tidbit for the "## Tools" agent listing
	MCP  bool   // valid in llm.<harness>.*.mcp_tools
	Chat bool   // valid in viz.chat.*.tools
}

// specs is the ordered catalog. Order is used only for deterministic listing
// of "all" tools; per-agent listings preserve the config order.
var specs = []Spec{
	// One read tool, not eight. The per-kind split (read_function, read_struct,
	// read_interface, read_named_type, read_file, read_package, read_dependency) asked models
	// to classify a resource before reading it -- something the ID resolver already does --
	// and they routinely picked the wrong one, paying a correction turn for it. Which KINDS
	// are readable moved to read.kinds, a project-wide setting; this entry only decides
	// whether an agent may read at all. The registered tool answers to "read_resource" when
	// the harness has its own native read (see toolspec.ReadToolName).
	{"read", "read any resources by ID -- one call takes several, and returns their source plus connected context", true, true},
	{"grep", "search node names, node descriptions and code contents, ranked in that order", true, true},
	{"edit", "apply exact string replacements to a file, or delete text with an empty new_string", true, true},
	{"write", "create or overwrite a file", true, true},
	{"warnings_list", "list topology warnings", true, true},
	{"bug_report", "report a confirmed bug on a node", true, true},
	{"bug_list", "list bugs by node and/or state", true, true},
	{"bug_acknowledge", "mark a bug as acknowledged (real, needs fixing)", true, true},
	{"bug_dismiss", "mark a bug as dismissed (false positive)", true, true},
	{"bug_delete", "delete a bug (fixed, or a duplicate false-positive)", true, true},
	{"update_description", "set a topology resource's description", true, true},
	{"node_list_no_description", "list undocumented resources needing descriptions", true, true},
	// Proprietary-chat native tools (not exposed as MCP tools).
	{"ls", "list directory contents", false, true},
	{"bash", "run shell commands", false, true},
	{"glob", "find files by glob pattern", false, true},
	{"ask_user_question", "ask the user a clarifying question", false, true},
	{"CreateTasks", "spawn sub-agent tasks to run in parallel", false, true},
}

var registry = func() map[string]Spec {
	m := make(map[string]Spec, len(specs))
	for _, s := range specs {
		m[s.Name] = s
	}
	return m
}()

// nativeBlockable is the set of native tool names valid in blocked_tools.
var nativeBlockable = map[string]bool{
	"read": true, "grep": true, "edit": true, "write": true, "bash": true,
}

// nativeToolToKey maps a Claude Code native tool name (PascalCase, as seen in
// a hook's tool_name) to its aracne tool key. Used by the guard hook.
var nativeToolToKey = map[string]string{
	"Read": "read", "Grep": "grep", "Edit": "edit", "Write": "write", "Bash": "bash",
}

// shellCmdToKey maps a POSIX-shell command name to the aracne tool key it
// stands in for, so the guard hook can warn/block on shell equivalents.
//
// The pager/dumper entries beyond `cat` were added after a benchmark run showed the agent
// reaching for whatever spelling was still open once `cat` was refused: `more`, `nl` and
// `od` read a file just as completely as `cat` does, so leaving them out only taught the
// model a synonym.
var shellCmdToKey = map[string]string{
	"cat": "read", "head": "read", "tail": "read", "less": "read",
	"more": "read", "nl": "read", "tac": "read", "strings": "read",
	"xxd": "read", "od": "read", "hexdump": "read", "bat": "read",
	"grep": "grep", "rg": "grep",
	"egrep": "grep", "fgrep": "grep", "zgrep": "grep", "ack": "grep", "ag": "grep", "ug": "grep",
	"sed": "edit", "awk": "edit",
	// File-producing commands. These are `edit` only when a destination argument looks like
	// source (see destinationWrites): `cp dist/a dist/b` in a build step must stay untouched.
	"cp": "edit", "mv": "edit", "tee": "edit", "install": "edit",
	"truncate": "edit", "dd": "edit", "patch": "edit", "ed": "edit",
	// Interpreters. Classified only when their inline program text names a source file
	// (see interpreterKey); a bare `python3 -c "print(1)"` stays unclassified.
	"python": "edit", "python3": "edit", "node": "edit", "perl": "edit",
	"ruby": "edit", "php": "edit", "deno": "edit", "bun": "edit",
	// git is NOT here on purpose: the command word must stay unclassified so `git grep`
	// and `git log` keep working. Only specific subcommands count -- see gitSubcommandKey.
}

// conditionalCmds are commands whose entry in shellCmdToKey is a placeholder: the real key
// depends on their arguments, and ShellCommandKeyForArgs decides. Listing them here keeps
// the "does the name alone settle it?" question in one place.
//
// streamEditors is the original member of this family (sed/awk read unless told to write);
// destinationWrites and interpreters were added for the same reason, so that a name-only
// lookup never blocks a command that does not actually touch source.
var (
	streamEditors     = map[string]bool{"sed": true, "awk": true}
	destinationWrites = map[string]bool{
		"cp": true, "mv": true, "tee": true, "install": true,
		"truncate": true, "dd": true, "patch": true, "ed": true,
	}
	interpreters = map[string]bool{
		"python": true, "python3": true, "node": true, "perl": true,
		"ruby": true, "php": true, "deno": true, "bun": true,
	}
)

// powershellCmdToKey maps PowerShell cmdlets to aracne tool keys (lowercased).
// Only unambiguous full cmdlet names are listed; the short aliases gc/sls are
// intentionally omitted to avoid colliding with unrelated user aliases.
var powershellCmdToKey = map[string]string{
	"get-content":   "read",
	"select-string": "grep",
	"set-content":   "edit",
	"add-content":   "edit",
	"out-file":      "edit",
}

// nativeWarnings is the model-facing guidance shown when a native tool or its shell
// equivalent is used. Keyed by aracne tool key; bash has no MCP equivalent and so has no
// warning.
//
// These are deliberately SHORT. The hook fires on every matching call and its text is
// injected into the transcript each time, so a 200-character nag repeated across eighty
// native calls costs ~16 KB — spent telling the model something the CLAUDE.md contract
// already explains once, for free, in the cached prompt.
//
// They are also phrased as an offer rather than a correction, which is what the shipped
// warn-only default actually means: the native tools are allowed, and aracne's equivalents
// have to win on merit. Forbidding them is what produced redundant read turns.
//
// The read key is absent here on purpose: its tool answers to two names depending on the
// agent's blocked_tools, so its guidance is built at call time by readWarning.
//
// grep, edit and write are absent for a different reason -- they are not MCP tools in ANY mode
// since the rework (IsShellServedTool), so their guidance is the shell form on every surface
// and lives in sharedShellWarnings. Naming `mcp__aracne__grep` here pointed a ModeMCP agent at
// a tool its own server does not register, which is the one failure guaranteed to cost a turn.
var nativeWarnings = map[string]string{}

// The per-mode guidance tables, keyed by aracne tool key.
//
// Only the READ entry varies: it is the one capability the four modes put in genuinely
// different places. Search and mutation are answered by the same `arac` subcommands in all
// four, so all four share sharedShellWarnings for those -- including ModeMCP, which has no
// tool for them either.
//
// WHY SEPARATE TABLES RATHER THAN ONE WITH SUBSTITUTIONS. The modes do not spell the same
// read capability differently -- they put it in different places. Under ModeAracneRead the read
// capability is a subcommand; under the intercepting modes it has no name at all, because it
// arrives as the shell command the model already typed. Text that says "call X" cannot be
// find-and-replaced into text that says "the command you just ran is answered"; the result is
// grammatical and useless.
//
// Same length discipline throughout: this text is injected on every matching call.
//
// The mutation and search entries are shared, because those two capabilities ARE the same in
// all three: `arac grep` and the piped `arac edit`/`arac write` exist in every mode.
var sharedShellWarnings = map[string]string{
	"grep": "aracne: `arac grep <pattern>` also searches node names and descriptions, which no plain grep can reach.",
	"edit": "aracne: `echo '{\"edits\":[…]}' | arac edit` applies several replacements in ONE " +
		"call, under one file lock, rolling back as a unit -- and updates the topology inline.",
	"write": "aracne: `echo '{\"file_path\":…,\"content\":…}' | arac write` updates the topology inline.",
}

// aracneReadWarnings point at the subcommand, because in that mode nothing intercepts a read.
var aracneReadWarnings = withShellWarnings(map[string]string{
	"read": "aracne: `arac read <id> <id>` returns those declarations with their connected " +
		"context, for a fraction of the file they sit in. Prefer it over opening the file.",
})

// interceptIDWarnings name the spellings aracne answers and the ids they take.
var interceptIDWarnings = withShellWarnings(map[string]string{
	"read": "aracne: `cat`, `head -N`, `tail -N` and `sed -n 'A,Bp'` on an indexed file are " +
		"answered from the topology, and take a resource ID where they take a path. " +
		"`arac read <id> <id>` reads several at once.",
})

// lineRangeWarnings name the same spellings without the id vocabulary that mode exists to stop
// advertising -- the ranges to read come back in the output itself.
var lineRangeWarnings = withShellWarnings(map[string]string{
	"read": "aracne: `cat`, `head -N`, `tail -N` and `sed -n 'A,Bp'` on an indexed file are " +
		"answered from the topology -- the lines you asked for, framed by their declaration, " +
		"with the exact range of everything they touch.",
})

// withShellWarnings layers a mode's own entries over the ones every non-MCP mode shares.
func withShellWarnings(own map[string]string) map[string]string {
	out := make(map[string]string, len(own)+len(sharedShellWarnings))
	for k, v := range sharedShellWarnings {
		out[k] = v
	}
	for k, v := range own {
		out[k] = v
	}
	return out
}

// readWarning is the read tool's guidance, named for the tool the agent actually has. A
// static name is wrong half the time: blocking the harness's own read is exactly what makes
// the aracne tool register as "read", and that is also the case where the guidance arrives
// attached to a DENIAL -- so naming "read_resource" there refuses the call and sends the
// model to a tool that is not in its list. See ResolveReadToolName.
func readWarning(nativeReadAvailable bool) string {
	return "aracne: `mcp__aracne__" + ResolveReadToolName(nativeReadAvailable) +
		"` takes several ids at once and returns their neighbours too."
}

// ReadToolNames are the names the single read tool can answer to at runtime.
const (
	// ReadToolName is used when the harness's own read is blocked for this agent, so the
	// short name is free.
	ReadToolName = "read"
	// ReadResourceToolName is used when the harness still offers a native read. Two tools
	// called "read" in one session is a coin flip for the model even though the MCP prefix
	// makes them technically distinct, and the loser is usually the one that knows the
	// topology.
	ReadResourceToolName = "read_resource"
)

// ResolveReadToolName returns the name the read tool registers under. nativeReadAvailable
// reports whether the harness's own read tool is left enabled for this agent.
func ResolveReadToolName(nativeReadAvailable bool) string {
	if nativeReadAvailable {
		return ReadResourceToolName
	}
	return ReadToolName
}

// IsReadToolName reports whether name is either runtime name of the read tool.
func IsReadToolName(name string) bool {
	return name == ReadToolName || name == ReadResourceToolName
}

// ResolveToolName maps one catalog name to the name it registers under at runtime. Only the
// read tool has two runtime names; every other catalog name is its own runtime name.
func ResolveToolName(name string, nativeReadAvailable bool) string {
	if name == ReadToolName {
		return ResolveReadToolName(nativeReadAvailable)
	}
	return name
}

// ResolveToolNames maps a whole catalog-name list to runtime names, preserving order.
//
// Every generator that writes a tool name into a config or an agent file MUST go through
// this. The catalog key is "read", but the registered tool answers to "read_resource"
// whenever the harness keeps its own read -- and a generated allow-list naming a tool the
// server never registers does not fail loudly, it just silently denies the agent the tool.
// That is exactly how every generated Claude sub-agent lost the aracne read tool.
func ResolveToolNames(names []string, nativeReadAvailable bool) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, ResolveToolName(n, nativeReadAvailable))
	}
	return out
}

// bugToolPrefix marks the tools belonging to the bug pipeline, which ships behind
// features.bug_management. The naming convention IS the membership test: every tool in the
// pipeline is bug_*, and nothing else is.
const bugToolPrefix = "bug_"

// IsBugTool reports whether name belongs to the bug pipeline.
func IsBugTool(name string) bool { return strings.HasPrefix(name, bugToolPrefix) }

// Lookup returns the spec for a tool name.
func Lookup(name string) (Spec, bool) {
	s, ok := registry[name]
	return s, ok
}

// Description returns a tool's tidbit, or "" if unknown.
func Description(name string) string {
	if s, ok := registry[name]; ok {
		return s.Desc
	}
	return ""
}

// IsMCPTool reports whether name is a valid llm mcp_tools entry.
func IsMCPTool(name string) bool { s, ok := registry[name]; return ok && s.MCP }

// IsChatTool reports whether name is a valid viz.chat tools entry.
func IsChatTool(name string) bool { s, ok := registry[name]; return ok && s.Chat }

// IsNativeTool reports whether name is a valid blocked_tools entry.
func IsNativeTool(name string) bool { return nativeBlockable[name] }

// MCPToolNames returns the names of every tool valid as an llm mcp_tools entry,
// in catalog order.
func MCPToolNames() []string {
	var out []string
	for _, s := range specs {
		if s.MCP {
			out = append(out, s.Name)
		}
	}
	return out
}

// ChatToolNames returns the names of every tool valid as a viz.chat tools
// entry, in catalog order.
func ChatToolNames() []string {
	var out []string
	for _, s := range specs {
		if s.Chat {
			out = append(out, s.Name)
		}
	}
	return out
}

// NativeToolKey maps a Claude Code native tool name (e.g. "Grep") to its
// aracne tool key (e.g. "grep"), reporting false for unrecognized tools.
func NativeToolKey(toolName string) (string, bool) {
	key, ok := nativeToolToKey[toolName]
	return key, ok
}

// ShellCommandKey maps a shell command name (POSIX or PowerShell, any case) to
// the aracne tool key it stands in for, reporting false when there is none.
func ShellCommandKey(cmd string) (string, bool) {
	c := strings.ToLower(cmd)
	if key, ok := shellCmdToKey[c]; ok {
		return key, true
	}
	if key, ok := powershellCmdToKey[c]; ok {
		return key, true
	}
	return "", false
}

// ShellCommandKeyForArgs classifies a whole simple-command — the command word
// plus its arguments and whether the segment redirects stdout to a file —
// rather than the name alone.
//
// It exists for `sed` and `awk`. Classifying them as `edit` on the name meant
// `git log | sed -n '30,60p'` was refused: unambiguously a read of command
// output, no file operand, no -i, and nothing an MCP tool can serve instead.
// Reclassifying the non-mutating forms as `read` lets the ordinary pipe
// exemption cover them, so the policy stays in one place.
//
// A stream editor counts as `edit` when it writes in place (-i / --in-place,
// gawk's -i inplace) or redirects its output to a file. Everything else reads.
func ShellCommandKeyForArgs(word string, args []string, redirectsOut bool) (string, bool) {
	// Callers may pass a path-qualified word (/usr/bin/sed, sed.exe). The guard
	// normalizes before calling, but do not depend on that.
	name := strings.ToLower(baseCommandName(word))

	// git is deliberately absent from shellCmdToKey so `git grep`/`git log` stay open.
	// Only a handful of subcommands read or rewrite worktree files.
	if name == "git" {
		return gitSubcommandKey(args)
	}

	key, ok := ShellCommandKey(name)
	if !ok {
		return "", false
	}
	switch {
	case streamEditors[name]:
		if redirectsOut || streamEditorMutates(word, args) {
			return "edit", true
		}
		return "read", true
	case interpreters[name]:
		// The guard routes interpreters through InterpreterProgramKey with the full command
		// line, because the program text does not survive segment splitting. Reaching here
		// means a caller classified by name alone, which cannot decide this safely.
		return "", false
	case destinationWrites[name]:
		if commandTouchesSource(args) {
			return "edit", true
		}
		return "", false
	}
	return key, true
}

// gitSubcommandKey classifies a `git` invocation. Most subcommands are none of aracne's
// business, and saying so explicitly is what keeps `git grep` -- a genuinely better tool for
// some searches -- from being refused. Only two families matter:
//
//   - `git show <rev>:<path>` and `git cat-file` print a file's contents, so they are reads
//     wearing git as a disguise. Plain `git show` (a commit) is not.
//   - `git apply`, `git checkout -- <path>` and friends rewrite worktree files, which
//     desynchronizes the topology exactly the way a native write does.
func gitSubcommandKey(args []string) (string, bool) {
	sub := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			sub = strings.ToLower(a)
			break
		}
	}
	switch sub {
	case "apply", "restore", "revert", "am", "cherry-pick":
		return "edit", true
	case "checkout", "reset", "stash":
		// Only the worktree-rewriting forms. `git checkout <branch>` and `git stash list`
		// leave tracked file contents where the topology expects them.
		for _, a := range args {
			if a == "--" || a == "--hard" || a == "pop" || a == "apply" {
				return "edit", true
			}
		}
		return "", false
	case "cat-file":
		return "read", true
	case "show":
		// `git show HEAD:path/to/file.go` prints a file; `git show HEAD` prints a commit.
		//
		// Only the WORKING-TREE revision counts as a read the aracne tools could have served.
		// aracne indexes the checked-out tree and nothing else, so `git show origin/master:f`
		// asks for something it cannot answer at any price, and refusing it just removes a
		// capability with no replacement offered. Measured over four runs: `HEAD:` appeared 8
		// times and was always a plain source read (`HEAD:lib/response.js`,
		// `HEAD:tracing/src/span.rs`) -- the bypass this branch exists to close -- while every
		// other-revision use was one cell diffing its own change against `origin/master`.
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			i := strings.Index(a, ":")
			if i <= 0 || i >= len(a)-1 || !hasSourceExtension(a[i+1:]) {
				continue
			}
			if isWorkingTreeRev(a[:i]) {
				return "read", true
			}
			return "", false
		}
		return "", false
	}
	return "", false
}

// isWorkingTreeRev reports whether a git revision names what is checked out right now, which is
// the only revision the topology knows. `HEAD~1`, `HEAD^`, a branch and a SHA do not, and are
// therefore things aracne cannot serve at any price.
//
// Only `HEAD` qualifies. The index form (`git show :path`) never reaches here -- the caller
// requires a non-empty revision before the colon -- and claiming to handle it would be
// documenting a branch no input takes.
func isWorkingTreeRev(rev string) bool {
	return rev == "HEAD"
}

// IsInterpreter reports whether a command word runs an inline program (python, node, perl,
// …). The guard needs this before tokenizing, because an interpreter's program has to be
// classified against the whole command line rather than a paren-split fragment.
func IsInterpreter(word string) bool {
	return interpreters[strings.ToLower(baseCommandName(word))]
}

// InterpreterProgramKey classifies `python -c …`, `node -e …`, `perl -ne …` and friends by
// what their inline program actually does to a source file.
//
// The bar is deliberately high: a benchmark run found the agent reaching for
// `python3 -c "print(”.join(open('x.js').readlines()[600:760]))"` as its standing answer
// once `sed` was refused, and rewriting files through `python3 - <<EOF … open(p,'w') … EOF`
// in eight runs. But the same interpreters run build steps and test harnesses all day, so
// the program text must name something that looks like source before this fires at all.
//
// `program` is the whole command line, not the argument: quoting is already gone by the
// time the guard has fields, so there is nothing to be gained by locating the exact -c
// operand and a great deal to be lost by missing a heredoc body.
func InterpreterProgramKey(word, program string) (string, bool) {
	if !IsInterpreter(word) || !containsSourcePath(program) {
		return "", false
	}
	lower := strings.ToLower(program)
	// Write signals win over read signals: a script that opens for reading and then writes
	// is an edit, and the pessimistic reading is the safe one for topology freshness.
	//
	// The mode markers are matched unquoted (`,w)`) as well as quoted: the guard's tokenizer
	// strips quote characters before this ever runs, so `open(p,'w')` arrives as `open(p,w)`.
	for _, marker := range []string{`,'w'`, `,"w"`, ",w)", ", w)", `,'a'`, ",a)",
		"writefilesync", "write_text", "writefile", ".write(", "os.replace", "shutil.copy"} {
		if strings.Contains(lower, marker) {
			return "edit", true
		}
	}
	for _, marker := range []string{"open(", "readfilesync", "read_text", "readlines", "readfile"} {
		if strings.Contains(lower, marker) {
			return "read", true
		}
	}
	return "", false
}

// commandTouchesSource reports whether any argument looks like a source file, which is what
// separates `cp /tmp/x src/main.rs` (rewrites tracked source) from `cp dist/a dist/b`.
func commandTouchesSource(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if hasSourceExtension(a) {
			return true
		}
	}
	return false
}

// containsSourcePath reports whether a blob of program text mentions a path with a source
// extension. Deliberately crude: it only has to separate "this script touches code" from
// "this script prints a number".
func containsSourcePath(program string) bool {
	for _, field := range strings.FieldsFunc(program, func(r rune) bool {
		return r == '\'' || r == '"' || r == ' ' || r == '(' || r == ')' || r == ',' || r == '\n'
	}) {
		if hasSourceExtension(field) {
			return true
		}
	}
	return false
}

// sourceExtensions are the file suffixes that mean "this is code the topology tracks".
// Kept narrow on purpose: a false positive here refuses a command, and the guard's whole
// value depends on its refusals being obviously correct.
var sourceExtensions = map[string]bool{
	".go": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true,
	".rs": true, ".py": true, ".pyi": true, ".java": true, ".kt": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true, ".vue": true, ".svelte": true,
}

// hasSourceExtension reports whether a token ends in a tracked source extension, ignoring a
// trailing line/symbol suffix so `src/app.rs:95` still counts.
//
// Scratch paths are excluded. Agents legitimately write throwaway `.py`/`.rs` fixtures under
// /tmp to reproduce a bug -- the benchmark run has three such commands -- and the topology
// tracks none of them, so refusing those would be a denial that buys nothing.
func hasSourceExtension(tok string) bool {
	tok = strings.Trim(tok, `'"`)
	if i := strings.LastIndex(tok, ":"); i > 0 && !strings.Contains(tok[i:], "/") {
		tok = tok[:i]
	}
	if isScratchPath(tok) {
		return false
	}
	dot := strings.LastIndex(tok, ".")
	if dot < 0 {
		return false
	}
	return sourceExtensions[strings.ToLower(tok[dot:])]
}

// isScratchPath reports whether a path lives somewhere no project keeps tracked source.
func isScratchPath(tok string) bool {
	p := strings.ToLower(filepath.ToSlash(tok))
	for _, prefix := range []string{"/tmp/", "/var/tmp/", "/private/var/folders/", "/dev/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return strings.Contains(p, "/appdata/local/temp/") || strings.Contains(p, "/windows/temp/")
}

// streamEditorMutates reports whether a sed/awk invocation writes to a file.
func streamEditorMutates(word string, args []string) bool {
	switch strings.ToLower(baseCommandName(word)) {
	case "sed":
		for _, a := range args {
			if a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
				return true
			}
			// A short-flag cluster: -i, -i.bak, -ni, -Ei all edit in place.
			// Long options (--expression) are excluded by the -- check.
			if len(a) > 1 && a[0] == '-' && !strings.HasPrefix(a, "--") && strings.ContainsRune(shortFlagLetters(a), 'i') {
				return true
			}
		}
	case "awk":
		// gawk edits in place only via the inplace extension: -i inplace, or
		// --include=inplace.
		for _, a := range args {
			if strings.Contains(a, "inplace") {
				return true
			}
		}
	}
	return false
}

// shortFlagLetters returns the leading letters of a short-flag cluster, stopping
// at the first non-letter so `-i.bak` yields "i" and `-n5` yields "n".
func shortFlagLetters(tok string) string {
	var b strings.Builder
	for _, r := range tok[1:] {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
			continue
		}
		break
	}
	return b.String()
}

// baseCommandName strips a directory prefix and a trailing .exe so
// /usr/bin/sed and sed.exe both resolve to sed. It mirrors the guard's own
// normalization, which has already run by the time we are called; doing it
// again keeps this function correct when called directly.
func baseCommandName(tok string) string {
	if i := strings.LastIndexAny(tok, "/\\"); i >= 0 {
		tok = tok[i+1:]
	}
	return strings.TrimSuffix(tok, ".exe")
}

// WarningFor returns the model-facing guidance for an aracne tool key, or ""
// when the key has no MCP equivalent (e.g. "bash") or is unknown.
func WarningFor(key string, nativeReadAvailable bool) string {
	if key == ReadToolName {
		return readWarning(nativeReadAvailable)
	}
	if w, ok := nativeWarnings[key]; ok {
		return w
	}
	return sharedShellWarnings[key]
}

// shellServedTools are the capabilities that stopped being MCP tools when the modes were
// unscrambled: in ModeMCP the shell forms of all three are intercepted and answered by aracne,
// so registering a tool for them served the same question twice and cost a schema block per
// request to let the model pick.
//
// They stay in the catalog, and stay valid in a config's mcp_tools, so an existing config that
// names one is not a validation error -- it is simply not registered. Config.ServableMCPTools
// is the filter every consumer goes through.
var shellServedTools = map[string]bool{"grep": true, "edit": true, "write": true}

// IsShellServedTool reports whether a capability is served by shell interception rather than by
// an MCP tool.
func IsShellServedTool(name string) bool { return shellServedTools[strings.TrimSpace(name)] }

// Surface is the mode's guidance table, named here rather than in helper so this package can
// pick a warning without importing the config it would then be imported by.
type Surface int

const (
	// SurfaceMCP names `mcp__aracne__*` tools, which exist only in that mode.
	SurfaceMCP Surface = iota
	// SurfaceAracneRead names the `arac` subcommands, the only aracne surface that mode has.
	SurfaceAracneRead
	// SurfaceInterceptID names the shell commands aracne answers and the ids they accept.
	SurfaceInterceptID
	// SurfaceLineRange names the same commands, addressed by span.
	SurfaceLineRange
)

// WarningForSurface picks the guidance for the mode the project is actually in. Naming an
// `mcp__aracne__*` tool to an agent that has no MCP server is the one failure mode guaranteed
// to cost a turn, and it is invisible until the model tries the call -- and pointing at an
// intercepted `cat` in a mode that does not intercept reads is the same mistake spelled the
// other way.
func WarningForSurface(key string, nativeReadAvailable bool, surface Surface) string {
	switch surface {
	case SurfaceMCP:
		return WarningFor(key, nativeReadAvailable)
	case SurfaceAracneRead:
		return aracneReadWarnings[key]
	case SurfaceInterceptID:
		return interceptIDWarnings[key]
	default:
		return lineRangeWarnings[key]
	}
}

// Validates a list of tool names against an allowed set, returning an error for any unknown tools.
func validate(names []string, ok func(string) bool, kind string) error {
	var unknown []string
	for _, n := range names {
		if !ok(strings.TrimSpace(n)) {
			unknown = append(unknown, n)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("unknown %s tool(s): %s", kind, strings.Join(unknown, ", "))
}

// ValidateMCPTools returns an error naming any entry that is not a known MCP tool.
func ValidateMCPTools(names []string) error { return validate(names, IsMCPTool, "MCP") }

// ValidateChatTools returns an error naming any entry that is not a known chat tool.
func ValidateChatTools(names []string) error { return validate(names, IsChatTool, "chat") }

// ValidateNativeTools returns an error naming any entry that is not a blockable native tool.
func ValidateNativeTools(names []string) error {
	return validate(names, IsNativeTool, "native (blocked)")
}

// ToolsSection renders the "## Tools" markdown listing for an agent's tools,
// preserving the given order. Unknown names are listed without a description.
//
// `names` are CATALOG names (descriptions are keyed by those), but each line is rendered
// under the tool's RUNTIME name so the prose names the tool the agent actually has --
// telling an agent to call `read` when its tool is `read_resource` costs a wasted turn.
func ToolsSection(names []string, nativeReadAvailable bool) string {
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Tools\n")
	for _, n := range names {
		desc := Description(n)
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "- `%s` -- %s\n", ResolveToolName(n, nativeReadAvailable), desc)
	}
	return b.String()
}
