package rusttools

import (
	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/topology/rust"
)

type Parameter = tools.Parameter

// RegisterRustTools registers Rust tools (UpdateDescriptionTool and
// NodeListNoDescription) with the tool registry.
func RegisterRustTools(registry *tools.Registry, mgr *rust.RustManager) {
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewNodeListNoDescription(mgr))
}
