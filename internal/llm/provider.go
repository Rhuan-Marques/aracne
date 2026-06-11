package llm

// Represents a message in an LLM chat conversation, containing the role (user/assistant/tool), content text, optional tool call ID, and any tool calls made by the assistant.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// Defines the structure of an LLM tool with Name, Description, and Parameters fields. Used for JSON serialization of tool definitions in MCP protocol responses.
type ToolDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  Parameters `json:"parameters"`
}

// Defines a JSON Schema parameters object with a type, property definitions, and required field list, used for LLM tool parameter definitions.
type Parameters struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

// Describes a JSON Schema property with its type and description, used for LLM tool parameter definitions.
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ToolCall represents a tool invocation request from an LLM response. It contains the call ID, type, and the function details (name and arguments).
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// Represents an LLM tool call with a Name field identifying the tool and an Arguments field containing the JSON-encoded parameters.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatResponse represents the response from an LLM provider. It contains the generated text content (Content) and any tool calls (ToolCalls) the model requested to execute.
type ChatResponse struct {
	Content   string
	Reasoning string
	ToolCalls []ToolCall
}

// Interface that LLM provider implementations must satisfy. Defines a Chat method that takes conversation messages and tool definitions, returning a ChatResponse with content and optional tool calls.
type StreamEvent struct {
	Content   string
	Reasoning string
}

type StreamCallback func(StreamEvent)

type Provider interface {
	Chat(messages []Message, tools []ToolDefinition) (*ChatResponse, error)
	StreamChat(messages []Message, tools []ToolDefinition, emit StreamCallback) (*ChatResponse, error)
}
