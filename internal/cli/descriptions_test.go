package cli

import (
	"database/sql"

	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
)

func TestDescriptionsExecutorProfileIsLimited(t *testing.T) {
	tools := helper.DefaultAgentMCPTools("descriptions-generation-executor")
	allowed := make(map[string]bool, len(tools))
	for _, tool := range tools {
		allowed[tool] = true
	}

	for _, want := range []string{"read", "update_description"} {
		if !allowed[want] {
			t.Fatalf("executor profile missing %q in %v", want, tools)
		}
	}
	for _, blocked := range []string{"node_list_no_description", "edit", "write", "bug_report", "bug_list"} {
		if allowed[blocked] {
			t.Fatalf("executor profile should not include %q in %v", blocked, tools)
		}
	}
}

func TestChunkDescriptionResourcesUsesBatchesOfRequestedSize(t *testing.T) {
	resources := []descriptionResource{
		{ID: "1", Name: "one", Kind: domain.ResourceFunction},
		{ID: "2", Name: "two", Kind: domain.ResourceFunction},
		{ID: "3", Name: "three", Kind: domain.ResourceFunction},
		{ID: "4", Name: "four", Kind: domain.ResourceFunction},
		{ID: "5", Name: "five", Kind: domain.ResourceFunction},
	}

	batches := chunkDescriptionResources(resources, 2)
	if len(batches) != 3 {
		t.Fatalf("len(batches) = %d, want 3", len(batches))
	}
	if len(batches[0]) != 2 || len(batches[1]) != 2 || len(batches[2]) != 1 {
		t.Fatalf("unexpected batch sizes: %d, %d, %d", len(batches[0]), len(batches[1]), len(batches[2]))
	}

	seen := map[string]bool{}
	for _, batch := range batches {
		for _, res := range batch {
			if seen[res.ID] {
				t.Fatalf("resource %s appeared in multiple batches", res.ID)
			}
			seen[res.ID] = true
		}
	}
}

func TestDescriptionExecutorInputConstrainsAssignedResources(t *testing.T) {
	input := descriptionExecutorInput([]descriptionResource{{ID: "fn:one", Name: "One", Kind: domain.ResourceFunction}}, nil, nil)
	for _, want := range []string{"Describe only the assigned resource", "read", "update_description", "fn:one", "Kind: function"} {
		if !strings.Contains(input, want) {
			t.Fatalf("executor input missing %q:\n%s", want, input)
		}
	}
}

// A --regen_oversized batch must reach the executor as a rewrite of the stored text, not
// as a blank-slate description.
func TestDescriptionExecutorInputCarriesCurrentDescription(t *testing.T) {
	current := strings.Repeat("wordy ", 40)
	input := descriptionExecutorInput([]descriptionResource{
		{ID: "fn:one", Name: "One", Kind: domain.ResourceFunction, Current: current},
	}, nil, nil)
	for _, want := range []string{"Rewrite the description", "Current description (", "fn:one"} {
		if !strings.Contains(input, want) {
			t.Fatalf("regen executor input missing %q:\n%s", want, input)
		}
	}
}

