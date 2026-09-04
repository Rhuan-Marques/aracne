package viz

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/chat"
	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// HTTP server that manages the visualization database, WebSocket connections, and chat initialization.
type Server struct {
	dbPath   string
	ws       *WebSocketManager
	chatMu   sync.Mutex
	chatInit bool
	chatMgr  *chat.Manager
	chatErr  error
}

// Snapshot of topology statistics including node/edge counts, language, warnings, bugs, and kind/type distributions.
type Summary struct {
	Root         string         `json:"root"`
	Language     string         `json:"language"`
	Languages    []string       `json:"languages"`
	NodeCount    int            `json:"node_count"`
	EdgeCount    int            `json:"edge_count"`
	WarningCount int            `json:"warning_count"`
	BugCount     int            `json:"bug_count"`
	Kinds        map[string]int `json:"kinds"`
	EdgeTypes    map[string]int `json:"edge_types"`
	GeneratedAt  time.Time      `json:"generated_at"`
}

// Represents a codebase resource node with metadata, location info, and topology metrics for visualization.
type GraphNode struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Kind         string              `json:"kind"`
	Language     string              `json:"language"`
	Path         string              `json:"path"`
	StartsAt     int                 `json:"starts_at"`
	EndsAt       int                 `json:"ends_at"`
	Description  string              `json:"description,omitempty"`
	Properties   map[string]any      `json:"properties,omitempty"`
	InDegree     int                 `json:"in_degree"`
	OutDegree    int                 `json:"out_degree"`
	WarningCount int                 `json:"warning_count"`
	BugCount     int                 `json:"bug_count"`
	Includes     []CollapsedResource `json:"includes,omitempty"`
}

// JSON-serializable representation of a collapsed topology resource with id, name, kind, and optional description.
type CollapsedResource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
}

// A named visualization optimization rule with active/favorite flags and a sequence of operations to apply.
type OptimizationRule struct {
	ID         string                  `json:"id"`
	Active     bool                    `json:"active"`
	Favorite   bool                    `json:"favorite"`
	Operations []OptimizationOperation `json:"operations"`
}

// A single operation within an optimization rule, specifying a kind and optional value.
type OptimizationOperation struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

// JSON-serializable graph edge with source/target ids, edge type, and source/target resource kinds.
type GraphEdge struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Type       string `json:"type"`
	SourceKind string `json:"source_kind"`
	TargetKind string `json:"target_kind"`
}

// Graph query response containing nodes, edges, and pagination info for topology visualization.
type GraphResponse struct {
	Nodes      []GraphNode `json:"nodes"`
	Edges      []GraphEdge `json:"edges"`
	Truncated  bool        `json:"truncated"`
	Limit      int         `json:"limit"`
	TotalMatch int         `json:"total_match"`
}

// Indexes a topology graph with incoming/outgoing edges and degree metrics for fast traversal and warning/bug counting.
type graphIndex struct {
	topo          *domain.Topology
	bugs          []domain.KnownBug
	incoming      map[string][]GraphEdge
	outgoing      map[string][]GraphEdge
	inDegree      map[string]int
	outDegree     map[string]int
	warningCounts map[string]int
	bugCounts     map[string]int
}

// Creates a new HTTP server handler for topology visualization with WebSocket support.
func NewServer(dbPath string) *Server {
	return &Server{dbPath: dbPath, ws: NewWebSocketManager()}
}

// bugManagementEnabled reports whether this project has the bug pipeline turned on.
//
// Read per call rather than cached at construction: the viz process is long-lived and
// handleConfig can rewrite .aracne/config.json underneath it, so a cached flag would go
// stale for the rest of the session. LoadConfig falls back to defaults (feature off) when
// the file is missing or unparseable.
func (s *Server) bugManagementEnabled() bool {
	return helper.LoadConfig(helper.ConfigPath(s.dbPath)).BugManagementEnabled()
}

// Starts an HTTP server for topology visualization on the specified address and database path.
func Listen(addr, dbPath string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           NewServer(dbPath),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Topology visualization listening on http://%s (db: %s)", addr, dbPath)
	return srv.ListenAndServe()
}

// HTTP handler that routes API and static requests, enforcing cross-origin checks for /api/ endpoints.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") && !localOriginAllowed(r) {
		http.Error(w, "forbidden: cross-origin request rejected", http.StatusForbidden)
		return
	}
	mux := http.NewServeMux()
	mux.Handle("/", staticFileServer())
	mux.HandleFunc("/api/ws", s.handleWebSocket)
	mux.HandleFunc("/api/summary", s.handleSummary)
	mux.HandleFunc("/api/graph", s.handleGraph)
	mux.HandleFunc("/api/neighborhood", s.handleNeighborhood)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/node/", s.handleNode)
	mux.HandleFunc("/api/optimization-rules", s.handleOptimizationRules)
	mux.HandleFunc("/api/context-graph", s.handleContextGraph)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/chat/", s.handleChat)
	mux.HandleFunc("/api/warnings", s.handleWarnings)
	// The bug pipeline ships behind features.bug_management; with it off the endpoint is
	// absent (404 via the mux default) rather than serving an empty list, so the disabled
	// state is verifiable from outside the process.
	if s.bugManagementEnabled() {
		mux.HandleFunc("/api/bugs", s.handleBugs)
	}
	mux.ServeHTTP(w, r)
}

// Loads topology database and bugs, then builds an index with graph edges, degrees, and warning/bug counts for each resource.

