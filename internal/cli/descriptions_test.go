package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ltp/internal/topology/domain"
)

func TestDescriptionsExecutorProfileIsLimited(t *testing.T) {
	tools := profileTools(ToolProfileDescriptionsExecutor)
	allowed := make(map[string]bool, len(tools))
	for _, tool := range tools {
		allowed[tool] = true
	}

	for _, want := range []string{"read_file", "read_struct", "read_function", "read_resource_and_cut", "update_description"} {
		if !allowed[want] {
			t.Fatalf("executor profile missing %q in %v", want, tools)
		}
	}
	for _, blocked := range []string{"list_undocumented_resources", "edit", "write", "bug_report", "bug_list"} {
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
	input := descriptionExecutorInput([]descriptionResource{{ID: "fn:one", Name: "One", Kind: domain.ResourceFunction}})
	for _, want := range []string{"Process only the assigned resources", "read_resource_and_cut", "update_description", "fn:one", "Kind: function"} {
		if !strings.Contains(input, want) {
			t.Fatalf("executor input missing %q:\n%s", want, input)
		}
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
