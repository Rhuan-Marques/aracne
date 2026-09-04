package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
)

// MCP JSON-RPC server that listens on stdin/stdout and dispatches requests to the registered tool handlers.
type Server struct {
	registry *tools.Registry
}

// Creates a new MCP Server instance with the given tool registry, used to handle JSON-RPC requests over stdio.
func NewServer(registry *tools.Registry) *Server {
	return &Server{registry: registry}
}

// Starts the MCP server's stdio event loop. Reads JSON-RPC 2.0 messages line by line from stdin, dispatches them to the appropriate handler, and writes responses to stdout. Returns any scanner error encountered.
func (s *Server) Serve() error {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.writeError(nil, -32700, "Parse error")
			continue
		}

		resp := s.dispatch(&req)
		if resp != nil {
			b, _ := json.Marshal(resp)
			os.Stdout.Write(b)
			os.Stdout.Write([]byte{'\n'})
		}
	}
	return sc.Err()
}

// Routes incoming MCP JSON-RPC requests to the appropriate handler based on method name (initialize, tools/list, tools/call).
func (s *Server) dispatch(req *Request) *Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.ID)
	case "tools/list":
		return s.handleListTools(req.ID)
	case "tools/call":
		return s.handleCallTool(req.ID, req.Params)
	default:
		if req.ID != nil {
			return s.errorResponse(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
		}
		return nil
	}
}

// Handles the MCP initialize request by returning protocol version "2024-11-05", server capabilities (tools supported), and server info (name and version). Returns nil if the request ID is nil.
func (s *Server) handleInitialize(id *int) *Response {
	if id == nil {
		return nil
	}
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Result: InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities:    Capabilities{Tools: &struct{}{}},
			ServerInfo:      ServerInfo{Name: "aracne", Version: "1.0.0"},
		},
	}
}

// Handles the MCP tools/list request: enumerates all registered tools, builds their JSON Schema parameter definitions, and returns the tool list.
func (s *Server) handleListTools(id *int) *Response {
	if id == nil {
		return nil
	}
	allTools := s.registry.List()
	list := make([]Tool, 0, len(allTools))
	for _, t := range allTools {
		props := make(map[string]Property)
		var required []string
		for _, p := range t.Parameters() {
			prop := Property{Type: p.Type, Description: p.Description}
			if p.Items != "" {
				prop.Items = &Items{Type: p.Items}
			}
			props[p.Name] = prop
			if p.Required {
				required = append(required, p.Name)
			}
		}
		list = append(list, Tool{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: InputSchema{
				Type:       "object",
				Properties: props,
				Required:   required,
			},
		})
	}
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  ListToolsResult{Tools: list},
	}
}

// Handles the MCP tools/call request: looks up the tool by name in the registry, executes it with the provided arguments, and returns the result or an error response.
func (s *Server) handleCallTool(id *int, params json.RawMessage) *Response {
	if id == nil {
		return nil
	}
	var call CallToolParams
	if err := json.Unmarshal(params, &call); err != nil {
		return s.errorResponse(id, -32602, "Invalid params")
	}

	t, ok := s.registry.Get(call.Name)
	if !ok {
		return s.errorResponse(id, -32601, fmt.Sprintf("Tool not found: %s", call.Name))
	}

	result, err := t.Run(call.Arguments)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      id,
			Result: CallToolResult{
				Content: []ContentBlock{{Type: "text", Text: err.Error()}},
				IsError: true,
			},
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Result: CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: result}},
		},
	}
}

// Writes a JSON-RPC error response to stdout. Constructs the error response from the request ID, error code, and message, then marshals and prints it.
func (s *Server) writeError(id *int, code int, message string) {
	resp := s.errorResponse(id, code, message)
	b, _ := json.Marshal(resp)
	os.Stdout.Write(b)
	os.Stdout.Write([]byte{'\n'})
}

// Constructs a JSON-RPC 2.0 error response with the given request ID, error code, and message. Returns a Response pointer containing the error payload.
func (s *Server) errorResponse(id *int, code int, message string) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	}
}