func (s *Server) loadIndex() (*graphIndex, error) {
	topo, err := helper.ReadDb(s.dbPath)
	if err != nil {
		return nil, err
	}
	// With bug management off, `bugs` stays nil and `bugCounts` stays empty, so
	// Summary.BugCount, GraphNode.BugCount and nodeBugs all report zero/empty with no
	// further gating. app.js tests `bug_count > 0`, so its red node stroke never fires.
	var bugs []domain.KnownBug
	if s.bugManagementEnabled() {
		bugs, err = helper.ReadBugs(s.dbPath, "", "")
		if err != nil {
			return nil, err
		}
	}
	idx := &graphIndex{
		topo:          topo,
		bugs:          bugs,
		incoming:      make(map[string][]GraphEdge),
		outgoing:      make(map[string][]GraphEdge),
		inDegree:      make(map[string]int),
		outDegree:     make(map[string]int),
		warningCounts: make(map[string]int),
		bugCounts:     make(map[string]int),
	}
	for sourceID, res := range topo.Resources {
		for connType, targets := range res.Connections {
			for _, targetID := range targets {
				edge := GraphEdge{Source: sourceID, Target: targetID, Type: connType, SourceKind: string(res.Kind), TargetKind: idx.resourceKind(targetID)}
				idx.outgoing[sourceID] = append(idx.outgoing[sourceID], edge)
				idx.incoming[targetID] = append(idx.incoming[targetID], edge)
				idx.outDegree[sourceID]++
				idx.inDegree[targetID]++
			}
		}
	}
	for _, warning := range topo.Warnings {
		if warning.SourceID != "" {
			idx.warningCounts[warning.SourceID]++
		}
		if warning.TargetID != "" && warning.TargetID != warning.SourceID {
			idx.warningCounts[warning.TargetID]++
		}
	}
	for _, bug := range bugs {
		idx.bugCounts[bug.NodeID]++
	}
	return idx, nil
}

// HTTP handler that upgrades the connection to WebSocket and delegates to the server's WebSocket handler.

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	s.ws.ServeHTTP(w, r)
}

// HTTP handler that returns aggregate statistics about the topology including node count, warnings, bugs, and edge type distributions.

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	idx, err := s.loadIndex()
	if err != nil {
		writeError(w, err)
		return
	}
	summary := Summary{
		Root:         idx.topo.Root,
		Language:     idx.topo.Language,
		Languages:    idx.languages(),
		NodeCount:    len(idx.topo.Resources),
		WarningCount: len(idx.topo.Warnings),
		BugCount:     len(idx.bugs),
		Kinds:        make(map[string]int),
		EdgeTypes:    make(map[string]int),
		GeneratedAt:  time.Now(),
	}
	for _, res := range idx.topo.Resources {
		summary.Kinds[string(res.Kind)]++
		for connType, targets := range res.Connections {
			summary.EdgeTypes[connType] += len(targets)
			summary.EdgeCount += len(targets)
		}
	}
	writeJSON(w, summary)
}

// HTTP handler that returns a topology graph filtered by query, path, language, and resource kinds with optional optimization rules.

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	idx, err := s.loadIndex()
	if err != nil {
		writeError(w, err)
		return
	}
	rules, err := parseOptimizationRules(r.URL.Query().Get("optimization_rules"))
	if err != nil {
		writeError(w, err)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	language := normalizeLanguageFilter(r.URL.Query().Get("language"))
	limit := queryInt(r.URL.Query(), "limit", 300, 1, 3000)

	var graph GraphResponse
	switch strings.TrimSpace(r.URL.Query().Get("mode")) {
	case "packages":
		graph = idx.packageGraph(query, language, limit)
	case "data_flow":
		graph = idx.resourceGraph(query, dataFlowKinds(), path, language, dataFlowEdges(), limit, false)
	default:
		kindSet := parseSet(r.URL.Query(), "kind")
		edgeSet := parseSet(r.URL.Query(), "edge_kind")
		strictEdges := r.URL.Query().Get("strict_edges") == "true"
		graph = idx.resourceGraph(query, kindSet, path, language, edgeSet, limit, strictEdges)
	}
	writeJSON(w, idx.optimizedGraph(graph, rules))
}

// HTTP handler that returns a graph of nodes and edges around a given resource within a specified depth and direction, with optional optimization rules applied.

func (s *Server) handleNeighborhood(w http.ResponseWriter, r *http.Request) {
	idx, err := s.loadIndex()
	if err != nil {
		writeError(w, err)
		return
	}
	rules, err := parseOptimizationRules(r.URL.Query().Get("optimization_rules"))
	if err != nil {
		writeError(w, err)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, errors.New("missing id"))
		return
	}
	if _, ok := idx.topo.Resources[id]; !ok {
		writeError(w, fmt.Errorf("node not found: %s", id))
		return
	}
	depth := queryInt(r.URL.Query(), "depth", 1, 0, 4)
	direction := strings.TrimSpace(r.URL.Query().Get("direction"))
	if direction == "" {
		direction = "both"
	}
	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	kindSet := parseSet(r.URL.Query(), "kind")
	edgeSet := parseSet(r.URL.Query(), "edge_kind")
	switch mode {
	case "packages":
		kindSet = packageKinds()
		edgeSet = packageEdges()
	case "data_flow":
		kindSet = dataFlowKinds()
		edgeSet = dataFlowEdges()
	}
	limit := queryInt(r.URL.Query(), "limit", 500, 1, 3000)

	selected, truncated := idx.neighborhood(id, depth, direction, kindSet, edgeSet, limit)
	ids := setIDs(selected)
	nodes := make([]GraphNode, 0, len(ids))
	for _, nodeID := range ids {
		node := idx.nodeDTO(nodeID)
		nodes = append(nodes, node)
	}
	edges := idx.edgesWithin(selected, edgeSet)
	graph := GraphResponse{Nodes: nodes, Edges: edges, Truncated: truncated, Limit: limit, TotalMatch: len(nodes)}
	writeJSON(w, idx.optimizedGraph(graph, rules))
}

// HTTP handler that searches the topology index by query string, kind, path, and language filters, returning matching nodes.

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	idx, err := s.loadIndex()
	if err != nil {
		writeError(w, err)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	kindSet := parseSet(r.URL.Query(), "kind")
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	language := normalizeLanguageFilter(r.URL.Query().Get("language"))
	limit := queryInt(r.URL.Query(), "limit", 50, 1, 500)
	ids := idx.filteredIDs(query, kindSet, path, language)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	nodes := make([]GraphNode, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, idx.nodeDTO(id))
	}
	writeJSON(w, nodes)
}