// --regen_oversized selects described-but-too-long resources; the default pass selects
// undescribed ones. The two populations never overlap.
func TestPendingDescriptionResourcesRegenOversized(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "topology.db")
	loc := domain.Location{Path: "x.go", StartsAt: 1, EndsAt: 20}
	overFn := strings.Repeat("x", domain.DescriptionBudgetFunction+1)
	topo := &domain.Topology{Root: ".", Language: "go", Resources: map[string]domain.Resource{
		"fn:over":  {ID: "fn:over", Name: "Over", Kind: domain.ResourceFunction, Description: overFn, Location: loc},
		"fn:ok":    {ID: "fn:ok", Name: "Ok", Kind: domain.ResourceFunction, Description: "Fits fine.", Location: loc},
		"fn:blank": {ID: "fn:blank", Name: "Blank", Kind: domain.ResourceFunction, Location: loc},
		// Fits a function's 120 but overruns a type's 100.
		"st:over": {ID: "st:over", Name: "StOver", Kind: domain.ResourceStruct, Description: strings.Repeat("y", domain.DescriptionBudgetType+1), Location: loc},
	}}
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	// WriteDb caps descriptions on the way in (domain.CapDescription), so an over-budget row
	// can no longer be created through it. These rows are the GRANDFATHERED ones --  written
	// before the cap existed -- which is the only population `--regen_oversized` exists to
	// find, so the test writes them the way history did: straight into the table.
	writeOversizedDescription(t, dbPath, "fn:over", strings.Repeat("x", domain.DescriptionBudgetFunction+1))
	writeOversizedDescription(t, dbPath, "st:over", strings.Repeat("y", domain.DescriptionBudgetType+1))
	manager := topology.New()
	if err := manager.Load(dbPath); err != nil {
		t.Fatalf("Load: %v", err)
	}

	targets := []domain.ResourceKind{domain.ResourceFunction, domain.ResourceStruct}
	regen, err := pendingDescriptionResources(manager, targets, domain.DefaultContextFilter(), false, true)
	if err != nil {
		t.Fatalf("pendingDescriptionResources: %v", err)
	}
	ids := make([]string, 0, len(regen))
	for _, res := range regen {
		ids = append(ids, res.ID)
	}
	if got := strings.Join(ids, ","); got != "fn:over,st:over" {
		t.Fatalf("regen selection = %q, want \"fn:over,st:over\"", got)
	}
	for _, res := range regen {
		if res.Current == "" {
			t.Fatalf("%s must carry its stored description into the prompt", res.ID)
		}
	}

	generate, err := pendingDescriptionResources(manager, targets, domain.DefaultContextFilter(), false, false)
	if err != nil {
		t.Fatalf("pendingDescriptionResources: %v", err)
	}
	if len(generate) != 1 || generate[0].ID != "fn:blank" || generate[0].Current != "" {
		t.Fatalf("default pass should select only the undescribed resource, got %+v", generate)
	}
}

func TestParseClearDescriptionTargetsAcceptsBracketedKinds(t *testing.T) {
	targets, err := parseClearDescriptionTargets("[Function, Struct]")
	if err != nil {
		t.Fatalf("parseClearDescriptionTargets: %v", err)
	}
	if len(targets) != 2 || targets[0] != domain.ResourceFunction || targets[1] != domain.ResourceStruct {
		t.Fatalf("targets = %v, want [function struct]", targets)
	}
}

func TestParseClearDescriptionTargetsEmptyMeansAll(t *testing.T) {
	targets, err := parseClearDescriptionTargets("")
	if err != nil {
		t.Fatalf("parseClearDescriptionTargets: %v", err)
	}
	if targets != nil {
		t.Fatalf("targets = %v, want nil", targets)
	}
}

func TestWriteOpenCodePrimaryCommandDoesNotCreateSubtask(t *testing.T) {
	dir := t.TempDir()
	writeOpenCodePrimaryCommand(dir, "descriptions-generate", "Generate descriptions", "build", "body", true)

	data, err := os.ReadFile(filepath.Join(dir, "descriptions-generate.md"))
	if err != nil {
		t.Fatalf("read command: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "agent: build") {
		t.Fatalf("command missing primary agent:\n%s", content)
	}
	if strings.Contains(content, "subtask: true") {
		t.Fatalf("descriptions command must not run as an OpenCode subtask:\n%s", content)
	}
}

// writeOversizedDescription puts a description into the table directly, bypassing the storage
// cap, to simulate a row written before that cap existed.
func writeOversizedDescription(t *testing.T, dbPath, id, description string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE resources SET description = ? WHERE id = ?", description, id); err != nil {
		t.Fatalf("seed oversized description: %v", err)
	}
}
