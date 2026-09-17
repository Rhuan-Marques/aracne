package agent

import (
	"encoding/json"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm"
	"github.com/Rhuan-Marques/aracne/internal/llm/toolapi"
	"github.com/Rhuan-Marques/aracne/internal/prompts"
)

type mockProvider struct {
	response *llm.ChatResponse
}

func (m *mockProvider) Chat(messages []llm.Message, toolDefs []llm.ToolDefinition) (*llm.ChatResponse, error) {
	return m.response, nil
}

func (m *mockProvider) StreamChat(messages []llm.Message, toolDefs []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
	return m.response, nil
}

type mockTool struct{}

func (m *mockTool) Name() string        { return "mock_tool" }
func (m *mockTool) Description() string { return "A mock tool" }
func (m *mockTool) Parameters() []toolapi.Parameter {
	return []toolapi.Parameter{
		{Name: "input", Type: "string", Description: "input", Required: true},
	}
}
func (m *mockTool) Run(args json.RawMessage) (string, error) {
	return "tool_result", nil
}

// The harness's system prompt is the project's contract, not a per-language constant of its
// own. What used to be five `Build<Lang>SystemPrompt` functions here (plus a "multi" case that
// concatenated all five) is one call into prompts, so a change to what a read returns reaches
// this harness and CLAUDE.md together or not at all.
func TestBuildPromptIsTheProjectContract(t *testing.T) {
	cfg := helper.DefaultConfig()
	for _, languages := range [][]string{{"go"}, {"python"}, {"go", "rust"}, nil, {"unknown"}} {
		got := BuildPrompt(cfg, languages)
		if got == "" {
			t.Fatalf("languages %v: empty system prompt", languages)
		}
		if want := prompts.ContractContent(cfg, languages); got != want {
			t.Errorf("languages %v: system prompt has drifted from the contract", languages)
		}
	}
}

// The dial reaches this harness too -- it is the surface the long contract was written for.
func TestBuildPromptFollowsContractVerbosity(t *testing.T) {
	low := helper.DefaultConfig()
	high := helper.DefaultConfig()
	high.ContractVerbosity = helper.ContractVerbosityHigh
	if len(BuildPrompt(high, []string{"go"})) <= len(BuildPrompt(low, []string{"go"})) {
		t.Error(`contract_verbosity "high" did not lengthen the harness system prompt`)
	}
}

func TestToToolDefinitions(t *testing.T) {
	registry := toolapi.NewRegistry()
	registry.Register(&mockTool{})

	defs := toToolDefinitions(registry.List())
	if len(defs) != 1 {
		t.Fatalf("expected 1 tool definition, got %d", len(defs))
	}
	if defs[0].Name != "mock_tool" {
		t.Errorf("expected name 'mock_tool', got %q", defs[0].Name)
	}
	if defs[0].Parameters.Type != "object" {
		t.Errorf("expected parameters type 'object', got %q", defs[0].Parameters.Type)
	}
	if len(defs[0].Parameters.Required) != 1 || defs[0].Parameters.Required[0] != "input" {
		t.Errorf("unexpected required: %v", defs[0].Parameters.Required)
	}
}

func TestToToolDefinitionsEmpty(t *testing.T) {
	defs := toToolDefinitions([]toolapi.Tool{})
	if len(defs) != 0 {
		t.Errorf("expected 0 definitions, got %d", len(defs))
	}
}

func TestNew(t *testing.T) {
	provider := &mockProvider{}
	registry := toolapi.NewRegistry()
	agent := New(provider, registry, helper.DefaultConfig(), []string{"go"})

	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
	if agent.maxIterations != defaultMaxIterations {
		t.Errorf("expected max iterations %d, got %d", defaultMaxIterations, agent.maxIterations)
	}
}

func TestSetMaxIterations(t *testing.T) {
	provider := &mockProvider{}
	registry := toolapi.NewRegistry()
	agent := New(provider, registry, helper.DefaultConfig(), []string{"go"})

	agent.SetMaxIterations(10)
	if agent.maxIterations != 10 {
		t.Errorf("expected 10, got %d", agent.maxIterations)
	}
}

func TestProviderInterface(t *testing.T) {
	var p llm.Provider = &mockProvider{
		response: &llm.ChatResponse{Content: "hello"},
	}
	resp, err := p.Chat(nil, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("expected 'hello', got %q", resp.Content)
	}
}

func TestMessageTypes(t *testing.T) {
	msg := llm.Message{
		Role:    "user",
		Content: "hello",
	}
	if msg.Role != "user" || msg.Content != "hello" {
		t.Errorf("unexpected Message: %+v", msg)
	}
}

func TestToolCall(t *testing.T) {
	tc := llm.ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      "mock_tool",
			Arguments: `{"input": "test"}`,
		},
	}
	if tc.ID != "call_1" || tc.Function.Name != "mock_tool" {
		t.Errorf("unexpected ToolCall: %+v", tc)
	}
}

func TestChatResponse(t *testing.T) {
	resp := llm.ChatResponse{
		Content: "Hello",
		ToolCalls: []llm.ToolCall{
			{ID: "call_1", Type: "function", Function: llm.ToolCallFunction{Name: "tool"}},
		},
	}
	if resp.Content != "Hello" {
		t.Errorf("expected 'Hello', got %q", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
}

func TestToolDefinition(t *testing.T) {
	def := llm.ToolDefinition{
		Name:        "test",
		Description: "test tool",
		Parameters: llm.Parameters{
			Type: "object",
			Properties: map[string]llm.Property{
				"arg": {Type: "string", Description: "an arg"},
			},
			Required: []string{"arg"},
		},
	}
	if def.Name != "test" {
		t.Errorf("unexpected name: %q", def.Name)
	}
	if len(def.Parameters.Required) != 1 {
		t.Errorf("expected 1 required, got %d", len(def.Parameters.Required))
	}
}
