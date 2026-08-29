package chat

import (
	"encoding/json"
	"path/filepath"
	"strings"
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
func (p PermissionPolicy) Decide(toolName string, args string, mode Mode, approvalMode ApprovalMode) permissionDecision {
	if toolName == "ask_user_question" {
		return permissionDecision{Action: permissionAllow, Reason: "user question"}
	}
	if outside, path := p.referencesOutsideWorkspace(args); outside {
		return permissionDecision{Action: permissionAsk, Reason: "access outside workspace scope: " + path}
	}
	if mode == ModePlan && (isMutatingTool(toolName) || toolName == "bash") {
		return permissionDecision{Action: permissionDeny, Reason: "tool is not allowed in Plan Mode"}
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
func (p PermissionPolicy) referencesOutsideWorkspace(args string) (bool, string) {
	if strings.TrimSpace(args) == "" {
		return false, ""
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(args), &values); err != nil {
		return false, ""
	}
	for _, key := range []string{"file_path", "path", "workdir"} {
		value, ok := values[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		if !p.insideWorkspace(value) {
			return true, value
		}
	}
	return false, ""
}

// Checks whether a file path is contained within the configured workspace directory.
func (p PermissionPolicy) insideWorkspace(path string) bool {
	if path == "." {
		return true
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.workspace, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(p.workspace, abs)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
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
