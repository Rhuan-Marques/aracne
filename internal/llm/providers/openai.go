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

type OpenAI struct {
	apiKey  string
	model   string
	baseURL string
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
	Stream   bool            `json:"stream,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  llm.Parameters `json:"parameters,omitempty"`
	Arguments   string         `json:"arguments,omitempty"`
}

type openAIToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
}

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

func (o *OpenAI) Chat(messages []llm.Message, tools []llm.ToolDefinition) (*llm.ChatResponse, error) {
	return o.StreamChat(messages, tools, nil)
}

func (o *OpenAI) StreamChat(messages []llm.Message, tools []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
	if o.apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is not configured")
	}

	reqBody := openAIRequest{Model: o.model, Messages: toOpenAIMessages(messages), Tools: toOpenAITools(tools), Stream: true}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", o.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

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
	for i := 0; ; i++ {
		call := toolCalls[i]
		if call == nil {
			break
		}
		result.ToolCalls = append(result.ToolCalls, *call)
	}
	return result, nil
}

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