// HTTP handler that returns detailed information for a single node including its code, edges, warnings, and known bugs.

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	idx, err := s.loadIndex()
	if err != nil {
		writeError(w, err)
		return
	}
	rules, err := parseOptimizationRules(r.URL.Query().Get("optimization_rules"))
	if err != nil {
		writeError(w, err)
		return
	}
	encodedID := strings.TrimPrefix(r.URL.Path, "/api/node/")
	id, err := url.PathUnescape(encodedID)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, ok := idx.topo.Resources[id]; !ok {
		writeError(w, fmt.Errorf("node not found: %s", id))
		return
	}
	node := idx.nodeDTO(id)
	node.Includes = idx.includesFor(id, rules)
	writeJSON(w, struct {
		Node     GraphNode                `json:"node"`
		Outgoing []GraphEdge              `json:"outgoing"`
		Incoming []GraphEdge              `json:"incoming"`
		Warnings []domain.TopologyWarning `json:"warnings"`
		Bugs     []domain.KnownBug        `json:"bugs"`
		Code     string                   `json:"code,omitempty"`
	}{
		Node:     node,
		Outgoing: idx.outgoing[id],
		Incoming: idx.incoming[id],
		Warnings: idx.nodeWarnings(id),
		Bugs:     idx.nodeBugs(id),
		Code:     idx.nodeCodeWithIncludes(id, rules),
	})
}

// HTTP handler that gets or updates configuration, including description generation kinds and batch size.

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	path := helper.ConfigPath(s.dbPath)
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, helper.EnsureConfig(path))
	case http.MethodPut:
		cfg := helper.EnsureConfig(path)
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			writeError(w, err)
			return
		}
		if data, ok := raw["descriptions"]; ok {
			var section struct {
				Kinds []domain.ResourceKind `json:"kinds"`
			}
			if err := json.Unmarshal(data, &section); err != nil {
				writeError(w, err)
				return
			}
			targets, err := helper.NormalizeDescribeTargets(section.Kinds)
			if err != nil {
				writeError(w, err)
				return
			}
			cfg.Descriptions.Kinds = targets
		}
		if data, ok := raw["description_batch_size"]; ok {
			var value int
			if err := json.Unmarshal(data, &value); err != nil {
				writeError(w, err)
				return
			}
			if value <= 0 {
				writeError(w, fmt.Errorf("description_batch_size must be greater than 0"))
				return
			}
			setExecutorBatchSize(cfg, value)
		}
		if err := helper.SaveConfig(cfg, path); err != nil {
			writeError(w, err)
			return
		}
		s.refreshChatConfig(cfg)
		writeJSON(w, cfg)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// Updates the chat manager's configuration if initialized, respecting lazy initialization synchronization.

func (s *Server) refreshChatConfig(cfg *helper.Config) {
	if cfg == nil {
		return
	}
	// Read the manager under the same lock that guards lazy initialization in
	// chatManager(). Do not initialize it here: if the chat manager has not
	// been created yet it will pick up the saved config when first used.
	s.chatMu.Lock()
	mgr := s.chatMgr
	s.chatMu.Unlock()
	if mgr == nil {
		return
	}
	mgr.SetConfig(cfg)
}

// HTTP handler that gets or updates optimization rules for graph visualization via GET or PUT requests.

func (s *Server) handleOptimizationRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rules, err := s.readOptimizationRules()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, rules)
	case http.MethodPut:
		var rules []OptimizationRule
		if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
			writeError(w, err)
			return
		}
		rules = normalizeOptimizationRules(rules)
		if err := s.writeOptimizationRules(rules); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, rules)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// setExecutorBatchSize writes the descriptions-executor max-batch-size to both
// the chat agent (used by viz workflows) and the llm <any> agent (used by the
// CLI), so the viz control affects both.
// CLI), so the viz control affects both.
func setExecutorBatchSize(cfg *helper.Config, value int) {
	const name = "descriptions-generation-executor"
	if cfg.Viz.Chat.Agents.Agents == nil {
		cfg.Viz.Chat.Agents.Agents = map[string]helper.ChatAgentConfig{}
	}
	chatAg := cfg.Viz.Chat.Agents.Agents[name]
	if chatAg.Params == nil {
		chatAg.Params = map[string]int{}
	}
	chatAg.Params["max-batch-size"] = value
	cfg.Viz.Chat.Agents.Agents[name] = chatAg

	if cfg.LLM.Any.Agents == nil {
		cfg.LLM.Any.Agents = map[string]helper.AgentConfig{}
	}
	llmAg := cfg.LLM.Any.Agents[name]
	if llmAg.Params == nil {
		llmAg.Params = map[string]int{}
	}
	llmAg.Params["max-batch-size"] = value
	cfg.LLM.Any.Agents[name] = llmAg
}

// Resolves the optimization rules file path from config, using a default or configured absolute/relative path.

func (s *Server) optimizationRulesPath() string {
	cfg := helper.LoadConfig(helper.ConfigPath(s.dbPath))
	rules := strings.TrimSpace(cfg.Viz.Graph.OptimizationRules)
	if rules == "" {
		return filepath.Join(filepath.Dir(s.dbPath), "optimization_rules.json")
	}
	if filepath.IsAbs(rules) {
		return rules
	}
	// Relative config paths are resolved against the project root (the parent
	// of the .aracne directory holding the topology db).
	return filepath.Join(filepath.Dir(filepath.Dir(s.dbPath)), rules)
}

// Reads optimization rules from JSON file, returning defaults if not found, and normalizes the loaded rules.

func (s *Server) readOptimizationRules() ([]OptimizationRule, error) {
	path := s.optimizationRulesPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		rules := defaultOptimizationRules()
		return rules, s.writeOptimizationRules(rules)
	}
	if err != nil {
		return nil, err
	}
	var rules []OptimizationRule
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, err
	}
	return normalizeOptimizationRules(rules), nil
}

// Serializes and writes optimization rules to a JSON file in the configured directory.

