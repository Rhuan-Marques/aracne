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
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/llm"
)

// anthropicHTTP is the client every request goes through.
//
// http.DefaultClient has no timeout at all, and Chat passes context.Background() -- so a
// stalled connection stalled the caller forever, with nothing to cancel it. The budget is
// generous because a long completion with a thinking budget legitimately takes minutes; it is
// a backstop against a dead socket, not a latency target.
var anthropicHTTP = &http.Client{Timeout: 10 * time.Minute}

// Anthropic LLM provider with API key, model, base URL, and thinking budget configuration.
type Anthropic struct {
	apiKey         string
	model          string
	baseURL        string
	thinkingBudget int
}

// Request payload for Anthropic API with model, messages, system prompt, tools, and thinking configuration.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
	Thinking  *anthropicThinking `json:"thinking,omitempty"`
}

// Configuration for extended thinking feature specifying budget tokens for model reasoning.
type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// SetThinkingBudget enables Anthropic extended thinking with the given token
// budget. A non-positive budget leaves thinking disabled.
func (a *Anthropic) SetThinkingBudget(budget int) {
	a.thinkingBudget = budget
}

// Represents a single message in Anthropic API requests with role and content blocks.
type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

// JSON-serializable content block for Anthropic API responses, supporting text and tool use payloads.
type anthropicContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

// Anthropic tool definition with name, description, and input schema.
type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema llm.Parameters `json:"input_schema"`
}

// Response payload from Anthropic API containing content blocks with text, tool calls, and metadata.
type anthropicResponse struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
}

// Initializes an Anthropic LLM provider with API key, model, and base URL, using default Claude Sonnet model and environment variables as fallback.
func NewAnthropic(apiKey, model, baseURL string) *Anthropic {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &Anthropic{apiKey: apiKey, model: model, baseURL: baseURL}
}

// Sends a chat request to Anthropic API without streaming and returns the response.
func (a *Anthropic) Chat(messages []llm.Message, tools []llm.ToolDefinition) (*llm.ChatResponse, error) {
	return a.StreamChatContext(context.Background(), messages, tools, nil)
}

// Sends a streaming chat request to Anthropic API with callback emission and returns the aggregated response.
func (a *Anthropic) StreamChat(messages []llm.Message, tools []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
	return a.StreamChatContext(context.Background(), messages, tools, emit)
}

