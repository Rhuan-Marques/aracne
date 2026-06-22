package viz

import (
	"errors"
	"net/http"
	"strings"

	"aracne/internal/chat"
	"aracne/internal/topology/domain"
)

// Context-graph endpoint.
//
// This serves the miniature "what the LLM is aware of" Data Flow graph shown in
// the chat side panel. The set of resources the model has "seen" is derived,
// on demand, from the chat session's already-persisted tool messages, so the
// shared topology-read tools and the MCP path are never touched and external
// harnesses are unaffected.
//
// A node is green when the model saw the resource's code, yellow when it only
// saw its name/description. Only Data Flow kinds (function/method/type/
// interface) ever appear, and the user's favorite Node-Optimization rules are
// applied — matching the main Data Flow visualization.

const (
	ctxGreen  = "green"
	ctxYellow = "yellow"
)

// contextGraphNode is a graph node annotated with its context state.
type contextGraphNode struct {
	GraphNode
	ContextState string `json:"context_state"`
}

// Response payload containing graph nodes and edges for context visualization.
type contextGraphResponse struct {
	Nodes []contextGraphNode `json:"nodes"`
	Edges []GraphEdge        `json:"edges"`
}

// HTTP handler that returns a filtered context graph for a chat session, showing resources seen by the model with optimization rules applied.
func (s *Server) handleContextGraph(w http.ResponseWriter, r *http.Request) {
	idx, err := s.loadIndex()
	if err != nil {
		writeError(w, err)
		return
	}
	mgr, err := s.chatManager()
	if err != nil {
		writeError(w, err)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		writeError(w, errors.New("missing session_id"))
		return
	}
	session, err := mgr.GetSession(sessionID)
	if err != nil {
		writeError(w, err)
		return
	}

	messages := session.Messages
	groupID := strings.TrimSpace(r.URL.Query().Get("group_id"))
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	if groupID != "" && taskID != "" {
		messages = taskMessages(session, groupID, taskID)
	}

	seen := deriveSeenResources(idx, mgr.ContextFilter(), messages)

	selected := make(map[string]bool, len(seen))
	nodes := make([]GraphNode, 0, len(seen))
	for id := range seen {
		selected[id] = true
		nodes = append(nodes, idx.nodeDTO(id))
	}
	graph := GraphResponse{Nodes: nodes, Edges: idx.edgesWithin(selected, dataFlowEdges()), TotalMatch: len(nodes)}

	rules, err := s.readOptimizationRules()
	if err != nil {
		writeError(w, err)
		return
	}
	graph = idx.optimizedGraph(graph, favoriteRules(rules))

	out := contextGraphResponse{Edges: graph.Edges, Nodes: make([]contextGraphNode, 0, len(graph.Nodes))}
	for _, node := range graph.Nodes {
		state := seen[node.ID]
		if state == "" {
			state = ctxYellow
		}
		// A surviving node that absorbed a collapsed child the model saw as
		// code is itself green.
		if state != ctxGreen {
			for _, inc := range node.Includes {
				if seen[inc.ID] == ctxGreen {
					state = ctxGreen
					break
				}
			}
		}
		out.Nodes = append(out.Nodes, contextGraphNode{GraphNode: node, ContextState: state})
	}
	writeJSON(w, out)
}

// taskMessages returns the message list for a single sub-agent task, so a task
// chat shows only its own context (never the parent chat's reads, and vice
// versa).
func taskMessages(session *chat.Session, groupID, taskID string) []chat.SessionMessage {
	for _, group := range session.TaskGroups {
		if group.ID != groupID {
			continue
		}
		for _, task := range group.Tasks {
			if task.ID == taskID {
				return task.Messages
			}
		}
	}
	return nil
}

// deriveSeenResources scans completed topology-read tool messages and returns a
// map of resourceID -> context state ("green"|"yellow"). Green always wins:
// once the model has seen a resource's code it stays green. Only Data Flow
// kinds are tracked.
func deriveSeenResources(idx *graphIndex, filter domain.ContextFilter, messages []chat.SessionMessage) map[string]string {
	seen := make(map[string]string)
	mark := func(id, state string) {
		if id == "" {
			return
		}
		res, ok := idx.topo.Resources[id]
		if !ok || !dataFlowKinds()[string(res.Kind)] {
			return
		}
		if seen[id] == ctxGreen {
			return
		}
		if state == ctxGreen {
			seen[id] = ctxGreen
			return
		}
		if seen[id] == "" {
			seen[id] = ctxYellow
		}
	}

	for _, msg := range messages {
		if msg.Role != "tool" || msg.Status != "completed" {
			continue
		}
		switch msg.ToolName {
		case "read_function", "read_struct", "read_interface", "read_named_type":
			id := idx.resolveResourceName(toolStringArg(msg.ToolInput, "name"), readToolKinds(msg.ToolName))
			if id == "" {
				continue
			}
			mark(id, ctxGreen)
			markNeighbors(idx, filter, id, mark)
		case "read_file":
			for _, id := range idx.resourcesInFile(toolStringArg(msg.ToolInput, "name")) {
				mark(id, ctxGreen)
				markNeighbors(idx, filter, id, mark)
			}
		case "grep":
			for _, id := range parseGrepResourceIDs(msg.ToolOutput) {
				mark(id, ctxYellow)
			}
		}
	}
	return seen
}