func (s *Server) writeOptimizationRules(rules []OptimizationRule) error {
	path := s.optimizationRulesPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(normalizeOptimizationRules(rules), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// Returns default graph optimization rules for filtering non-exported leaf and short functions.

func defaultOptimizationRules() []OptimizationRule {
	return []OptimizationRule{
		{
			ID:       "default-non-exported-leaf",
			Active:   true,
			Favorite: true,
			Operations: []OptimizationOperation{
				{Kind: "non_exported"},
				{Kind: "less_equal_incoming", Value: "1"},
				{Kind: "less_equal_outgoing", Value: "0"},
			},
		},
		{
			ID:       "default-non-exported-short",
			Active:   true,
			Favorite: true,
			Operations: []OptimizationOperation{
				{Kind: "non_exported"},
				{Kind: "less_equal_lines", Value: "10"},
			},
		},
	}
}

// Normalizes optimization rules by assigning auto-generated IDs to unnamed rules and trimming whitespace from operation kinds and values.

func normalizeOptimizationRules(rules []OptimizationRule) []OptimizationRule {
	for i := range rules {
		if strings.TrimSpace(rules[i].ID) == "" {
			rules[i].ID = fmt.Sprintf("rule-%d", i+1)
		}
		if rules[i].Operations == nil {
			rules[i].Operations = []OptimizationOperation{}
		}
		for j := range rules[i].Operations {
			rules[i].Operations[j].Kind = strings.TrimSpace(rules[i].Operations[j].Kind)
			rules[i].Operations[j].Value = strings.TrimSpace(rules[i].Operations[j].Value)
		}
	}
	return rules
}

// Unmarshals JSON optimization rules from a string and normalizes them.

func parseOptimizationRules(raw string) ([]OptimizationRule, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var rules []OptimizationRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, fmt.Errorf("invalid optimization_rules: %w", err)
	}
	return normalizeOptimizationRules(rules), nil
}

// Applies optimization rules to collapse nodes and rewire edges, removing collapsed nodes from the graph and reconnecting edges to their visible endpoints.

func (idx *graphIndex) optimizedGraph(graph GraphResponse, rules []OptimizationRule) GraphResponse {
	collapsed := idx.collapsedByRules(rules)
	if len(collapsed) == 0 {
		return graph
	}
	selected := make(map[string]bool, len(graph.Nodes))
	for _, node := range graph.Nodes {
		selected[node.ID] = true
	}
	visible, includes, parentsByCollapsed := idx.collapseAssignments(selected, collapsed)

	nodes := make([]GraphNode, 0, len(visible))
	for _, node := range graph.Nodes {
		if !visible[node.ID] {
			continue
		}
		node.Includes = includes[node.ID]
		nodes = append(nodes, node)
	}

	edges := make([]GraphEdge, 0, len(graph.Edges))
	seen := make(map[string]bool)
	addEdge := func(edge GraphEdge) {
		if !visible[edge.Source] || !visible[edge.Target] || edge.Source == edge.Target {
			return
		}
		key := edge.Source + "\x00" + edge.Target + "\x00" + edge.Type
		if seen[key] {
			return
		}
		seen[key] = true
		edges = append(edges, edge)
	}
	resolvedEndpoints := func(id string) []string {
		if visible[id] {
			return []string{id}
		}
		if collapsed[id] {
			return parentsByCollapsed[id]
		}
		return nil
	}
	for _, edge := range graph.Edges {
		for _, source := range resolvedEndpoints(edge.Source) {
			for _, target := range resolvedEndpoints(edge.Target) {
				rewired := edge
				rewired.Source = source
				rewired.Target = target
				rewired.SourceKind = idx.resourceKind(source)
				rewired.TargetKind = idx.resourceKind(target)
				addEdge(rewired)
			}
		}
	}
	sortEdges(edges)
	graph.Nodes = nodes
	graph.Edges = edges
	return graph
}

// Computes which selected resources should be hidden and groups them under their terminal incoming parents.

func (idx *graphIndex) collapseAssignments(selected map[string]bool, collapsed map[string]bool) (map[string]bool, map[string][]CollapsedResource, map[string][]string) {
	visible := make(map[string]bool, len(selected))
	for id := range selected {
		visible[id] = true
	}
	includes := make(map[string][]CollapsedResource)
	parentsByCollapsed := make(map[string][]string)
	for id := range selected {
		if !collapsed[id] {
			continue
		}
		parents := idx.terminalIncomingParents(id, selected, collapsed, map[string]bool{id: true})
		if len(parents) == 0 {
			continue
		}
		parentsByCollapsed[id] = parents
		delete(visible, id)
		for _, parent := range parents {
			includes[parent] = append(includes[parent], idx.collapsedResource(id))
		}
	}
	for parent := range includes {
		sortCollapsedResources(includes[parent])
	}
	return visible, includes, parentsByCollapsed
}

// Recursively finds terminal (leaf) incoming parents by traversing collapsed nodes and excluding non-selected or visiting nodes.

func (idx *graphIndex) terminalIncomingParents(id string, selected map[string]bool, collapsed map[string]bool, visiting map[string]bool) []string {
	seen := make(map[string]bool)
	for _, edge := range idx.incoming[id] {
		source := edge.Source
		if !selected[source] || visiting[source] {
			continue
		}
		if !collapsed[source] {
			seen[source] = true
			continue
		}
		visiting[source] = true
		for _, parent := range idx.terminalIncomingParents(source, selected, collapsed, visiting) {
			seen[parent] = true
		}
		delete(visiting, source)
	}
	return setIDs(seen)
}

// Returns collapsed resources that would include a given resource after applying optimization rules.

func (idx *graphIndex) includesFor(id string, rules []OptimizationRule) []CollapsedResource {
	collapsed := idx.collapsedByRules(rules)
	if len(collapsed) == 0 || collapsed[id] {
		return nil
	}
	selected := make(map[string]bool, len(idx.topo.Resources))
	for resourceID := range idx.topo.Resources {
		selected[resourceID] = true
	}
	_, includes, _ := idx.collapseAssignments(selected, collapsed)
	return includes[id]
}

// Sorts collapsed resources by kind, then by ID

func sortCollapsedResources(resources []CollapsedResource) {
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Kind != resources[j].Kind {
			return resources[i].Kind < resources[j].Kind
		}
		return resources[i].ID < resources[j].ID
	})
}

