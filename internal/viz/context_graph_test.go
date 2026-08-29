package viz

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"aracne/internal/chat"
	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

func contextTestIndex(t *testing.T) *graphIndex {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	topo := &domain.Topology{
		Root:     dir,
		Language: "go",
		Resources: map[string]domain.Resource{
			"p.A": {
				ID: "p.A", Kind: domain.ResourceFunction, Name: "A",
				Description: "entry", Location: domain.Location{Path: "a.go", StartsAt: 1, EndsAt: 10},
				Connections: map[string][]string{
					"calls":           {"p.B", "p.Sm"},
					"uses_struct":     {"p.T"},
					"uses_named_type": {"p.NT"},
				},
			},
			"p.B": {
				ID: "p.B", Kind: domain.ResourceFunction, Name: "B",
				Description: "big neighbor", Location: domain.Location{Path: "b.go", StartsAt: 20, EndsAt: 60},
			},
			"p.Sm": {
				ID: "p.Sm", Kind: domain.ResourceFunction, Name: "Sm",
				Description: "small helper", Location: domain.Location{Path: "b.go", StartsAt: 70, EndsAt: 72},
			},
			"p.T": {
				ID: "p.T", Kind: domain.ResourceStruct, Name: "T",
				Description: "a type", Location: domain.Location{Path: "t.go", StartsAt: 1, EndsAt: 4},
			},
			"p.NT": {
				ID: "p.NT", Kind: domain.ResourceNamedType, Name: "NT",
				Description: "named type", Location: domain.Location{Path: "t.go", StartsAt: 6, EndsAt: 6},
			},
			"p.G": {
				ID: "p.G", Kind: domain.ResourceFunction, Name: "G",
				Description: "grep hit", Location: domain.Location{Path: "g.go", StartsAt: 1, EndsAt: 3},
			},
			"p.C": {
				ID: "p.C", Kind: domain.ResourceFunction, Name: "C",
				Description: "in c.go", Location: domain.Location{Path: "c.go", StartsAt: 1, EndsAt: 5},
				Connections: map[string][]string{"calls": {"p.B"}},
			},
		},
		Warnings: map[string]domain.TopologyWarning{},
		Errors:   map[string]string{},
	}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	srv := &Server{dbPath: dbPath}
	idx, err := srv.loadIndex()
	if err != nil {
		t.Fatalf("loadIndex: %v", err)
	}
	return idx
}

// readMsg builds a completed `read` call. The tool takes a LIST of ids, so the recorded input
// is an []any exactly as it round-trips through a session transcript.
func readMsg(ids ...string) chat.SessionMessage {
	m := chat.SessionMessage{Role: "tool", Status: "completed", ToolName: "read"}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	m.ToolInput = map[string]any{"ids": args}
	return m
}

func TestDeriveSeenResources(t *testing.T) {
	idx := contextTestIndex(t)
	// SmallFnVisibility=Full: small functions surfaced as full code are green.
	filter := domain.ContextFilter{
		ExtVarsVisibility: domain.VisibilityNormal,
		SmallFnVisibility: domain.VisibilityFull,
		SmallFnThreshold:  5,
	}

	// Real topogrep output: one "# <id> — <description>" header above the resource's
	// matches. The old fixture used a "ResourceID:" prefix grep has never emitted.
	grepOut := "# p.G — grep hit\ng.go:1:func G() {\n"
	messages := []chat.SessionMessage{
		readMsg("p.A"), // A green; B yellow, Sm green, T yellow, NT excluded
		{Role: "tool", Status: "error", ToolName: "read", ToolInput: map[string]any{"ids": []any{"p.T"}}}, // ignored
		{Role: "tool", Status: "completed", ToolName: "grep", ToolOutput: grepOut},                        // G yellow
		readMsg("p.B", "c.go"), // one batched call: B upgraded to green, C green
	}

	seen := deriveSeenResources(idx, filter, messages)
	want := map[string]string{
		"p.A":  ctxGreen,
		"p.B":  ctxGreen, // upgraded from yellow
		"p.Sm": ctxGreen, // small function shown full
		"p.T":  ctxYellow,
		"p.G":  ctxYellow,
		"p.C":  ctxGreen,
	}
	for id, state := range want {
		if seen[id] != state {
			t.Errorf("seen[%s] = %q, want %q", id, seen[id], state)
		}
	}
	if _, ok := seen["p.NT"]; ok {
		t.Errorf("named_type p.NT must not appear in a Data Flow context graph")
	}
	if len(seen) != len(want) {
		t.Errorf("unexpected seen set: %+v", seen)
	}
}

