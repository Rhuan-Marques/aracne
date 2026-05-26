package agent

import (
	"encoding/json"
	"fmt"

	"llm-topology/internal/llm"
	"llm-topology/internal/llm/tools"
)

const maxIterations = 20

type Agent struct {
	provider llm.Provider
	registry *tools.Registry
	messages []llm.Message
}

func New(provider llm.Provider, registry *tools.Registry, language string) *Agent {
	return &Agent{
		provider: provider,
		registry: registry,
		messages: []llm.Message{
			{
				Role:    "system",
				Content: BuildPrompt(language),
			},
		},
	}
}

func (a *Agent) Run(input string) error {
	a.messages = append(a.messages, llm.Message{Role: "user", Content: input})

	for i := 0; i < maxIterations; i++ {
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

	return fmt.Errorf("exceeded max iterations (%d)", maxIterations)
}

func (a *Agent) RunSubAgent(systemPrompt, input string, toolMap map[string]tools.Tool) (string, error) {
	subRegistry := tools.NewRegistry()
	for _, t := range toolMap {
		subRegistry.Register(t)
	}

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: input},
	}

	for i := 0; i < maxIterations; i++ {
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

	return "", fmt.Errorf("sub-agent exceeded max iterations (%d)", maxIterations)
}

func toToolDefinitions(toolList []tools.Tool) []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(toolList))
	for _, t := range toolList {
		props := make(map[string]llm.Property)
		var required []string
		for _, p := range t.Parameters() {
			props[p.Name] = llm.Property{
				Type:        p.Type,
				Description: p.Description,
			}
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