// Returns source code for a resource plus related included code sections formatted with their names and kinds according to optimization rules.

func (idx *graphIndex) nodeCodeWithIncludes(id string, rules []OptimizationRule) string {
	parts := make([]string, 0)
	if code := idx.nodeCode(id); code != "" {
		parts = append(parts, code)
	}
	for _, included := range idx.includesFor(id, rules) {
		code := idx.nodeCode(included.ID)
		if code == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("// Includes: %s (%s)\n%s", included.Name, included.Kind, code))
	}
	return strings.Join(parts, "\n\n")
}

// Identifies resources that match active optimization rules and marks them as collapsed.

func (idx *graphIndex) collapsedByRules(rules []OptimizationRule) map[string]bool {
	collapsed := make(map[string]bool)
	for id, res := range idx.topo.Resources {
		if len(idx.incoming[id]) == 0 {
			continue
		}
		for _, rule := range rules {
			if !rule.Active || len(rule.Operations) == 0 {
				continue
			}
			if idx.matchesOptimizationRule(id, res, rule) {
				collapsed[id] = true
				break
			}
		}
	}
	return collapsed
}

// Checks if a resource matches all operations in an optimization rule.

func (idx *graphIndex) matchesOptimizationRule(id string, res domain.Resource, rule OptimizationRule) bool {
	for _, op := range rule.Operations {
		if !idx.matchesOptimizationOperation(id, res, op) {
			return false
		}
	}
	return true
}

// Checks if a resource matches a single optimization operation condition (degree bounds, export status, kind, or line count).

func (idx *graphIndex) matchesOptimizationOperation(id string, res domain.Resource, op OptimizationOperation) bool {
	switch op.Kind {
	case "less_equal_incoming":
		return idx.inDegree[id] <= operationInt(op.Value)
	case "less_equal_outgoing":
		return idx.outDegree[id] <= operationInt(op.Value)
	case "less_equal_connections":
		return idx.inDegree[id]+idx.outDegree[id] <= operationInt(op.Value)
	case "non_exported":
		return nonExportedResource(res)
	case "resource_kind":
		return strings.EqualFold(string(res.Kind), op.Value)
	case "less_equal_lines":
		lines := resourceLines(res)
		return lines > 0 && lines <= operationInt(op.Value)
	default:
		return false
	}
}

// Parses a string value to a non-negative integer, returning 0 on parse error or negative values.

func operationInt(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// Checks if a resource is non-exported (unexported identifier name)

func nonExportedResource(res domain.Resource) bool {
	switch res.Kind {
	case domain.ResourceFunction, domain.ResourceMethod, domain.ResourceStruct, domain.ResourceNamedType, domain.ResourceInterface, domain.ResourceVariable:
		name := strings.TrimSpace(res.Name)
		if name == "" {
			name = res.ID
		}
		parts := strings.Split(name, ".")
		name = strings.Trim(parts[len(parts)-1], "()")
		return name != "" && !token.IsExported(name)
	default:
		return false
	}
}

// Calculates the line count of a resource based on its start and end locations.

func resourceLines(res domain.Resource) int {
	if res.Location.StartsAt <= 0 || res.Location.EndsAt < res.Location.StartsAt {
		return 0
	}
	return res.Location.EndsAt - res.Location.StartsAt + 1
}

// Returns a CollapsedResource representation of a resource by ID, with name, kind, and description from the topology.

func (idx *graphIndex) collapsedResource(id string) CollapsedResource {
	res, ok := idx.topo.Resources[id]
	if !ok {
		return CollapsedResource{ID: id, Name: id, Kind: "missing"}
	}
	return CollapsedResource{ID: id, Name: res.Name, Kind: string(res.Kind), Description: res.Description}
}

// HTTP handler that loads the topology index and returns all warnings sorted by ID as JSON.

func (s *Server) handleWarnings(w http.ResponseWriter, r *http.Request) {
	idx, err := s.loadIndex()
	if err != nil {
		writeError(w, err)
		return
	}
	warnings := make([]domain.TopologyWarning, 0, len(idx.topo.Warnings))
	for _, warning := range idx.topo.Warnings {
		warnings = append(warnings, warning)
	}
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].ID < warnings[j].ID })
	writeJSON(w, warnings)
}

// HTTP handler that retrieves and returns sorted bugs from the topology index.

func (s *Server) handleBugs(w http.ResponseWriter, r *http.Request) {
	idx, err := s.loadIndex()
	if err != nil {
		writeError(w, err)
		return
	}
	sort.Slice(idx.bugs, func(i, j int) bool { return idx.bugs[i].ID < idx.bugs[j].ID })
	writeJSON(w, idx.bugs)
}

// Retrieves source code for a resource by ID from the topology, returning empty string if the resource is missing or not inspectable.

func (idx *graphIndex) nodeCode(id string) string {
	res, ok := idx.topo.Resources[id]
	if !ok || !inspectableCodeKind(res.Kind) {
		return ""
	}
	loc := res.Location
	if loc.Path == "" && res.Kind == domain.ResourceFile {
		loc.Path = id
	}
	code, err := sourceCut(idx.topo.Root, loc)
	if err != nil {
		return ""
	}
	return code
}

// Returns true if a resource kind is code-inspectable (files, functions, methods, types, interfaces).

func inspectableCodeKind(kind domain.ResourceKind) bool {
	switch kind {
	case domain.ResourceFile, domain.ResourceFunction, domain.ResourceMethod, domain.ResourceStruct, domain.ResourceNamedType, domain.ResourceInterface:
		return true
	case domain.ResourceKind("struct"), domain.ResourceKind("class"):
		return true
	default:
		return false
	}
}