func TestDeriveSeenResourcesHideNoDescriptionAndNormal(t *testing.T) {
	idx := contextTestIndex(t)
	// Default-style filter: nothing is inlined as full, so neighbors are yellow.
	filter := domain.ContextFilter{
		ExtVarsVisibility: domain.VisibilityNormal,
		SmallFnVisibility: domain.VisibilityNormal,
		SmallFnThreshold:  5,
	}
	seen := deriveSeenResources(idx, filter, []chat.SessionMessage{readMsg("p.A")})
	if seen["p.A"] != ctxGreen {
		t.Errorf("primary read should be green, got %q", seen["p.A"])
	}
	if seen["p.Sm"] != ctxYellow {
		t.Errorf("small fn under Normal visibility should be yellow, got %q", seen["p.Sm"])
	}
	if seen["p.B"] != ctxYellow || seen["p.T"] != ctxYellow {
		t.Errorf("neighbors should be yellow: B=%q T=%q", seen["p.B"], seen["p.T"])
	}
}

func TestParseGrepResourceIDs(t *testing.T) {
	// The header topogrep.FormatResult actually emits: "# <id>", optionally followed
	// by " — <description>", once per resource run. A bare header (no description)
	// and a repeated resource must both work.
	out := "# p.A — x\na.go:1:hit\n# p.B\nb.go:2:hit\n# p.A — x\na.go:9:hit\n"
	got := parseGrepResourceIDs(out)
	if len(got) != 2 || got[0] != "p.A" || got[1] != "p.B" {
		t.Fatalf("parseGrepResourceIDs = %v, want [p.A p.B]", got)
	}
}

func TestParseGrepResourceIDsIgnoresNonHeaderLines(t *testing.T) {
	// Match lines are always "path:line:text", so a hit on a shell/Python comment
	// cannot be mistaken for a header. Trailers start with "…".
	out := "a.sh:3:# not a header\n… 12 more match(es) not shown (showing 200 of 212).\n"
	if got := parseGrepResourceIDs(out); len(got) != 0 {
		t.Fatalf("parseGrepResourceIDs = %v, want none", got)
	}
}

func TestHandleContextGraphEmptySession(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	if err := helper.WriteDb(&domain.Topology{Root: dir, Resources: map[string]domain.Resource{}}, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	srv := &Server{dbPath: dbPath, ws: NewWebSocketManager()}
	mgr, err := srv.chatManager()
	if err != nil {
		t.Fatalf("chatManager: %v", err)
	}
	session, err := mgr.CreateSession("", "ctx test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.handleContextGraph(rec, httptest.NewRequest("GET", "/api/context-graph?session_id="+session.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp contextGraphResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if len(resp.Nodes) != 0 || len(resp.Edges) != 0 {
		t.Fatalf("expected empty context graph, got %+v", resp)
	}

	// Missing session_id is a client error, not a 200.
	bad := httptest.NewRecorder()
	srv.handleContextGraph(bad, httptest.NewRequest("GET", "/api/context-graph", nil))
	if bad.Code == http.StatusOK {
		t.Fatalf("missing session_id should not return 200")
	}
}

func TestFavoriteRules(t *testing.T) {
	rules := []OptimizationRule{
		{ID: "1", Favorite: true},
		{ID: "2", Favorite: false},
		{ID: "3", Favorite: true},
	}
	favs := favoriteRules(rules)
	if len(favs) != 2 || favs[0].ID != "1" || favs[1].ID != "3" {
		t.Fatalf("favoriteRules = %+v, want rules 1 and 3", favs)
	}
}
