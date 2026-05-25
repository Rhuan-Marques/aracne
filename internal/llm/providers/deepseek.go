package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"llm-topology/internal/llm"
)

type DeepSeek struct {
	apiKey  string
	model   string
	baseURL string
}

type deepSeekRequest struct {
	Model    string               `json:"model"`
	Messages []llm.Message        `json:"messages"`
	Tools    []llm.ToolDefinition `json:"tools,omitempty"`
}

type deepSeekResponse struct {
	Choices []struct {
		Message struct {
			Role      string             `json:"role"`
			Content   string             `json:"content"`
			ToolCalls []deepSeekToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

type deepSeekToolCall struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Func struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func NewDeepSeek() *DeepSeek {
	return &DeepSeek{
		apiKey:  os.Getenv("DEEPSEEK_API_KEY"),
		model:   "deepseek-chat",
		baseURL: "https://api.deepseek.com",
	}
}

func (d *DeepSeek) Chat(messages []llm.Message, tools []llm.ToolDefinition) (*llm.ChatResponse, error) {
	reqBody := deepSeekRequest{
		Model:    d.model,
		Messages: messages,
		Tools:    tools,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", d.baseURL+"/v1/chat/completions", bytes.NewReader(body))
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var dsResp deepSeekResponse
	if err := json.Unmarshal(respBody, &dsResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(dsResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	msg := dsResp.Choices[0].Message
	result := &llm.ChatResponse{
		Content: msg.Content,
	}

	for _, tc := range msg.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, llm.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: llm.ToolCallFunction{
				Name:      tc.Func.Name,
				Arguments: tc.Func.Arguments,
			},
		})
	}

	return result, nil
}
