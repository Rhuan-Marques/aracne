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

	"aracne/internal/llm"
)

// DeepSeek is the LLM provider implementation for DeepSeek's API. It holds the API key, model name, and base URL for making chat completion requests.
type DeepSeek struct {
	apiKey  string
	model   string
	baseURL string
}

// Represents a request payload sent to the DeepSeek API. Contains the model name, conversation messages, and optional tool definitions for function calling.
type deepSeekRequest struct {
	Model    string        `json:"model"`
	Messages []llm.Message `json:"messages"`
	Tools    []openAITool  `json:"tools,omitempty"`
	Stream   bool          `json:"stream,omitempty"`
}

// Represents the API response from DeepSeek's chat completions endpoint, wrapping an array of Choices each containing a Message with role, content, and optional tool_calls for function-calling workflows.
type deepSeekResponse struct {
	Choices []struct {
		Message struct {
			Role      string             `json:"role"`
			Content   string             `json:"content"`
			ToolCalls []deepSeekToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

// Represents a tool call in DeepSeek API responses containing the call ID, type, and function details (name and JSON arguments). Used to parse and dispatch tool calls from the LLM.
type deepSeekToolCall struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Func struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Creates a new DeepSeek LLM provider instance configured with the DEEPSEEK_API_KEY environment variable, model name, and base URL.
func NewDeepSeek() *DeepSeek {
	return NewDeepSeekWithConfig(os.Getenv("DEEPSEEK_API_KEY"), "deepseek-chat", "https://api.deepseek.com")
}

func NewDeepSeekWithConfig(apiKey, model, baseURL string) *DeepSeek {
	if model == "" {
		model = "deepseek-chat"
	}
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	return &DeepSeek{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
	}
}

// Sends a chat completion request to the DeepSeek API with the given messages and tool definitions. Returns the response content and any tool calls, or an error on failure.
func (d *DeepSeek) Chat(messages []llm.Message, tools []llm.ToolDefinition) (*llm.ChatResponse, error) {
	return d.StreamChatContext(context.Background(), messages, tools, nil)
}

func (d *DeepSeek) StreamChat(messages []llm.Message, tools []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
	return d.StreamChatContext(context.Background(), messages, tools, emit)
}

func (d *DeepSeek) StreamChatContext(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition, emit llm.StreamCallback) (*llm.ChatResponse, error) {
	reqBody := deepSeekRequest{
		Model:    d.model,
		Messages: messages,
		Tools:    toOpenAITools(tools),
		Stream:   true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", d.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
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

		var chunk deepSeekStreamResponse
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
				if tc.Func.Name != "" {
					call.Function.Name = tc.Func.Name
				}
				if tc.Func.Arguments != "" {
					call.Function.Arguments += tc.Func.Arguments
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

type deepSeekStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index int    `json:"index"`
				ID    string `json:"id"`
				Type  string `json:"type"`
				Func  struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}
