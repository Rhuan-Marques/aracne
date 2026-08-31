// Package toolspec is the single source of truth for the tools that can be
// referenced by name in .aracne/config.json. Every tool has a canonical name
// (the value selected in config) and a short description used to render the
// "## Tools" listing in generated agent markdown. Config tool names are
// validated against this catalog so a typo errors out at init time.
package toolspec

import (
	"fmt"
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
var shellCmdToKey = map[string]string{
	"cat": "read", "head": "read", "tail": "read", "less": "read",
	"grep": "grep", "rg": "grep",
	"sed": "edit", "awk": "edit",
}

// powershellCmdToKey maps PowerShell cmdlets to aracne tool keys (lowercased).
// Only unambiguous full cmdlet names are listed; the short aliases gc/sls are
// intentionally omitted to avoid colliding with unrelated user aliases.
// streamEditors are the commands that mutate a file only under some flags. They
// map to `edit` in shellCmdToKey, but that is the pessimistic reading: `sed` and
// `awk` are stream editors, and unless they are told to write in place they only
// read and filter, exactly like `head`. ShellCommandKeyForArgs decides which.
var streamEditors = map[string]bool{"sed": true, "awk": true}

var powershellCmdToKey = map[string]string{
	"get-content":   "read",
	"select-string": "grep",
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
var nativeWarnings = map[string]string{
	"grep":  "aracne: `mcp__aracne__grep` also searches node names and descriptions, which no plain grep can reach.",
	"edit":  "aracne: `mcp__aracne__edit` updates the topology inline (the update-file hook covers this one too).",
	"write": "aracne: `mcp__aracne__write` updates the topology inline.",
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
	name := baseCommandName(word)
	key, ok := ShellCommandKey(name)
	if !ok {
		return "", false
	}
	if key != "edit" || !streamEditors[strings.ToLower(name)] {
		return key, true
	}
	if redirectsOut || streamEditorMutates(word, args) {
		return "edit", true
	}
	return "read", true
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
	return nativeWarnings[key]
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
func ToolsSection(names []string) string {
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
		fmt.Fprintf(&b, "- `%s` -- %s\n", n, desc)
	}
	return b.String()
}
