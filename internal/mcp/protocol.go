package mcp

import "encoding/json"

// Represents an incoming MCP JSON-RPC request with method name, optional ID, and optional parameters.
//
// The ID IS RAW JSON and is echoed back verbatim, which is the only handling JSON-RPC 2.0
// actually requires of a server. It used to be a *int: a client sending the equally legal
// `"id": "1"` failed to unmarshal, so the WHOLE request came back as a parse error with a null
// id the client could not correlate. Claude Code happens to send integers, which is why that
// never surfaced -- and is exactly why it would have surfaced on the next host.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Represents a JSON-RPC 2.0 response with fields for protocol version (JSONRPC), request ID, result payload, and error details. Used for MCP protocol communication.
//
// ID has no omitempty: the spec asks for `"id": null` on an error raised before the id could
// be read, and omitting the member entirely is not the same message.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// JSON-RPC error object with numeric code and message string for MCP error responses.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Represents the parameters of an MCP initialize request, containing protocol version, client capabilities, and client info.
type InitializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ClientInfo      json.RawMessage `json:"clientInfo,omitempty"`
}

// MCP initialize response containing protocol version, server capabilities, and server info.
type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
}

// Represents the capabilities declared by an MCP server during initialization, such as supported tools.
type Capabilities struct {
	Tools *struct{} `json:"tools,omitempty"`
}

// ServerInfo identifies the MCP server implementation during the initialize handshake. It carries the server name and version string.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MCP protocol response type containing a list of Tool definitions. Used in the list_tools response to describe available server capabilities.
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// Represents an MCP tool definition for JSON-RPC communication. Contains the tool's Name, a human-readable Description, and an InputSchema defining the expected parameters. Used when registering tools with the MCP server.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// Defines the JSON Schema structure for an MCP tool's input parameters, specifying the type ("object"), property definitions, and required field names.
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

// Defines a single JSON Schema property with a type string and description, used to describe MCP tool input parameters.
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Items       *Items `json:"items,omitempty"`
}

// Items is the JSON Schema element type of an array property.
type Items struct {
	Type string `json:"type"`
}

// Represents the parameters for an MCP tool call request, holding the tool name and optional JSON arguments.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Represents the result of an MCP tool call, containing a list of ContentBlock entries and an optional IsError flag to indicate failure.
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// MCP response content block with a type (e.g., "text") and optional text content for tool results.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
