package chat

import (
	"time"

	"aracne/internal/llm"
)

type Mode string

type ApprovalMode string

type ProviderName string

const (
	ModePlan  Mode = "plan"
	ModeBuild Mode = "build"

	ApprovalManual ApprovalMode = "manual"
	ApprovalAuto   ApprovalMode = "auto"
	ApprovalAlways ApprovalMode = "always"

	ProviderOpenAI    ProviderName = "openai"
	ProviderAnthropic ProviderName = "anthropic"
	ProviderDeepSeek  ProviderName = "deepseek"
)

type ProviderSettings struct {
	Provider ProviderName `json:"provider"`
	Model    string       `json:"model,omitempty"`
	BaseURL  string       `json:"base_url,omitempty"`
	APIKey   string       `json:"api_key,omitempty"`
	KeyEnv   string       `json:"key_env,omitempty"`
	Contract ProviderName `json:"chat_contract,omitempty"`
	Custom   bool         `json:"custom,omitempty"`
}

type Session struct {
	ID               string            `json:"id"`
	Title            string            `json:"title"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Mode             Mode              `json:"mode"`
	ApprovalMode     ApprovalMode      `json:"approval_mode"`
	Provider         ProviderName      `json:"provider"`
	Model            string            `json:"model,omitempty"`
	Agent            string            `json:"agent"`
	Messages         []SessionMessage  `json:"messages"`
	LLMMessages      []llm.Message     `json:"llm_messages"`
	Events           []Event           `json:"events"`
	PendingApprovals []PendingApproval `json:"pending_approvals,omitempty"`
	PendingQuestions []PendingQuestion `json:"pending_questions,omitempty"`
}

type SessionSummary struct {
	ID               string       `json:"id"`
	Title            string       `json:"title"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	Mode             Mode         `json:"mode"`
	ApprovalMode     ApprovalMode `json:"approval_mode"`
	Provider         ProviderName `json:"provider"`
	Model            string       `json:"model,omitempty"`
	Agent            string       `json:"agent"`
	MessageCount     int          `json:"message_count"`
	PendingApprovals int          `json:"pending_approvals"`
	PendingQuestions int          `json:"pending_questions"`
}

type SessionMessage struct {
	ID         string    `json:"id"`
	Role       string    `json:"role"`
	Content    string    `json:"content,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Status     string    `json:"status,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	ToolInput  jsonRaw   `json:"tool_input,omitempty"`
	ToolOutput string    `json:"tool_output,omitempty"`
	Reasoning  string    `json:"reasoning,omitempty"`
}

type Event struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Type      string         `json:"type"`
	CreatedAt time.Time      `json:"created_at"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type PendingApproval struct {
	ID        string       `json:"id"`
	CreatedAt time.Time    `json:"created_at"`
	Reason    string       `json:"reason"`
	ToolCall  llm.ToolCall `json:"tool_call"`
}

type PendingQuestion struct {
	ID        string       `json:"id"`
	CreatedAt time.Time    `json:"created_at"`
	Question  string       `json:"question"`
	Options   []string     `json:"options,omitempty"`
	Multiple  bool         `json:"multiple,omitempty"`
	ToolCall  llm.ToolCall `json:"tool_call"`
}

type CreateSessionRequest struct {
	Agent        string           `json:"agent,omitempty"`
	Title        string           `json:"title,omitempty"`
	Content      string           `json:"content,omitempty"`
	Mode         Mode             `json:"mode,omitempty"`
	ApprovalMode ApprovalMode     `json:"approval_mode,omitempty"`
	Provider     ProviderSettings `json:"provider,omitempty"`
}

type SendRequest struct {
	Content      string           `json:"content"`
	Mode         Mode             `json:"mode"`
	ApprovalMode ApprovalMode     `json:"approval_mode"`
	Provider     ProviderSettings `json:"provider"`
}

type WorkflowRequest struct {
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	BatchSize int    `json:"batch_size"`
	Parallel  int    `json:"parallel"`
}

type jsonRaw map[string]any

func summarizeSession(s *Session) SessionSummary {
	return SessionSummary{
		ID:               s.ID,
		Title:            s.Title,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
		Mode:             s.Mode,
		ApprovalMode:     s.ApprovalMode,
		Provider:         s.Provider,
		Model:            s.Model,
		Agent:            s.Agent,
		MessageCount:     len(s.Messages),
		PendingApprovals: len(s.PendingApprovals),
		PendingQuestions: len(s.PendingQuestions),
	}
}
