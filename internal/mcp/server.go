package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Rhuan-Marques/aracne/internal/buildinfo"
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

// maxMessageBytes bounds one JSON-RPC message. A line over it is answered with an error and
// skipped; the connection stays up.
const maxMessageBytes = 10 * 1024 * 1024

// Starts the MCP server's stdio event loop. Reads JSON-RPC 2.0 messages line by line from stdin, dispatches them to the appropriate handler, and writes responses to stdout. Returns any read error other than end of input.
func (s *Server) Serve() error {
	return s.serve(os.Stdin, os.Stdout)
}

// serve is Serve over any stream pair, so the loop itself can be tested.
//
// It reads with a bufio.Reader, not a bufio.Scanner. A Scanner treats a line over its buffer
// as a fatal error: one oversized request -- a runaway tool argument -- ended the loop, and the
// server exited from under the harness, taking every later call with it. Here an oversized
// line is drained, answered with an error, and the next message is read as usual.
func (s *Server) serve(in io.Reader, out io.Writer) error {
	r := bufio.NewReaderSize(in, 64*1024)
	for {
		line, tooLong, err := readMessage(r, maxMessageBytes)
		var reply interface{}
		if tooLong {
			reply = s.errorResponse(nil, codeInvalidRequest,
				fmt.Sprintf("Invalid Request: message exceeds %d bytes", maxMessageBytes))
		} else {
			reply = s.handleMessage(line)
		}
		if reply != nil {
			b, _ := json.Marshal(reply)
			// A failed write means the peer closed the pipe; the next read returns EOF and
			// ends the loop. There is no reply channel left to report it on.
			_, _ = out.Write(b)
			_, _ = out.Write([]byte{'\n'})
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readMessage reads one newline-terminated message. When it runs past limit the rest of the
// line is consumed and discarded, and tooLong reports it. err is io.EOF after the last line.
func readMessage(r *bufio.Reader, limit int) (line []byte, tooLong bool, err error) {
	for {
		chunk, rerr := r.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > limit+1 { // +1: the newline itself
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if rerr == bufio.ErrBufferFull {
			continue
		}
		return line, tooLong, rerr
	}
}

// JSON-RPC 2.0 error codes.
const (
	codeParseError     = -32700 // not valid JSON
	codeInvalidRequest = -32600 // valid JSON, but not a Request object
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602 // MCP uses it for an unknown tool too
)

// handleMessage answers one line: a single request, or a batch (a JSON array of requests), as
// JSON-RPC 2.0 defines them. It returns nil when nothing is owed -- a notification, or a batch
// of them.
//
// Each failure gets the code the spec gives it. Invalid JSON is a parse error; JSON that is not
// a request -- a bare number, no method, a jsonrpc other than "2.0" -- is an invalid request.
// A batch used to be decoded as one object, fail, and come back as a parse error with no id.
func (s *Server) handleMessage(line []byte) interface{} {
	msg := bytes.TrimSpace(line)
	if len(msg) == 0 {
		return nil
	}
	if !json.Valid(msg) {
		return s.errorResponse(nil, codeParseError, "Parse error")
	}
	if msg[0] != '[' {
		if resp := s.handleRequest(msg); resp != nil {
			return resp
		}
		return nil
	}
	var batch []json.RawMessage
	if err := json.Unmarshal(msg, &batch); err != nil || len(batch) == 0 {
		return s.errorResponse(nil, codeInvalidRequest, "Invalid Request: empty batch")
	}
	var replies []*Response
	for _, m := range batch {
		if resp := s.handleRequest(m); resp != nil {
			replies = append(replies, resp)
		}
	}
	if len(replies) == 0 {
		return nil
	}
	return replies
}

// handleRequest validates one Request object and dispatches it.
func (s *Server) handleRequest(raw json.RawMessage) *Response {
	var req struct {
		Request
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return s.errorResponse(nil, codeInvalidRequest, "Invalid Request")
	}
	id := req.ID
	if len(id) > 0 && !validID(id) {
		return s.errorResponse(nil, codeInvalidRequest, "Invalid Request: id must be a string, number or null")
	}
	if req.Method == "" && (req.Result != nil || req.Error != nil) {
		return nil // a client's response to a request this server never sends: nothing to answer
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return s.errorResponse(id, codeInvalidRequest, "Invalid Request")
	}
	return s.dispatch(&req.Request)
}

// validID reports whether a request id is one JSON-RPC allows: a string, a number or null.
func validID(id json.RawMessage) bool {
	switch id[0] {
	case '{', '[', 't', 'f':
		return false
	}
	return true
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
	case "ping":
		// Part of the base protocol, and clients use it to check the server is alive.
		// Answering "method not found" to a liveness probe is a fine way to be judged dead.
		if req.ID == nil {
			return nil
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: struct{}{}}
	default:
		if req.ID != nil {
			return s.errorResponse(req.ID, codeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
		}
		return nil
	}
}

// Handles the MCP initialize request by returning protocol version "2024-11-05", server capabilities (tools supported), and server info (name and version). Returns nil if the request ID is nil.
func (s *Server) handleInitialize(id json.RawMessage) *Response {
	if id == nil {
		return nil
	}
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Result: InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities:    Capabilities{Tools: &struct{}{}},
			ServerInfo:      ServerInfo{Name: "aracne", Version: buildinfo.Version},
		},
	}
}

// Handles the MCP tools/list request: enumerates all registered tools, builds their JSON Schema parameter definitions, and returns the tool list.
func (s *Server) handleListTools(id json.RawMessage) *Response {
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
// BOTH `params` AND `arguments` MAY BE ABSENT, and neither is an error.
//
// A tool whose parameters are all optional -- warnings_list, bug_list,
// node_list_no_description -- is legally called as `{"name":"warnings_list"}`, with no
// `arguments` member at all. That left CallToolParams.Arguments nil, and every tool's Run
// begins with json.Unmarshal(args, &params), which on nil input fails with "unexpected end of
// JSON input". The call came back as a tool error carrying a JSON parser message that told the
// model nothing about what it had done wrong.
//
// Claude Code sends `"arguments": {}` and never hit it, which is exactly why it survived: the
// same class as the `*int` request id in protocol.go, a defect that only shows up on the second
// host. Normalising here rather than in twenty Run methods keeps it fixed for every tool,
// including ones added later.
func (s *Server) handleCallTool(id json.RawMessage, params json.RawMessage) *Response {
	if id == nil {
		return nil
	}
	var call CallToolParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &call); err != nil {
			return s.errorResponse(id, codeInvalidParams, "Invalid params")
		}
	}
	if call.Name == "" {
		return s.errorResponse(id, codeInvalidParams, "Invalid params: missing tool name")
	}
	// An absent `arguments` is an empty object. An explicit `null` already decodes to the
	// four bytes `null`, which unmarshals into a struct cleanly, so only absence needs this.
	if len(call.Arguments) == 0 {
		call.Arguments = json.RawMessage("{}")
	}

	t, ok := s.registry.Get(call.Name)
	if !ok {
		// -32602, not -32601: the METHOD (tools/call) exists; the tool is a bad parameter to it.
		// That is the code the MCP spec gives an unknown tool.
		return s.errorResponse(id, codeInvalidParams, fmt.Sprintf("Unknown tool: %s", call.Name))
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

// Constructs a JSON-RPC 2.0 error response with the given request ID, error code, and message. Returns a Response pointer containing the error payload.
func (s *Server) errorResponse(id json.RawMessage, code int, message string) *Response {
	if id == nil {
		id = json.RawMessage("null")
	}
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	}
}
