package viz

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"aracne/internal/chat"
	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

type Server struct {
	dbPath   string
	ws       *WebSocketManager
	chatOnce sync.Once
	chatMgr  *chat.Manager
	chatErr  error
}

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

type GraphNode struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	Kind             string              `json:"kind"`
	Language         string              `json:"language"`
	Path             string              `json:"path"`
	StartsAt         int                 `json:"starts_at"`
	EndsAt           int                 `json:"ends_at"`
	Description      string              `json:"description,omitempty"`
	Properties       map[string]any      `json:"properties,omitempty"`
	InDegree         int                 `json:"in_degree"`
	OutDegree        int                 `json:"out_degree"`
	WarningCount     int                 `json:"warning_count"`
	BugCount         int                 `json:"bug_count"`
	Includes         []CollapsedResource `json:"includes,omitempty"`
}

type CollapsedResource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
}

type OptimizationRule struct {
	ID         string                  `json:"id"`
	Active     bool                    `json:"active"`
	Favorite   bool                    `json:"favorite"`
	Operations []OptimizationOperation `json:"operations"`
}

type OptimizationOperation struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

type GraphEdge struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Type       string `json:"type"`
	SourceKind string `json:"source_kind"`
	TargetKind string `json:"target_kind"`
}

type GraphResponse struct {
	Nodes      []GraphNode `json:"nodes"`
	Edges      []GraphEdge `json:"edges"`
	Truncated  bool        `json:"truncated"`
	Limit      int         `json:"limit"`
	TotalMatch int         `json:"total_match"`
}

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

func NewServer(dbPath string) *Server {
	return &Server{dbPath: dbPath, ws: NewWebSocketManager()}
}

func Listen(addr, dbPath string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           NewServer(dbPath),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Topology visualization listening on http://%s (db: %s)", addr, dbPath)
	return srv.ListenAndServe()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	mux.Handle("/", staticFileServer())
	mux.HandleFunc("/api/ws", s.handleWebSocket)
	mux.HandleFunc("/api/summary", s.handleSummary)
	mux.HandleFunc("/api/graph", s.handleGraph)
	mux.HandleFunc("/api/neighborhood", s.handleNeighborhood)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/node/", s.handleNode)
	mux.HandleFunc("/api/optimization-rules", s.handleOptimizationRules)
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/chat/", s.handleChat)
	mux.HandleFunc("/api/warnings", s.handleWarnings)
	mux.HandleFunc("/api/bugs", s.handleBugs)
	mux.ServeHTTP(w, r)
}

func (s *Server) loadIndex() (*graphIndex, error) {
	topo, err := helper.ReadDb(s.dbPath)
	if err != nil {
		return nil, err
	}
	bugs, err := helper.ReadBugs(s.dbPath, "", "")
	if err != nil {
		return nil, err
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

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	s.ws.ServeHTTP(w, r)
}

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

func (s *Server) optimizationRulesPath() string {
	return filepath.Join(filepath.Dir(s.dbPath), "optimization_rules.json")
}

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

func sortCollapsedResources(resources []CollapsedResource) {
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Kind != resources[j].Kind {
			return resources[i].Kind < resources[j].Kind
		}
		return resources[i].ID < resources[j].ID
	})
}

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

func (idx *graphIndex) matchesOptimizationRule(id string, res domain.Resource, rule OptimizationRule) bool {
	for _, op := range rule.Operations {
		if !idx.matchesOptimizationOperation(id, res, op) {
			return false
		}
	}
	return true
}

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

