package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"ltp/internal/helper"
	"ltp/internal/topology/domain"
)

var resourceIDLineRE = regexp.MustCompile(`(?m)^\s*(?:ResourceID|ID):\s*(\S+)`)
var bulletIDLineRE = regexp.MustCompile(`(?m)^\s*-\s+(\S+)\s*$`)

type routeContext struct {
	sessionID string
	platform  string
	label     string
}

func (s *Server) recordAgentRouteForTool(toolName string, args json.RawMessage, result string) {
	ctx, ok := s.agentRouteContext()
	if !ok {
		return
	}
	fullCut, descriptions := s.routeResourcesForTool(toolName, args, result)
	if len(descriptions) > 0 {
		_ = helper.RecordAgentRouteAccess(s.dbPath, ctx.sessionID, ctx.platform, ctx.label, helper.AgentRouteAccessDescription, descriptions, s.routeTTL)
	}
	if len(fullCut) > 0 {
		_ = helper.RecordAgentRouteAccess(s.dbPath, ctx.sessionID, ctx.platform, ctx.label, helper.AgentRouteAccessFullCut, fullCut, s.routeTTL)
	}
}

func (s *Server) agentRouteContext() (routeContext, bool) {
	if strings.TrimSpace(s.dbPath) == "" {
		return routeContext{}, false
	}
	ctx := routeContext{
		sessionID: firstEnv("LTP_AGENT_ROUTE_SESSION", "CLAUDE_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "OPENCODE_SESSION_ID", "OPENCODE_SESSION"),
		platform:  firstEnv("LTP_AGENT_ROUTE_PLATFORM"),
		label:     firstEnv("LTP_AGENT_ROUTE_LABEL"),
	}
	if ctx.sessionID == "" {
		return routeContext{}, false
	}
	if ctx.platform == "" {
		switch {
		case os.Getenv("CLAUDE_SESSION_ID") != "" || os.Getenv("CLAUDE_CODE_SESSION_ID") != "":
			ctx.platform = "claude"
		case os.Getenv("OPENCODE_SESSION_ID") != "" || os.Getenv("OPENCODE_SESSION") != "":
			ctx.platform = "opencode"
		default:
			ctx.platform = "unknown"
		}
	}
	if ctx.label == "" {
		ctx.label = ctx.platform + ":" + ctx.sessionID
	}
	return ctx, true
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func (s *Server) routeResourcesForTool(toolName string, args json.RawMessage, result string) ([]string, []string) {
	toolName = strings.TrimSpace(toolName)
	switch toolName {
	case "read":
		if id := stringArg(args, "resource_id"); id != "" {
			return []string{id}, nil
		}
	case "read_function":
		return s.resolveNamedRead(args, domain.ResourceFunction, domain.ResourceMethod), ambiguousIDs(result)
	case "read_struct":
		return s.resolveNamedRead(args, domain.ResourceType), ambiguousIDs(result)
	case "read_interface":
		return s.resolveNamedRead(args, domain.ResourceInterface), ambiguousIDs(result)
	case "read_named_type":
		return s.resolveNamedRead(args, domain.ResourceNamedType), ambiguousIDs(result)
	case "read_file":
		return s.resolveNamedRead(args, domain.ResourceFile), ambiguousIDs(result)
	case "read_package":
		return s.resolveNamedRead(args, domain.ResourcePackage), ambiguousIDs(result)
	case "read_dependency":
		return s.resolveNamedRead(args, domain.ResourceDependency), ambiguousIDs(result)
	case "grep", "node_list_no_description":
		return nil, idsFromResult(result)
	}
	return nil, nil
}

func (s *Server) resolveNamedRead(args json.RawMessage, kinds ...domain.ResourceKind) []string {
	name := stringArg(args, "name")
	if name == "" {
		return nil
	}
	topo, err := helper.ReadDb(s.dbPath)
	if err != nil {
		return nil
	}
	kindSet := make(map[domain.ResourceKind]bool, len(kinds))
	for _, kind := range kinds {
		kindSet[kind] = true
	}
	matches := make([]string, 0, 1)
	for id, res := range topo.Resources {
		if !kindSet[res.Kind] {
			continue
		}
		if id == name || res.Name == name || filepath.Base(res.Location.Path) == name {
			matches = append(matches, id)
		}
	}
	sort.Strings(matches)
	if len(matches) == 1 {
		return matches
	}
	return nil
}

func stringArg(args json.RawMessage, key string) string {
	var values map[string]any
	if err := json.Unmarshal(args, &values); err != nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func idsFromResult(result string) []string {
	seen := make(map[string]bool)
	ids := make([]string, 0)
	for _, match := range resourceIDLineRE.FindAllStringSubmatch(result, -1) {
		id := strings.TrimSpace(match[1])
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func ambiguousIDs(result string) []string {
	if !strings.Contains(result, "Multiple ") {
		return nil
	}
	seen := make(map[string]bool)
	ids := make([]string, 0)
	for _, match := range bulletIDLineRE.FindAllStringSubmatch(result, -1) {
		id := strings.TrimSpace(match[1])
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