// markNeighbors visits the Data Flow neighbors of a read resource and marks
// each one green (full code shown) or yellow (description only) according to
// the read context filter — the same decision the read tools make.
func markNeighbors(idx *graphIndex, filter domain.ContextFilter, id string, mark func(string, string)) {
	consider := func(neighborID, edgeType string) {
		if !dataFlowEdges()[strings.ToLower(edgeType)] {
			return
		}
		res, ok := idx.topo.Resources[neighborID]
		if !ok {
			return
		}
		vis := filter.For(res.Kind, lineSpan(res.Location), strings.TrimSpace(res.Description) != "")
		switch vis {
		case domain.VisibilityHidden:
			return
		case domain.VisibilityFull:
			mark(neighborID, ctxGreen)
		default:
			mark(neighborID, ctxYellow)
		}
	}
	for _, edge := range idx.outgoing[id] {
		consider(edge.Target, edge.Type)
	}
	if filter.IncludeIncoming {
		for _, edge := range idx.incoming[id] {
			consider(edge.Source, edge.Type)
		}
	}
}

// readToolKinds maps a read tool to the resource kinds it can resolve, used to
// disambiguate a bare name into a single resource ID.
func readToolKinds(tool string) map[string]bool {
	switch tool {
	case "read_function":
		return map[string]bool{"function": true, "method": true}
	case "read_struct":
		return map[string]bool{"type": true}
	case "read_interface":
		return map[string]bool{"interface": true}
	case "read_named_type":
		return map[string]bool{"named_type": true}
	default:
		return nil
	}
}

// resolveResourceName resolves a read tool's "name" argument to a resource ID.
// Read tools are documented to take a resource ID, so an exact match is the
// common path; a unique name match among the allowed kinds is the fallback.
// Ambiguous or unknown names resolve to "" (the read tool would have returned a
// disambiguation list rather than content).
func (idx *graphIndex) resolveResourceName(name string, kinds map[string]bool) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if _, ok := idx.topo.Resources[name]; ok {
		return name
	}
	match := ""
	count := 0
	for id, res := range idx.topo.Resources {
		if kinds != nil && !kinds[string(res.Kind)] {
			continue
		}
		if res.Name == name {
			match = id
			count++
		}
	}
	if count == 1 {
		return match
	}
	return ""
}

// resourcesInFile returns the Data Flow resources defined in the given file.
// The argument may be absolute or workspace-relative; both forms are matched.
func (idx *graphIndex) resourcesInFile(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	cleaned := strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "./")
	var ids []string
	for id, res := range idx.topo.Resources {
		if !dataFlowKinds()[string(res.Kind)] {
			continue
		}
		abs := res.Location.Path
		rel := relativePath(idx.topo.Root, abs)
		if rel == cleaned || abs == name || strings.HasSuffix(abs, "/"+cleaned) || strings.HasSuffix(rel, "/"+cleaned) {
			ids = append(ids, id)
		}
	}
	return ids
}

// parseGrepResourceIDs extracts the topology resource IDs reported by the grep
// tool. grep surfaces matches as names/descriptions only, so they are yellow.
func parseGrepResourceIDs(output string) []string {
	var ids []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		const marker = "ResourceID:"
		if !strings.HasPrefix(trimmed, marker) {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(trimmed, marker))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// Filters optimization rules to return only those marked as favorites.
func favoriteRules(rules []OptimizationRule) []OptimizationRule {
	out := make([]OptimizationRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Favorite {
			out = append(out, rule)
		}
	}
	return out
}

// toolStringArg reads a string argument from a decoded tool-input map.
func toolStringArg(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	if v, ok := input[key].(string); ok {
		return v
	}
	return ""
}

// lineSpan reports the inclusive source line span of a location, or 0 when
// unknown.
func lineSpan(loc domain.Location) int {
	if loc.EndsAt <= 0 || loc.EndsAt < loc.StartsAt {
		return 0
	}
	return loc.EndsAt - loc.StartsAt + 1
}
