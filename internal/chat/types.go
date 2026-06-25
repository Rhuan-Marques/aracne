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

// LLM provider configuration including model, base URL, API key, and thinking budget settings.
type ProviderSettings struct {
	Provider ProviderName `json:"provider"`
	Model    string       `json:"model,omitempty"`
	BaseURL  string       `json:"base_url,omitempty"`
	APIKey   string       `json:"api_key,omitempty"`
	KeyEnv   string       `json:"key_env,omitempty"`
	Contract ProviderName `json:"chat_contract,omitempty"`
	Custom   bool         `json:"custom,omitempty"`
	// ThinkingBudget, when > 0, requests extended reasoning from providers that
	// support it (Anthropic thinking budget; OpenAI reasoning effort on
	// reasoning-capable models). Ignored by providers/models without support.
	ThinkingBudget int `json:"thinking_budget,omitempty"`
}

// Complete chat session with metadata, messages, events, task groups, and pending approvals/questions.
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
	Pinned           bool              `json:"pinned,omitempty"`
	Running          bool              `json:"running,omitempty"`
	Messages         []SessionMessage  `json:"messages"`
	LLMMessages      []llm.Message     `json:"llm_messages"`
	Events           []Event           `json:"events"`
	TaskGroups       []TaskGroup       `json:"task_groups,omitempty"`
	PendingApprovals []PendingApproval `json:"pending_approvals,omitempty"`
	PendingQuestions []PendingQuestion `json:"pending_questions,omitempty"`
	PendingToolCalls []llm.ToolCall    `json:"pending_tool_calls,omitempty"`
}

// Lightweight session summary with metadata, message count, and pending approval/question counts.
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
	Pinned           bool         `json:"pinned,omitempty"`
	MessageCount     int          `json:"message_count"`
	PendingApprovals int          `json:"pending_approvals"`
	PendingQuestions int          `json:"pending_questions"`
}

// Single chat message with role, content, tool execution details, reasoning, and creation timestamp.
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

// Chat event with ID, session, type, timestamp, and arbitrary payload data.
type Event struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Type      string         `json:"type"`
	CreatedAt time.Time      `json:"created_at"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// Pending LLM tool call awaiting user approval with reason and timestamp.
type PendingApproval struct {
	ID        string       `json:"id"`
	CreatedAt time.Time    `json:"created_at"`
	Reason    string       `json:"reason"`
	ToolCall  llm.ToolCall `json:"tool_call"`
}

// Pending user question or prompt from an LLM tool call with optional multiple-choice options.
type PendingQuestion struct {
	ID        string       `json:"id"`
	CreatedAt time.Time    `json:"created_at"`
	Question  string       `json:"question"`
	Options   []string     `json:"options,omitempty"`
	Multiple  bool         `json:"multiple,omitempty"`
	ToolCall  llm.ToolCall `json:"tool_call"`
}

// Represents a batch of agent tasks with shared metadata and processing state.
type TaskGroup struct {
	ID          string      `json:"id"`
	ToolCallID  string      `json:"tool_call_id"`
	Status      string      `json:"status"`
	WorkerCount int         `json:"worker_count"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Tasks       []AgentTask `json:"tasks"`
	Processing  bool        `json:"processing,omitempty"`
}

// Tracks execution state of an agent task: prompt, status, result, messages, and timestamps.
type AgentTask struct {
	ID          string           `json:"id"`
	AgentKind   string           `json:"agent_kind"`
	Prompt      string           `json:"prompt"`
	NeedResult  bool             `json:"need_result"`
	Status      string           `json:"status"`
	Result      string           `json:"result,omitempty"`
	Error       string           `json:"error,omitempty"`
	Messages    []SessionMessage `json:"messages,omitempty"`
	LLMMessages []llm.Message    `json:"llm_messages,omitempty"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
}

// Defines an agent type with name, description, tools list, and optional system prompt.
type AgentKind struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Tools        []string `json:"tools"`
	SystemPrompt string   `json:"system_prompt,omitempty"`
}

// HTTP request payload for initiating a new chat session with agent, mode, and provider settings.
type CreateSessionRequest struct {
	Agent        string           `json:"agent,omitempty"`
	Title        string           `json:"title,omitempty"`
	Content      string           `json:"content,omitempty"`
	Mode         Mode             `json:"mode,omitempty"`
	ApprovalMode ApprovalMode     `json:"approval_mode,omitempty"`
	Provider     ProviderSettings `json:"provider,omitempty"`
}

// Request payload for sending a message in chat mode with content, execution mode, approval settings, and provider configuration.
type SendRequest struct {
	Content      string           `json:"content"`
	Mode         Mode             `json:"mode"`
	ApprovalMode ApprovalMode     `json:"approval_mode"`
	Provider     ProviderSettings `json:"provider"`
}

// Specifies execution parameters for a task workflow: session, type, batch size, and parallelism.
type WorkflowRequest struct {
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	BatchSize int    `json:"batch_size"`
	Parallel  int    `json:"parallel"`
}

type jsonRaw map[string]any

// Converts a Session to a SessionSummary with metadata and message/approval counts.
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
		Pinned:           s.Pinned,
		MessageCount:     len(s.Messages),
		PendingApprovals: len(s.PendingApprovals),
		PendingQuestions: len(s.PendingQuestions),
	}
}
