package viz

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"ltp/internal/helper"
)

func (s *Server) agentRouteTTL() time.Duration {
	cfg := helper.EnsureConfig(helper.ConfigPath(s.dbPath))
	return cfg.EffectiveAgentRouteTTL()
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	s.ws.ServeHTTP(w, r)
}

func (s *Server) handleAgentRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := helper.ReadAgentRoutes(s.dbPath, s.agentRouteTTL())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, routes)
}

func (s *Server) ensureRouteWatcher() {
	s.watcherOnce.Do(func() {
		go s.watchAgentRoutes()
	})
}

func (s *Server) watchAgentRoutes() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	lastVersion := ""
	for range ticker.C {
		version, err := helper.AgentRoutesVersion(s.dbPath, s.agentRouteTTL())
		if err != nil || version == lastVersion {
			continue
		}
		lastVersion = version
		s.ws.Broadcast(WebSocketEvent{Type: "agent_routes_changed", Payload: map[string]string{"version": version}})
	}
}

func (s *Server) agentRouteGraph(idx *graphIndex, sessionID, query string, kindSet map[string]bool, path string, edgeSet map[string]bool, limit int) (GraphResponse, error) {
	routeResources, err := helper.ReadAgentRouteResources(s.dbPath, sessionID, s.agentRouteTTL())
	if err != nil {
		return GraphResponse{}, err
	}
	if len(routeResources) == 0 {
		return GraphResponse{Nodes: []GraphNode{}, Edges: []GraphEdge{}, Limit: limit}, nil
	}
	allowed := idSet(idx.filteredIDs(query, kindSet, path))
	ids := make([]string, 0, len(routeResources))
	for id := range routeResources {
		if _, ok := idx.topo.Resources[id]; !ok {
			continue
		}
		if allowed[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	total := len(ids)
	truncated := total > limit
	if truncated {
		ids = ids[:limit]
	}
	selected := idSet(ids)
	nodes := make([]GraphNode, 0, len(ids))
	for _, id := range ids {
		node := idx.nodeDTO(id)
		node.AgentRouteAccess = normalizeRouteAccessForGraph(routeResources[id].AccessKind)
		nodes = append(nodes, node)
	}
	edges := idx.edgesWithin(selected, edgeSet)
	return GraphResponse{Nodes: nodes, Edges: edges, Truncated: truncated, Limit: limit, TotalMatch: total}, nil
}

func normalizeRouteAccessForGraph(access string) string {
	switch strings.ToLower(strings.TrimSpace(access)) {
	case helper.AgentRouteAccessFullCut:
		return helper.AgentRouteAccessFullCut
	case helper.AgentRouteAccessDescription:
		return helper.AgentRouteAccessDescription
	default:
		return ""
	}
}

func requireAgentRouteSession(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("missing agent_route")
	}
	return sessionID, nil
}
