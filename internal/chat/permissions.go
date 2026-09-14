package chat

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/toolspec"
)

type permissionAction string

const (
	permissionAllow permissionAction = "allow"
	permissionAsk   permissionAction = "ask"
	permissionDeny  permissionAction = "deny"
)

// Represents a permission decision with an action and reason.
type permissionDecision struct {
	Action permissionAction
	Reason string
}

// Enforces workspace-scoped permission policies for chat operations.
type PermissionPolicy struct {
	workspace string
}

// Creates a permission policy enforcer scoped to an absolute workspace path.
func NewPermissionPolicy(workspace string) PermissionPolicy {
	abs, err := filepath.Abs(workspace)
	if err == nil {
		workspace = abs
	}
	return PermissionPolicy{workspace: filepath.Clean(workspace)}
}

// Determines whether to allow, deny, or ask approval for a tool call based on workspace scope and mode.
//
// DENIALS ARE EVALUATED BEFORE ESCALATIONS, and the order is the rule rather than an accident.
// The Plan Mode test used to sit BELOW the workspace-scope test, and the scope test returns
// `ask` -- so a mutation aimed outside the workspace returned `ask` and never reached the deny
// below it. That inverted the safety gradient exactly backwards: `rm -rf build` inside the
// project was refused outright, while `rm -rf /etc/nginx` was merely put to the user for
// approval, in the one mode a user selects to be certain nothing runs.
//
// The general form, worth preserving in any future branch added here: no branch may return an
// action weaker than one a later branch would have returned. Deny first, then ask, then allow.
func (p PermissionPolicy) Decide(toolName string, args string, mode Mode, approvalMode ApprovalMode) permissionDecision {
	if toolName == "ask_user_question" {
		return permissionDecision{Action: permissionAllow, Reason: "user question"}
	}
	if mode == ModePlan && (isMutatingTool(toolName) || toolName == "bash") {
		return permissionDecision{Action: permissionDeny, Reason: "tool is not allowed in Plan Mode"}
	}
	if outside, path := p.referencesOutsideWorkspace(args); outside {
		return permissionDecision{Action: permissionAsk, Reason: "access outside workspace scope: " + path}
	}
	if isReadOnlyTool(toolName) {
		return permissionDecision{Action: permissionAllow, Reason: "read-only in workspace"}
	}
	if approvalMode == ApprovalAlways {
		return permissionDecision{Action: permissionAllow, Reason: "approval mode allows tool calls"}
	}
	if approvalMode == ApprovalAuto {
		return permissionDecision{Action: permissionAsk, Reason: "auto approval requires judge"}
	}
	return permissionDecision{Action: permissionAsk, Reason: "tool requires approval"}
}

// Inspects tool arguments for file paths that reference outside the workspace scope.
//
// `ids` is in the key list because the read tool's parameter is called that and nothing else
// looked at it. isReadOnlyTool then classified `read`/`read_resource` as read-only and returned
// allow with the reason "read-only IN WORKSPACE" -- a claim nothing had checked. An absolute
// path that is not a resource id falls through unitFor to rawFileUnit, which reads it off disk,
// so `{"ids":["/etc/passwd"]}` was served without approval in every approval mode.
//
// Its values are a LIST and are only path-shaped some of the time: `internal/cli.RunGuard` is a
// resource id, not a file, and rejecting it would break the tool's ordinary use. Only tokens
// that look like paths are judged -- see pathShapedToken.
func (p PermissionPolicy) referencesOutsideWorkspace(args string) (bool, string) {
	if strings.TrimSpace(args) == "" {
		return false, ""
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(args), &values); err != nil {
		return false, ""
	}
	for _, key := range []string{"file_path", "path", "workdir", "ids"} {
		for _, value := range stringValues(values[key]) {
			if strings.TrimSpace(value) == "" {
				continue
			}
			// Only reached for `ids`, whose values may be resource ids rather than
			// paths; the three explicit path keys are always paths and are judged
			// whether they are absolute or not.
			if key == "ids" && !pathShapedToken(value) {
				continue
			}
			if !p.insideWorkspace(value) {
				return true, value
			}
		}
	}
	// A bash command names its paths in `command`, and nothing looked there -- so the whole
	// workspace scope was one `bash` call away from bypassed: `cat /etc/passwd` and
	// `rm -rf ~` raised no scope question at all and fell straight through to the approval
	// mode. commandPaths is the guard's own extractor, reused so "does this touch the
	// project" has one definition rather than two that drift.
	if command, ok := values["command"].(string); ok && strings.TrimSpace(command) != "" {
		for _, path := range toolspec.CommandPaths(command) {
			// Relative paths are resolved against the workspace, so they are inside it by
			// construction; an ABSOLUTE path outside it is the case worth asking about.
			if filepath.IsAbs(path) && !p.insideWorkspace(path) {
				return true, path
			}
		}
	}
	return false, ""
}

// stringValues reads an argument that may be a single string or a list of them, which is the
// difference between `file_path` and the read tool's `ids`. Anything else yields nothing.
func stringValues(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// pathShapedToken reports whether a token is worth judging against the workspace boundary.
//
// The read tool takes resource ids and file paths in one argument, and they are not the same
// kind of thing: `internal/cli.RunGuard` names a declaration and lives nowhere in particular,
// while `/etc/passwd` names a file. Only an absolute path is judged -- a relative one resolves
// against the workspace and is inside it by construction, which is the same rule the `command`
// branch below applies.
func pathShapedToken(tok string) bool {
	// Absolute AS THE CALLER WROTE IT. filepath.IsAbs is false on Windows for `/etc/passwd`
	// -- rooted, but carrying no volume -- so an id like that was not even judged path-shaped
	// there, and the workspace-scope check this feeds never saw it.
	return toolspec.ShellPathIsAbs(tok)
}

// Checks whether a file path is contained within the configured workspace directory.
func (p PermissionPolicy) insideWorkspace(path string) bool {
	if path == "." {
		return true
	}
	// ONE DIALECT ON BOTH SIDES. The workspace comes from this process and the path comes from
	// the caller, so on Windows they arrive spelled differently -- filepath.Rel then answers
	// with an error rather than a relationship, and `/etc/passwd` was read as a RELATIVE path
	// and joined onto the workspace, which is how a read outside it was auto-allowed. Judging
	// both in the shell's spelling settles it either way: a POSIX path is inside a POSIX
	// workspace and outside a Windows one, which is exactly what each means.
	workspace := p.workspace
	if !toolspec.ShellPathIsAbs(workspace) {
		if abs, err := filepath.Abs(workspace); err == nil {
			workspace = abs
		}
	}
	if !toolspec.ShellPathIsAbs(path) {
		path = toolspec.ShellPathJoin(workspace, path)
	}
	return toolspec.ShellPathUnder(path, workspace)
}

// Checks if a tool name is read-only (safe for unprivileged access).
func isReadOnlyTool(name string) bool {
	switch name {
	case "read", "read_resource", "grep", "glob", "ls", "warnings_list", "bug_list", "node_list_no_description":
		return true
	default:
		return false
	}
}

// Returns true if the tool name is a mutation operation (edit, write, update_description, bug_report, etc.).
func isMutatingTool(name string) bool {
	switch name {
	case "edit", "write", "update_description", "bug_report", "bug_acknowledge", "bug_dismiss", "bug_delete", "CreateTasks":
		return true
	default:
		return false
	}
}
