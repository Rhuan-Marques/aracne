package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"llm-topology/internal/llm/tools"
	"llm-topology/internal/topology"
)

type Server struct {
	mgr *topology.TopologyManager
}

func NewServer(mgr *topology.TopologyManager) *Server {
	return &Server{mgr: mgr}
}

func (s *Server) Serve() error {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for sc.Scan() {
		line := sc.Text()
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
			fmt.Println(string(b))
		}
	}
	return sc.Err()
}

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
			ServerInfo:      ServerInfo{Name: "llm-topology", Version: "1.0.0"},
		},
	}
}

func (s *Server) handleListTools(id *int) *Response {
	allTools := s.buildTools()
	list := make([]Tool, 0, len(allTools))
	for _, t := range allTools {
		props := make(map[string]Property)
		var required []string
		for _, p := range t.Parameters() {
			props[p.Name] = Property{Type: p.Type, Description: p.Description}
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

func (s *Server) handleCallTool(id *int, params json.RawMessage) *Response {
	var call CallToolParams
	if err := json.Unmarshal(params, &call); err != nil {
		return s.errorResponse(id, -32602, "Invalid params")
	}

	allTools := s.buildTools()
	t, ok := allTools[call.Name]
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

func (s *Server) buildTools() map[string]tools.Tool {
	return map[string]tools.Tool{
		"ls":             &tools.Ls{},
		"read":           &tools.Read{},
		"read_function":  tools.NewReadFunction(s.mgr),
		"read_struct":    tools.NewReadStruct(s.mgr),
		"edit":           tools.NewEdit(s.mgr),
	}
}

func (s *Server) writeError(id *int, code int, message string) {
	resp := s.errorResponse(id, code, message)
	b, _ := json.Marshal(resp)
	fmt.Println(string(b))
}

func (s *Server) errorResponse(id *int, code int, message string) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	}
}
