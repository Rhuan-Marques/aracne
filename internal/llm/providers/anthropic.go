package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"aracne/internal/llm"
)

type Anthropic struct {
	apiKey  string
	model   string
	baseURL string
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema llm.Parameters `json:"input_schema"`
}

type anthropicResponse struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
}

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

func (a *Anthropic) Chat(messages []llm.Message, tools []llm.ToolDefinition) (*llm.ChatResponse, error) {
	return a.StreamChat(messages, tools, nil)
}

func (a *Anthropic) StreamChat(messages []llm.Message, tools []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
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

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
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
					if emit != nil {
						emit(llm.StreamEvent{Reasoning: event.Delta.Thinking})
					}
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
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}
	return result, nil
}

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
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

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
		blocks := make([]anthropicContentBlock, 0, len(msg.ToolCalls)+1)
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
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: ""})
		}
		out = append(out, anthropicMessage{Role: role, Content: blocks})
	}
	return joinAnthropicSystem(systemParts), out
}

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
