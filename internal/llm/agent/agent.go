package agent

import (
	"encoding/json"
	"fmt"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm"
	"github.com/Rhuan-Marques/aracne/internal/llm/toolapi"
)

// Default maximum number of tool-calling iterations (20) the agent loop can execute before returning.
const defaultMaxIterations = 20

// Represents the AI agent loop that manages LLM interactions, tool execution, and conversation history. Key fields: provider (LLM API client), registry (available tools), messages (conversation history), and maxIterations (tool-call loop limit).
type Agent struct {
	provider      llm.Provider
	registry      *toolapi.Registry
	messages      []llm.Message
	maxIterations int
}

// Creates a new Agent with the given LLM provider, tool registry, project config and topology
// languages. Initializes the system prompt and sets the default max iterations (20).
//
// It takes the config rather than a bare language string because the system prompt IS the
// project's contract now (see BuildPrompt): the mode decides which capabilities it may name and
// contract_verbosity decides how much it says about them, and both live in the config.
func New(provider llm.Provider, registry *toolapi.Registry, cfg *helper.Config, languages []string) *Agent {
	return &Agent{
		provider:      provider,
		registry:      registry,
		messages:      []llm.Message{{Role: "system", Content: BuildPrompt(cfg, languages)}},
		maxIterations: defaultMaxIterations,
	}
}

// Sets the maximum number of tool-call iterations allowed in the agent loop before returning control.
func (a *Agent) SetMaxIterations(n int) {
	a.maxIterations = n
}

// Main agent loop: sends messages to the LLM provider, processes tool calls, and appends results to the conversation until a final response without tool calls is received or max iterations are exceeded.
func (a *Agent) Run(input string) error {
	a.messages = append(a.messages, llm.Message{Role: "user", Content: input})

	for i := 0; i < a.maxIterations; i++ {
		toolDefs := toToolDefinitions(a.registry.List())

		resp, err := a.provider.Chat(a.messages, toolDefs)
		if err != nil {
			return fmt.Errorf("LLM call failed: %w", err)
		}

		if len(resp.ToolCalls) == 0 {
			fmt.Println(resp.Content)
			a.messages = append(a.messages, llm.Message{Role: "assistant", Content: resp.Content})
			return nil
		}

		asstMsg := llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
			// Carried so the next request can send them back. A provider with extended
			// thinking on rejects an assistant turn that made a tool call and arrives
			// without its thinking blocks; providers without the concept ignore them.
			Thinking: resp.Thinking,
		}
		a.messages = append(a.messages, asstMsg)

		for _, tc := range resp.ToolCalls {
			tool, ok := a.registry.Get(tc.Function.Name)
			if !ok {
				a.messages = append(a.messages, llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("Error: unknown tool '%s'", tc.Function.Name),
					ToolCallID: tc.ID,
				})
				continue
			}

			result, err := tool.Run(json.RawMessage(tc.Function.Arguments))
			if err != nil {
				a.messages = append(a.messages, llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("Error: %s", err),
					ToolCallID: tc.ID,
				})
			} else {
				a.messages = append(a.messages, llm.Message{
					Role:       "tool",
					Content:    result,
					ToolCallID: tc.ID,
				})
			}
		}
	}

	return fmt.Errorf("exceeded max iterations (%d)", a.maxIterations)
}

// Runs a sub-agent loop with a separate tool set and system prompt. Delegates to the LLM provider, processes tool calls, and returns the final response or an error if max iterations are exceeded.
func (a *Agent) RunSubAgent(systemPrompt, input string, toolMap map[string]toolapi.Tool) (string, error) {
	subRegistry := toolapi.NewRegistry()
	for _, t := range toolMap {
		subRegistry.Register(t)
	}

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: input},
	}

	for i := 0; i < a.maxIterations; i++ {
		toolDefs := toToolDefinitions(subRegistry.List())

		resp, err := a.provider.Chat(messages, toolDefs)
		if err != nil {
			return "", fmt.Errorf("LLM call failed: %w", err)
		}

		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
			Thinking:  resp.Thinking,
		})

		for _, tc := range resp.ToolCalls {
			tool, ok := subRegistry.Get(tc.Function.Name)
			if !ok {
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("Error: unknown tool '%s'", tc.Function.Name),
					ToolCallID: tc.ID,
				})
				continue
			}

			result, err := tool.Run(json.RawMessage(tc.Function.Arguments))
			if err != nil {
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("Error: %s", err),
					ToolCallID: tc.ID,
				})
			} else {
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    result,
					ToolCallID: tc.ID,
				})
			}
		}
	}

	return "", fmt.Errorf("sub-agent exceeded max iterations (%d)", a.maxIterations)
}

// Converts a slice of Tool instances into LLM ToolDefinition objects by extracting each tool's name, description, and typed parameter schema with required flags. Returns the definitions slice.
func toToolDefinitions(toolList []toolapi.Tool) []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(toolList))
	for _, t := range toolList {
		props := make(map[string]llm.Property)
		var required []string
		for _, p := range t.Parameters() {
			prop := llm.Property{
				Type:        p.Type,
				Description: p.Description,
			}
			if p.Items != "" {
				prop.Items = &llm.ItemSpec{Type: p.Items}
			}
			props[p.Name] = prop
			if p.Required {
				required = append(required, p.Name)
			}
		}
		defs = append(defs, llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: llm.Parameters{
				Type:       "object",
				Properties: props,
				Required:   required,
			},
		})
	}
	return defs
}
