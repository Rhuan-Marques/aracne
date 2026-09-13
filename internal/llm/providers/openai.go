package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm"
)

// OpenAI LLM provider configuration holding API key, model name, base URL, and reasoning effort setting.
type OpenAI struct {
	apiKey          string
	model           string
	baseURL         string
	reasoningEffort string
}

// OpenAI API request payload with model, messages, optional tools, streaming, and reasoning effort parameters.
type openAIRequest struct {
	Model           string          `json:"model"`
	Messages        []openAIMessage `json:"messages"`
	Tools           []openAITool    `json:"tools,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

// SetReasoningEffort sets the reasoning effort ("low"/"medium"/"high") sent on
// requests. Only valid for reasoning-capable models; leave empty otherwise.
func (o *OpenAI) SetReasoningEffort(effort string) {
	o.reasoningEffort = effort
}

// OpenAI message with role, content, optional tool call ID and tool calls.
type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

// OpenAI tool definition with type and function schema for model invocation.
type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

// OpenAI function definition with name, description, parameters, and arguments.
type openAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  llm.Parameters `json:"parameters,omitempty"`
	Arguments   string         `json:"arguments,omitempty"`
}

// OpenAI tool invocation from model with ID, type, and function call details.
type openAIToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

// Initializes an OpenAI LLM provider with API key, model, and base URL, using default GPT-4.1 model and environment variables as fallback.
func NewOpenAI(apiKey, model, baseURL string) *OpenAI {
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if model == "" {
		model = "gpt-4.1"
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &OpenAI{apiKey: apiKey, model: model, baseURL: baseURL}
}

// Non-streaming chat completion for OpenAI that delegates to StreamChatContext without callback.
func (o *OpenAI) Chat(messages []llm.Message, tools []llm.ToolDefinition) (*llm.ChatResponse, error) {
	return o.StreamChatContext(context.Background(), messages, tools, nil)
}

// Streaming chat completion for OpenAI that delegates to StreamChatContext with a callback.
func (o *OpenAI) StreamChat(messages []llm.Message, tools []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
	return o.StreamChatContext(context.Background(), messages, tools, emit)
}

// Streams chat completions from OpenAI API with context, emitting content/reasoning tokens and tool calls as they arrive.
func (o *OpenAI) StreamChatContext(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
	if o.apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is not configured")
	}

	reqBody := openAIRequest{Model: o.model, Messages: toOpenAIMessages(messages), Tools: toOpenAITools(tools), Stream: true, ReasoningEffort: o.reasoningEffort}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := providerHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	result := &llm.ChatResponse{}
	toolCalls := map[int]*llm.ToolCall{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk openAIStreamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("unmarshal stream chunk: %w", err)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				result.Content += choice.Delta.Content
				if emit != nil {
					emit(llm.StreamEvent{Content: choice.Delta.Content})
				}
			}
			if choice.Delta.ReasoningContent != "" {
				result.Reasoning += choice.Delta.ReasoningContent
				if emit != nil {
					emit(llm.StreamEvent{Reasoning: choice.Delta.ReasoningContent})
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				call := toolCalls[tc.Index]
				if call == nil {
					call = &llm.ToolCall{Type: "function"}
					toolCalls[tc.Index] = call
				}
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Type != "" {
					call.Type = tc.Type
				}
				if tc.Function.Name != "" {
					call.Function.Name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					call.Function.Arguments += tc.Function.Arguments
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}
	indices := make([]int, 0, len(toolCalls))
	for idx := range toolCalls {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	for _, idx := range indices {
		result.ToolCalls = append(result.ToolCalls, *toolCalls[idx])
	}
	return result, nil
}

// OpenAI streaming response payload with delta content, reasoning, and tool call changes.
type openAIStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

// Transforms generic tool definitions into OpenAI function-type tool format.
func toOpenAITools(tools []llm.ToolDefinition) []openAITool {
	out := make([]openAITool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return out
}

// Converts generic LLM messages to OpenAI format, preserving roles, content, and tool calls.
func toOpenAIMessages(messages []llm.Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for _, msg := range messages {
		converted := openAIMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
		}
		for _, tc := range msg.ToolCalls {
			converted.ToolCalls = append(converted.ToolCalls, openAIToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: openAIFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		out = append(out, converted)
	}
	return out
}
