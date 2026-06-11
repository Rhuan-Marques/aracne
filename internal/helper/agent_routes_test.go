package helper

import (
	"path/filepath"
	"testing"
	"time"

	"aracne/internal/topology/domain"
)

func TestAgentRouteAccessLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "topology.db")
	topo := &domain.Topology{
		Root:     ".",
		Language: "go",
		Resources: map[string]domain.Resource{
			"f1": {ID: "f1", Kind: domain.ResourceFunction, Name: "One", Connections: map[string][]string{}},
		},
		Warnings: map[string]domain.TopologyWarning{},
		Errors:   map[string]string{},
	}
	if err := WriteDb(topo, dbPath); err != nil {
		t.Fatalf("write db: %v", err)
	}

	ttl := 10 * time.Minute
	if err := RecordAgentRouteAccess(dbPath, "s1", "opencode", "session", AgentRouteAccessDescription, []string{"f1"}, ttl); err != nil {
		t.Fatalf("record description: %v", err)
	}
	resources, err := ReadAgentRouteResources(dbPath, "s1", ttl)
	if err != nil {
		t.Fatalf("read resources: %v", err)
	}
	if got := resources["f1"].AccessKind; got != AgentRouteAccessDescription {
		t.Fatalf("access kind = %q, want %q", got, AgentRouteAccessDescription)
	}

	if err := RecordAgentRouteAccess(dbPath, "s1", "opencode", "session", AgentRouteAccessFullCut, []string{"f1"}, ttl); err != nil {
		t.Fatalf("record full cut: %v", err)
	}
	if err := RecordAgentRouteAccess(dbPath, "s1", "opencode", "session", AgentRouteAccessDescription, []string{"f1"}, ttl); err != nil {
		t.Fatalf("record downgrade attempt: %v", err)
	}
	resources, err = ReadAgentRouteResources(dbPath, "s1", ttl)
	if err != nil {
		t.Fatalf("read resources after upgrade: %v", err)
	}
	if got := resources["f1"].AccessKind; got != AgentRouteAccessFullCut {
		t.Fatalf("access kind after upgrade = %q, want %q", got, AgentRouteAccessFullCut)
	}

	routes, err := ReadAgentRoutes(dbPath, ttl)
	if err != nil {
		t.Fatalf("read routes: %v", err)
	}
	if len(routes) != 1 || routes[0].NodeCount != 1 {
		t.Fatalf("unexpected routes: %+v", routes)
	}
}

func TestAgentRouteCleanupUsesTTL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "topology.db")
	topo := &domain.Topology{Resources: map[string]domain.Resource{"f1": {ID: "f1", Kind: domain.ResourceFunction, Name: "One"}}, Warnings: map[string]domain.TopologyWarning{}, Errors: map[string]string{}}
	if err := WriteDb(topo, dbPath); err != nil {
		t.Fatalf("write db: %v", err)
	}
	if err := RecordAgentRouteAccess(dbPath, "s1", "claude", "", AgentRouteAccessDescription, []string{"f1"}, time.Minute); err != nil {
		t.Fatalf("record route: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := CleanupStaleAgentRoutes(dbPath, time.Millisecond); err != nil {
		t.Fatalf("cleanup routes: %v", err)
	}
	routes, err := ReadAgentRoutes(dbPath, time.Millisecond)
	if err != nil {
		t.Fatalf("read routes: %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("expected stale route cleanup, got %+v", routes)
	}
}

func TestDefaultAgentRouteConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.AgentRoutes.TTLSeconds != DefaultAgentRouteTTLSeconds {
		t.Fatalf("ttl seconds = %d, want %d", cfg.AgentRoutes.TTLSeconds, DefaultAgentRouteTTLSeconds)
	}
	if cfg.EffectiveAgentRouteTTL() != 10*time.Minute {
		t.Fatalf("effective ttl = %s, want 10m", cfg.EffectiveAgentRouteTTL())
	}
}
