//go:build audit

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
)

// noArgTool stands in for node_list_no_description: a real registered tool that takes no
// parameters, so a client has nothing to put in `arguments`.
type noArgTool struct{ saw json.RawMessage }

func (n *noArgTool) Name() string                  { return "no_args" }
func (n *noArgTool) Description() string           { return "takes nothing" }
func (n *noArgTool) Parameters() []tools.Parameter { return nil }
func (n *noArgTool) Run(args json.RawMessage) (string, error) {
	n.saw = args
	return "ran", nil
}

// A-12: CallToolParams.Arguments is `omitempty` raw JSON. MCP permits a client to omit
// `arguments` for a tool that takes none; the server then hands Run a nil payload and every
// tool's json.Unmarshal fails with "unexpected end of JSON input". protocol.go's own doc
// explains that the id was made raw JSON for exactly this class of host-specific breakage.
func TestAudit_ToolsCallWithoutArgumentsStillRuns(t *testing.T) {
	tool := &noArgTool{}
	reg := tools.NewRegistry()
	reg.Register(tool)
	s := NewServer(reg)

	resp := s.dispatch(&Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"no_args"}`),
	})

	body, _ := json.Marshal(resp)
	if strings.Contains(string(body), `"isError":true`) {
		t.Errorf("tools/call with `arguments` omitted came back as a tool error:\n%s", body)
	}
	if tool.saw == nil {
		t.Errorf("the tool received a nil arguments payload; json.Unmarshal(nil, &params) " +
			"fails with \"unexpected end of JSON input\" in every tool in the registry")
	}
}
