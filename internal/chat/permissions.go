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

type permissionDecision struct {
	Action permissionAction
	Reason string
}

type PermissionPolicy struct {
	workspace string
}

func NewPermissionPolicy(workspace string) PermissionPolicy {
	abs, err := filepath.Abs(workspace)
	if err == nil {
		workspace = abs
	}
	return PermissionPolicy{workspace: filepath.Clean(workspace)}
}

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

func isReadOnlyTool(name string) bool {
	switch name {
	case "read", "read_function", "read_struct", "read_interface", "read_named_type", "read_file", "read_package", "read_dependency", "grep", "glob", "ls", "warnings_list", "bug_list", "node_list_no_description":
		return true
	default:
		return false
	}
}

func isMutatingTool(name string) bool {
	switch name {
	case "edit", "write", "update_description", "bug_report", "bug_acknowledge", "bug_dismiss", "bug_delete":
		return true
	default:
		return false
	}
}
