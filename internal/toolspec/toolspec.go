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
	{"read", "read any resource by its ID", true, true},
	{"read_function", "inspect a function's source and connected context", true, true},
	{"read_struct", "inspect a struct/class's source, methods, and interfaces", true, true},
	{"read_interface", "inspect an interface/protocol and its implementations", true, true},
	{"read_named_type", "inspect a named type and its usages", true, true},
	{"read_file", "inspect a file's source and topology context, or a raw line range via start_line/end_line", true, true},
	{"read_package", "inspect a package and its members", true, true},
	{"read_dependency", "inspect a dependency and its usages", true, true},
	{"grep", "search code contents, returning topology resource metadata", true, true},
	{"edit", "apply exact string replacements to a file", true, true},
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
var powershellCmdToKey = map[string]string{
	"get-content":   "read",
	"select-string": "grep",
}

// nativeWarnings is the model-facing guidance shown when a native tool or its
// shell equivalent is used. Keyed by aracne tool key; bash has no MCP
// equivalent and so has no warning.
var nativeWarnings = map[string]string{
	"read":  "aracne: use the `mcp__aracne__read_file`/`mcp__aracne__read_function` MCP tools to read code instead of the native Read tool or shell `cat`/`head`/`tail`/`less`/`Get-Content`.",
	"grep":  "aracne: use the `mcp__aracne__grep` MCP tool for content search instead of the native Grep tool or shell `grep`/`rg`/`Select-String`.",
	"edit":  "aracne: use the `mcp__aracne__edit` MCP tool to modify files instead of the native Edit tool or shell `sed`/`awk`.",
	"write": "aracne: use the `mcp__aracne__write` MCP tool to create files instead of the native Write tool.",
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

// WarningFor returns the model-facing guidance for an aracne tool key, or ""
// when the key has no MCP equivalent (e.g. "bash") or is unknown.
func WarningFor(key string) string { return nativeWarnings[key] }

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