// Streams messages to Anthropic API with context, tool support, and optional extended thinking, emitting content and reasoning events.
func (a *Anthropic) StreamChatContext(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not configured")
	}

	system, convertedMessages := toAnthropicMessages(messages)
	reqBody := anthropicRequest{Model: a.model, MaxTokens: 8192, System: system, Messages: convertedMessages, Stream: true}
	for _, tool := range tools {
		reqBody.Tools = append(reqBody.Tools, anthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Parameters,
		})
	}
	if a.thinkingBudget > 0 {
		budget := a.thinkingBudget
		if budget < 1024 {
			budget = 1024
		}
		if budget > 32000 {
			budget = 32000
		}
		reqBody.Thinking = &anthropicThinking{Type: "enabled", BudgetTokens: budget}
		// max_tokens must exceed the thinking budget; keep room for the answer.
		reqBody.MaxTokens = budget + 8192
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := anthropicHTTP.Do(req)
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
	// Thinking blocks are accumulated per content-block index, exactly as tool calls are:
	// their text arrives as thinking_delta and their signature as signature_delta, and both
	// have to be carried back on the next request or the API rejects the conversation.
	thinking := map[int]*llm.ThinkingBlock{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		var event anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return nil, fmt.Errorf("unmarshal stream event: %w", err)
		}
		if event.Type == "error" {
			return nil, fmt.Errorf("API error: %s", event.Error.Message)
		}
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "thinking" || event.ContentBlock.Type == "redacted_thinking" {
				thinking[event.Index] = &llm.ThinkingBlock{}
			}
			if event.ContentBlock.Type == "tool_use" {
				call := &llm.ToolCall{ID: event.ContentBlock.ID, Type: "function"}
				call.Function.Name = event.ContentBlock.Name
				toolCalls[event.Index] = call
			} else if event.ContentBlock.Type == "text" && event.ContentBlock.Text != "" {
				result.Content += event.ContentBlock.Text
				if emit != nil {
					emit(llm.StreamEvent{Content: event.ContentBlock.Text})
				}
			}
		case "content_block_delta":
			switch event.Delta.Type {
			case "text_delta":
				if event.Delta.Text != "" {
					result.Content += event.Delta.Text
					if emit != nil {
						emit(llm.StreamEvent{Content: event.Delta.Text})
					}
				}
			case "thinking_delta":
				if event.Delta.Thinking != "" {
					result.Reasoning += event.Delta.Thinking
					if block := thinking[event.Index]; block != nil {
						block.Thinking += event.Delta.Thinking
					}
					if emit != nil {
						emit(llm.StreamEvent{Reasoning: event.Delta.Thinking})
					}
				}
			case "signature_delta":
				if block := thinking[event.Index]; block != nil {
					block.Signature += event.Delta.Signature
				}
			case "input_json_delta":
				if call := toolCalls[event.Index]; call != nil {
					call.Function.Arguments += event.Delta.PartialJSON
				}
			}
		case "content_block_stop":
			if call := toolCalls[event.Index]; call != nil {
				result.ToolCalls = append(result.ToolCalls, *call)
				delete(toolCalls, event.Index)
			}
			// A block with no signature cannot be re-sent, so it is not kept: an
			// unsignable block in the history is a rejected request, where its absence is
			// merely a shorter one.
			if block := thinking[event.Index]; block != nil {
				if block.Signature != "" {
					result.Thinking = append(result.Thinking, *block)
				}
				delete(thinking, event.Index)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}
	return result, nil
}

// Streaming event from Anthropic API with content blocks, deltas for text/thinking/tool input, and error info.
type anthropicStreamEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Converts generic LLM messages to Anthropic format, aggregating system prompts and transforming tool calls/results.
func toAnthropicMessages(messages []llm.Message) (string, []anthropicMessage) {
	var systemParts []string
	out := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "system" {
			if msg.Content != "" {
				systemParts = append(systemParts, msg.Content)
			}
			continue
		}

		if msg.Role == "tool" {
			out = append(out, anthropicMessage{Role: "user", Content: []anthropicContentBlock{{Type: "tool_result", ToolUseID: msg.ToolCallID, Content: msg.Content}}})
			continue
		}

		role := msg.Role
		if role != "assistant" {
			role = "user"
		}
		blocks := make([]anthropicContentBlock, 0, len(msg.ToolCalls)+len(msg.Thinking)+1)
		// Thinking blocks come FIRST, which is where the API puts them and where it expects
		// them back. Sending an assistant turn without them, while extended thinking is on,
		// is rejected -- which is what happened on the turn after every tool call.
		for _, block := range msg.Thinking {
			blocks = append(blocks, anthropicContentBlock{
				Type: "thinking", Thinking: block.Thinking, Signature: block.Signature})
		}
		if msg.Content != "" {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: msg.Content})
		}
		for _, tc := range msg.ToolCalls {
			input := map[string]any{}
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
			}
			blocks = append(blocks, anthropicContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: input})
		}
		if len(blocks) == 0 {
			// An empty text block is rejected ("text content blocks must be non-empty"), and
			// a message with nothing in it says nothing -- so it is dropped rather than sent
			// as a placeholder that fails the whole request.
			continue
		}
		out = append(out, anthropicMessage{Role: role, Content: blocks})
	}
	return joinAnthropicSystem(systemParts), out
}

// Joins multiple system prompt parts with double newlines.
func joinAnthropicSystem(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, part := range parts[1:] {
		result += "\n\n" + part
	}
	return result
}