func operationInt(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func nonExportedResource(res domain.Resource) bool {
	switch res.Kind {
	case domain.ResourceFunction, domain.ResourceMethod, domain.ResourceType, domain.ResourceNamedType, domain.ResourceInterface, domain.ResourceVariable:
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

func resourceLines(res domain.Resource) int {
	if res.Location.StartsAt <= 0 || res.Location.EndsAt < res.Location.StartsAt {
		return 0
	}
	return res.Location.EndsAt - res.Location.StartsAt + 1
}

func (idx *graphIndex) collapsedResource(id string) CollapsedResource {
	res, ok := idx.topo.Resources[id]
	if !ok {
		return CollapsedResource{ID: id, Name: id, Kind: "missing"}
	}
	return CollapsedResource{ID: id, Name: res.Name, Kind: string(res.Kind), Description: res.Description}
}

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

func (s *Server) handleBugs(w http.ResponseWriter, r *http.Request) {
	idx, err := s.loadIndex()
	if err != nil {
		writeError(w, err)
		return
	}
	sort.Slice(idx.bugs, func(i, j int) bool { return idx.bugs[i].ID < idx.bugs[j].ID })
	writeJSON(w, idx.bugs)
}

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

func inspectableCodeKind(kind domain.ResourceKind) bool {
	switch kind {
	case domain.ResourceFile, domain.ResourceFunction, domain.ResourceMethod, domain.ResourceType, domain.ResourceNamedType, domain.ResourceInterface:
		return true
	case domain.ResourceKind("struct"), domain.ResourceKind("class"):
		return true
	default:
		return false
	}
}

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

func (idx *graphIndex) packageGraph(query string, language string, limit int) GraphResponse {
	query = strings.ToLower(query)
	membership := idx.packageMembership()
	packageIDs := make([]string, 0)
	for id, res := range idx.topo.Resources {
		if res.Kind != domain.ResourcePackage {
			continue
		}
		if language != "" && idx.resourceLanguage(res) != language {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(id), query) && !strings.Contains(strings.ToLower(res.Name), query) && !strings.Contains(strings.ToLower(res.Description), query) {
			continue
		}
		packageIDs = append(packageIDs, id)
	}
	sort.Strings(packageIDs)
	total := len(packageIDs)
	truncated := total > limit
	if truncated {
		packageIDs = packageIDs[:limit]
	}
	selected := idSet(packageIDs)
	inDegree := make(map[string]int)
	outDegree := make(map[string]int)
	edgeMap := make(map[string]GraphEdge)
	for sourceID, res := range idx.topo.Resources {
		sourcePkg := packageFor(sourceID, res, membership)
		if sourcePkg == "" || !selected[sourcePkg] {
			continue
		}
		for connType, targets := range res.Connections {
			if !isPackageImportEdge(connType) {
				continue
			}
			for _, targetID := range targets {
				targetPkg := idx.targetPackage(targetID, membership)
				if targetPkg == "" || targetPkg == sourcePkg || !selected[targetPkg] {
					continue
				}
				key := sourcePkg + "\x00" + targetPkg + "\x00" + connType
				if _, exists := edgeMap[key]; exists {
					continue
				}
				edgeMap[key] = GraphEdge{Source: sourcePkg, Target: targetPkg, Type: connType}
				outDegree[sourcePkg]++
				inDegree[targetPkg]++
			}
		}
	}
	edges := make([]GraphEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		edges = append(edges, edge)
	}
	sortEdges(edges)
	nodes := make([]GraphNode, 0, len(packageIDs))
	for _, id := range packageIDs {
		node := idx.nodeDTO(id)
		node.InDegree = inDegree[id]
		node.OutDegree = outDegree[id]
		nodes = append(nodes, node)
	}
	return GraphResponse{Nodes: nodes, Edges: edges, Truncated: truncated, Limit: limit, TotalMatch: total}
}

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

func (idx *graphIndex) targetPackage(targetID string, membership map[string]string) string {
	if res, ok := idx.topo.Resources[targetID]; ok {
		return packageFor(targetID, res, membership)
	}
	if pkg, ok := membership[targetID]; ok {
		return pkg
	}
	return ""
}

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

func (idx *graphIndex) resourceLanguage(res domain.Resource) string {
	if res.Language != "" {
		return res.Language
	}
	if idx.topo.Language != "multi" {
		return idx.topo.Language
	}
	return ""
}

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

func (idx *graphIndex) resourceKind(id string) string {
	if res, ok := idx.topo.Resources[id]; ok {
		return string(res.Kind)
	}
	return "missing"
}

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

func packageFor(id string, res domain.Resource, membership map[string]string) string {
	if res.Kind == domain.ResourcePackage {
		return id
	}
	return membership[id]
}

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

func isPackageImportEdge(edgeType string) bool {
	switch edgeType {
	case "imports_package", "uses_package":
		return true
	default:
		return false
	}
}

func isContainmentEdge(edgeType string) bool {
	return strings.HasPrefix(edgeType, "has_") || edgeType == "methods"
}

func packageKinds() map[string]bool {
	return map[string]bool{
		"package": true,
	}
}

func packageEdges() map[string]bool {
	return map[string]bool{
		"imports_package": true,
		"uses_package":    true,
	}
}

func dataFlowKinds() map[string]bool {
	return map[string]bool{
		"function":  true,
		"method":    true,
		"type":      true,
		"interface": true,
	}
}

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

func normalizeLanguageFilter(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" || language == "all" {
		return ""
	}
	return language
}

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

func hasSet(set map[string]bool) bool {
	return len(set) > 0
}

func idSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func setIDs(set map[string]bool) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

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

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		log.Printf("write json response: %v", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