// Extracts and returns specified line range from a source file

func sourceCut(root string, loc domain.Location) (string, error) {
	path := loc.Path
	if path == "" {
		return "", errors.New("source path is empty")
	}
	if root != "" && !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if loc.StartsAt == 0 {
		loc.StartsAt = 1
	}
	if loc.EndsAt == 0 {
		loc.EndsAt = len(lines)
	}
	if loc.EndsAt < loc.StartsAt {
		return "", fmt.Errorf("EndsAt %d < StartsAt %d", loc.EndsAt, loc.StartsAt)
	}
	if loc.StartsAt < 1 || loc.StartsAt > len(lines) {
		return "", fmt.Errorf("StartsAt %d out of range (1-%d)", loc.StartsAt, len(lines))
	}
	if loc.EndsAt > len(lines) {
		return "", fmt.Errorf("EndsAt %d out of range (max %d)", loc.EndsAt, len(lines))
	}
	return strings.Join(lines[loc.StartsAt-1:loc.EndsAt], "\n"), nil
}

// Builds a filtered graph response from query results with optional edge filtering and truncation.

func (idx *graphIndex) resourceGraph(query string, kindSet map[string]bool, path string, language string, edgeSet map[string]bool, limit int, strictEdges bool) GraphResponse {
	ids := idx.filteredIDs(query, kindSet, path, language)
	total := len(ids)

	if strictEdges && hasSet(edgeSet) {
		connected := make(map[string]bool)
		for _, id := range ids {
			for _, edge := range idx.outgoing[id] {
				if edgeSet[strings.ToLower(edge.Type)] {
					connected[id] = true
					connected[edge.Target] = true
				}
			}
			for _, edge := range idx.incoming[id] {
				if edgeSet[strings.ToLower(edge.Type)] {
					connected[id] = true
					connected[edge.Source] = true
				}
			}
		}
		filtered := make([]string, 0, len(ids))
		for _, id := range ids {
			if connected[id] {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
		total = len(ids)
	}

	truncated := total > limit
	if truncated {
		ids = ids[:limit]
	}
	selected := idSet(ids)
	nodes := make([]GraphNode, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, idx.nodeDTO(id))
	}
	edges := idx.edgesWithin(selected, edgeSet)
	return GraphResponse{Nodes: nodes, Edges: edges, Truncated: truncated, Limit: limit, TotalMatch: total}
}

// packageGraph builds the "Packages & Modules" view. It is hybrid: Go is shown
// at package granularity (package nodes joined by package->package import edges,
// rolled up from file-level imports_package), while Python/JS/TS are shown at
// module granularity (file nodes joined by file->file imports_module edges).
// This reflects that a package is a real unit in Go but just a directory in the
// other languages, where the module (file) is the meaningful unit.
// other languages, where the module (file) is the meaningful unit.
func (idx *graphIndex) packageGraph(query string, language string, limit int) GraphResponse {
	query = strings.ToLower(query)
	membership := idx.packageMembership()
	nodeIDs := make([]string, 0)
	for id, res := range idx.topo.Resources {
		if !idx.isPackagesAndModulesNode(res) {
			continue
		}
		if language != "" && idx.resourceLanguage(res) != language {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(id), query) && !strings.Contains(strings.ToLower(res.Name), query) && !strings.Contains(strings.ToLower(res.Description), query) {
			continue
		}
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	total := len(nodeIDs)
	truncated := total > limit
	if truncated {
		nodeIDs = nodeIDs[:limit]
	}
	selected := idSet(nodeIDs)
	inDegree := make(map[string]int)
	outDegree := make(map[string]int)
	edgeMap := make(map[string]GraphEdge)
	for sourceID, res := range idx.topo.Resources {
		for connType, targets := range res.Connections {
			// imports_module is a direct file->file edge carried by the module
			// resource itself; imports_package/uses_package are file/member edges
			// rolled up to the owning package node (Go).
			var sourceNode string
			direct := connType == string(jsConnImportsModule)
			if direct {
				sourceNode = sourceID
			} else if isPackageImportEdge(connType) {
				sourceNode = packageFor(sourceID, res, membership)
			} else {
				continue
			}
			if sourceNode == "" || !selected[sourceNode] {
				continue
			}
			for _, targetID := range targets {
				targetNode := targetID
				if !direct {
					targetNode = idx.targetPackage(targetID, membership)
				}
				if targetNode == "" || targetNode == sourceNode || !selected[targetNode] {
					continue
				}
				key := sourceNode + "\x00" + targetNode + "\x00" + connType
				if _, exists := edgeMap[key]; exists {
					continue
				}
				edgeMap[key] = GraphEdge{Source: sourceNode, Target: targetNode, Type: connType}
				outDegree[sourceNode]++
				inDegree[targetNode]++
			}
		}
	}
	edges := make([]GraphEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		edges = append(edges, edge)
	}
	sortEdges(edges)
	nodes := make([]GraphNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		node := idx.nodeDTO(id)
		node.InDegree = inDegree[id]
		node.OutDegree = outDegree[id]
		nodes = append(nodes, node)
	}
	return GraphResponse{Nodes: nodes, Edges: edges, Truncated: truncated, Limit: limit, TotalMatch: total}
}

// isPackagesAndModulesNode reports whether a resource is a node in the
// "Packages & Modules" view: a Go package, or a Python/JS/TS/Rust/Java module (file).
func (idx *graphIndex) isPackagesAndModulesNode(res domain.Resource) bool {
	if res.Kind == domain.ResourcePackage {
		return true
	}
	if res.Kind == domain.ResourceFile {
		switch idx.resourceLanguage(res) {
		case "python", "javascript", "typescript", "rust", "java":
			return true
		}
	}
	return false
}

// jsConnImportsModule is the module->module import edge kind (shared spelling
// across the Python and JavaScript domains).
// across the Python and JavaScript domains).
const jsConnImportsModule = "imports_module"

// Maps resource IDs to their owning package, propagating membership through containment edges.

func (idx *graphIndex) packageMembership() map[string]string {
	membership := make(map[string]string)
	for id, res := range idx.topo.Resources {
		if res.Kind == domain.ResourcePackage {
			membership[id] = id
		}
	}
	for id, res := range idx.topo.Resources {
		if pkg, ok := stringProperty(res.Properties, "from_package"); ok && pkg != "" {
			membership[id] = pkg
		}
	}
	for packageID, res := range idx.topo.Resources {
		if res.Kind != domain.ResourcePackage && res.Kind != domain.ResourceFile {
			continue
		}
		owner := packageFor(packageID, res, membership)
		if owner == "" {
			continue
		}
		for connType, targets := range res.Connections {
			if !isContainmentEdge(connType) {
				continue
			}
			for _, targetID := range targets {
				membership[targetID] = owner
			}
		}
	}
	return membership
}

// Resolves the owning package of a target resource using membership map.

func (idx *graphIndex) targetPackage(targetID string, membership map[string]string) string {
	if res, ok := idx.topo.Resources[targetID]; ok {
		return packageFor(targetID, res, membership)
	}
	if pkg, ok := membership[targetID]; ok {
		return pkg
	}
	return ""
}

// Returns resource IDs matching query, kind, path, and language filters, sorted by path, kind, and ID.

func (idx *graphIndex) filteredIDs(query string, kindSet map[string]bool, path string, language string) []string {
	query = strings.ToLower(query)
	path = strings.ToLower(path)
	ids := make([]string, 0, len(idx.topo.Resources))
	for id, res := range idx.topo.Resources {
		if language != "" && idx.resourceLanguage(res) != language {
			continue
		}
		if hasSet(kindSet) && !kindSet[strings.ToLower(string(res.Kind))] {
			continue
		}
		if path != "" && !strings.Contains(strings.ToLower(res.Location.Path), path) && !strings.Contains(strings.ToLower(id), path) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(id), query) && !strings.Contains(strings.ToLower(res.Name), query) && !strings.Contains(strings.ToLower(res.Description), query) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left := idx.topo.Resources[ids[i]]
		right := idx.topo.Resources[ids[j]]
		if left.Location.Path != right.Location.Path {
			return left.Location.Path < right.Location.Path
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return ids[i] < ids[j]
	})
	return ids
}

// Returns all unique programming languages in the topology, sorted alphabetically.

func (idx *graphIndex) languages() []string {
	seen := make(map[string]bool)
	for _, lang := range idx.topo.Languages {
		if lang != "" {
			seen[lang] = true
		}
	}
	for _, res := range idx.topo.Resources {
		if lang := idx.resourceLanguage(res); lang != "" {
			seen[lang] = true
		}
	}
	languages := make([]string, 0, len(seen))
	for lang := range seen {
		languages = append(languages, lang)
	}
	sort.Strings(languages)
	return languages
}

// Resolves the language of a resource, falling back to topology language if unset.

func (idx *graphIndex) resourceLanguage(res domain.Resource) string {
	if res.Language != "" {
		return res.Language
	}
	if idx.topo.Language != "multi" {
		return idx.topo.Language
	}
	return ""
}

// Converts a topology resource into a GraphNode DTO with metadata including name, kind, language, location, degree counts, warnings, and bugs.

func (idx *graphIndex) nodeDTO(id string) GraphNode {
	res, ok := idx.topo.Resources[id]
	if !ok {
		return GraphNode{ID: id, Name: id, Kind: "missing", InDegree: idx.inDegree[id], OutDegree: idx.outDegree[id]}
	}
	return GraphNode{
		ID:           id,
		Name:         res.Name,
		Kind:         string(res.Kind),
		Language:     idx.resourceLanguage(res),
		Path:         relativePath(idx.topo.Root, res.Location.Path),
		StartsAt:     res.Location.StartsAt,
		EndsAt:       res.Location.EndsAt,
		Description:  res.Description,
		Properties:   res.Properties,
		InDegree:     idx.inDegree[id],
		OutDegree:    idx.outDegree[id],
		WarningCount: idx.warningCounts[id],
		BugCount:     idx.bugCounts[id],
	}
}

// Returns the kind of a resource by ID, or "missing" if not found.

func (idx *graphIndex) resourceKind(id string) string {
	if res, ok := idx.topo.Resources[id]; ok {
		return string(res.Kind)
	}
	return "missing"
}

// Returns edges between selected resources, optionally filtered by edge type, sorted for consistent output.

func (idx *graphIndex) edgesWithin(selected map[string]bool, edgeSet map[string]bool) []GraphEdge {
	edges := make([]GraphEdge, 0)
	for sourceID := range selected {
		for _, edge := range idx.outgoing[sourceID] {
			if hasSet(edgeSet) && !edgeSet[strings.ToLower(edge.Type)] {
				continue
			}
			if selected[edge.Target] {
				edges = append(edges, edge)
			}
		}
	}
	sortEdges(edges)
	return edges
}

// Explores graph nodes within a given depth and direction, filtering by kind and edge type, stopping at limit.

func (idx *graphIndex) neighborhood(root string, depth int, direction string, kindSet map[string]bool, edgeSet map[string]bool, limit int) (map[string]bool, bool) {
	selected := map[string]bool{root: true}
	frontier := []string{root}
	for level := 0; level < depth && len(frontier) > 0; level++ {
		next := make([]string, 0)
		for _, id := range frontier {
			for _, neighbor := range idx.neighbors(id, direction, kindSet, edgeSet) {
				if selected[neighbor] {
					continue
				}
				if len(selected) >= limit {
					return selected, true
				}
				selected[neighbor] = true
				next = append(next, neighbor)
			}
		}
		frontier = next
	}
	return selected, false
}

// Returns adjacent nodes in a given direction, optionally filtered by resource kind and edge type.

func (idx *graphIndex) neighbors(id string, direction string, kindSet map[string]bool, edgeSet map[string]bool) []string {
	seen := make(map[string]bool)
	add := func(edges []GraphEdge, incoming bool) {
		for _, edge := range edges {
			if hasSet(edgeSet) && !edgeSet[strings.ToLower(edge.Type)] {
				continue
			}
			neighbor := edge.Target
			if incoming {
				neighbor = edge.Source
			}
			if res, ok := idx.topo.Resources[neighbor]; ok {
				if hasSet(kindSet) && !kindSet[strings.ToLower(string(res.Kind))] {
					continue
				}
				seen[neighbor] = true
			}
		}
	}
	if direction == "out" || direction == "both" {
		add(idx.outgoing[id], false)
	}
	if direction == "in" || direction == "both" {
		add(idx.incoming[id], true)
	}
	return setIDs(seen)
}

// Collects and returns all topology warnings where the given resource ID is either the source or target, sorted by warning ID.

func (idx *graphIndex) nodeWarnings(id string) []domain.TopologyWarning {
	warnings := make([]domain.TopologyWarning, 0)
	for _, warning := range idx.topo.Warnings {
		if warning.SourceID == id || warning.TargetID == id {
			warnings = append(warnings, warning)
		}
	}
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].ID < warnings[j].ID })
	return warnings
}

