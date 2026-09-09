package llm

import "context"

// Represents a message in an LLM chat conversation, containing the role (user/assistant/tool), content text, optional tool call ID, and any tool calls made by the assistant.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	// Thinking carries an assistant turn's extended-reasoning blocks so they can be sent
	// back on the NEXT request. Anthropic requires that when extended thinking is on and the
	// turn made a tool call: the assistant message has to arrive with its thinking blocks
	// and their signatures intact, or the request is rejected. Dropping them is why the
	// thinking-budget chat agents -- bug-hunter, bug-judge, bug-solver -- could not survive
	// their own first tool call. Providers without the concept ignore the field.
	Thinking []ThinkingBlock `json:"thinking,omitempty"`
}

// ThinkingBlock is one extended-reasoning block, with the provider's signature over it. The
// signature is opaque and must be round-tripped byte for byte; a block without one cannot be
// re-sent.
type ThinkingBlock struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
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
	Type        string    `json:"type"`
	Description string    `json:"description"`
	Items       *ItemSpec `json:"items,omitempty"`
}

// ItemSpec is the JSON Schema element type of an array property.
type ItemSpec struct {
	Type string `json:"type"`
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
	// Thinking is the reasoning as the provider structured it, to be carried on the
	// assistant Message of the next request. Reasoning above is the same text flattened for
	// display; only this can be sent back.
	Thinking  []ThinkingBlock
	ToolCalls []ToolCall
}

// Interface that LLM provider implementations must satisfy. Defines a Chat method that takes conversation messages and tool definitions, returning a ChatResponse with content and optional tool calls.
type StreamEvent struct {
	Content   string
	Reasoning string
}

type StreamCallback func(StreamEvent)

// LLM interface for chat and streamed chat operations with tool support.
type Provider interface {
	Chat(messages []Message, tools []ToolDefinition) (*ChatResponse, error)
	StreamChat(messages []Message, tools []ToolDefinition, emit StreamCallback) (*ChatResponse, error)
}

// Provides streaming chat responses with context using tools and messages.
type ContextProvider interface {
	StreamChatContext(ctx context.Context, messages []Message, tools []ToolDefinition, emit StreamCallback) (*ChatResponse, error)
}