// Returns all known bugs associated with a resource node, sorted by bug ID.

func (idx *graphIndex) nodeBugs(id string) []domain.KnownBug {
	bugs := make([]domain.KnownBug, 0)
	for _, bug := range idx.bugs {
		if bug.NodeID == id {
			bugs = append(bugs, bug)
		}
	}
	sort.Slice(bugs, func(i, j int) bool { return bugs[i].ID < bugs[j].ID })
	return bugs
}

// Resolves the package ID for a resource, returning its own ID if it is a package or its membership ID otherwise.

func packageFor(id string, res domain.Resource, membership map[string]string) string {
	if res.Kind == domain.ResourcePackage {
		return id
	}
	return membership[id]
}

// Extracts a string value from a property map by key, returning the value and whether the key exists as a string.

func stringProperty(props map[string]any, key string) (string, bool) {
	if props == nil {
		return "", false
	}
	value, ok := props[key]
	if !ok {
		return "", false
	}
	s, ok := value.(string)
	return s, ok
}

// Converts an absolute path to relative from a root directory, falling back to the original if not possible.

func relativePath(root, path string) string {
	if path == "" {
		return ""
	}
	if root == "" || !filepath.IsAbs(path) {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// Checks if an edge type represents package imports (imports_package or uses_package)

func isPackageImportEdge(edgeType string) bool {
	switch edgeType {
	case "imports_package", "uses_package":
		return true
	default:
		return false
	}
}

// Checks if an edge type represents containment (has_* prefix or methods)

func isContainmentEdge(edgeType string) bool {
	return strings.HasPrefix(edgeType, "has_") || edgeType == "methods"
}

// Returns a map of resource kinds that can belong to packages: package and file.

func packageKinds() map[string]bool {
	return map[string]bool{
		"package": true,
		"file":    true,
	}
}

// Returns a map of valid package-level edge types: imports_package, uses_package, and imports_module.

func packageEdges() map[string]bool {
	return map[string]bool{
		"imports_package": true,
		"uses_package":    true,
		"imports_module":  true,
	}
}

// Returns set of valid node kinds for data flow graph representation.

func dataFlowKinds() map[string]bool {
	return map[string]bool{
		"function":  true,
		"method":    true,
		"struct":    true,
		"interface": true,
	}
}

// Returns set of valid edge types for data flow graph representation.

func dataFlowEdges() map[string]bool {
	return map[string]bool{
		"calls":           true,
		"constructor":     true,
		"implemented_by":  true,
		"implements":      true,
		"inherited_by":    true,
		"inherits":        true,
		"methods":         true,
		"uses_class":      true,
		"uses_interface":  true,
		"uses_named_type": true,
		"uses_struct":     true,
	}
}

// Normalizes a language filter string (lowercases, trims, treats empty/"all" as unfiltered)

func normalizeLanguageFilter(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" || language == "all" {
		return ""
	}
	return language
}

// Parses comma-separated query parameter values into a set of lowercase strings.

func parseSet(values url.Values, key string) map[string]bool {
	set := make(map[string]bool)
	for _, value := range values[key] {
		for _, part := range strings.Split(value, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part != "" {
				set[part] = true
			}
		}
	}
	return set
}

// Checks whether a boolean set (map) has any entries.

func hasSet(set map[string]bool) bool {
	return len(set) > 0
}

// Converts a slice of IDs into a boolean map for O(1) membership testing.

func idSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// Converts a map of string keys into a sorted slice of strings

func setIDs(set map[string]bool) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Sorts graph edges by source, then target, then type

func sortEdges(edges []GraphEdge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		if edges[i].Target != edges[j].Target {
			return edges[i].Target < edges[j].Target
		}
		return edges[i].Type < edges[j].Type
	})
}

// Extracts and clamps an integer from query parameters between min and max bounds.

func queryInt(values url.Values, key string, fallback, min, max int) int {
	value := strings.TrimSpace(values.Get(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	if parsed < min {
		return min
	}
	if parsed > max {
		return max
	}
	return parsed
}

// Encodes and writes a value as indented JSON to the HTTP response.

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		log.Printf("write json response: %v", err)
	}
}

// Writes an HTTP 400 error response as JSON with the error message.

func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// isLoopbackHost reports whether a host[:port] refers to the local machine.
// isLoopbackHost reports whether a host[:port] refers to the local machine.
func isLoopbackHost(hostPort string) bool {
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" || host == "" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// localOriginAllowed guards the local-only API against cross-site access and
// DNS-rebinding. The Host header must resolve to loopback (a rebound public
// domain will not), and any browser-supplied Origin must itself be loopback.
// This is intentional for a localhost developer tool; binding to a non-loopback
// address is not a supported configuration for the chat/API surface.
// address is not a supported configuration for the chat/API surface.
func localOriginAllowed(r *http.Request) bool {
	if !isLoopbackHost(r.Host) {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return isLoopbackHost(u.Host) || strings.EqualFold(u.Host, r.Host)
}
