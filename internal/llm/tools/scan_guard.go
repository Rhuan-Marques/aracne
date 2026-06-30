package tools

import (
	"encoding/json"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/scanner"
)

// readScanToolNames is the set of read/grep tool names whose invocation should
// be preceded by a topology scan when read.scan is enabled. Other tools (edit,
// write, bug_*, description maintenance, etc.) are never wrapped.
var readScanToolNames = map[string]bool{
	"read":            true,
	"read_function":   true,
	"read_struct":     true,
	"read_interface":  true,
	"read_named_type": true,
	"read_file":       true,
	"read_package":    true,
	"read_dependency": true,
	"grep":            true,
}

// isReadOrGrepTool reports whether name is a read/grep tool eligible for a
// pre-read scan.
func isReadOrGrepTool(name string) bool {
	return readScanToolNames[name]
}

// scanningTool decorates a read/grep Tool so a topology scan (per read.scan)
// runs before each invocation. The wrapped tool's identity (name, description,
// parameters) is preserved so registries and tool-allow-lists are unaffected.
type scanningTool struct {
	inner Tool
	mgr   *topology.TopologyManager
	reg   *scanner.Registry
	mode  helper.ReadScanMode
}

// Delegates to wrapped tool's name.
func (s *scanningTool) Name() string { return s.inner.Name() }

// Delegates to wrapped tool's description.
func (s *scanningTool) Description() string { return s.inner.Description() }

// Delegates to wrapped tool's parameters.
func (s *scanningTool) Parameters() []Parameter { return s.inner.Parameters() }

// Runs wrapped tool while triggering read scan before returning result.
func (s *scanningTool) Run(args json.RawMessage) (string, error) {
	// Best-effort: a scan failure must not block the read itself.
	_ = s.mgr.RunReadScan(s.reg, s.mode)
	return s.inner.Run(args)
}

// WrapWithReadScan wraps t so that, when read.scan is enabled and t is a
// read/grep tool, a topology scan runs before each call. It returns t unchanged
// for non-read tools or when scanning is disabled (mode none/empty or nil
// registry), so it is safe to apply to every tool in a registry.
func WrapWithReadScan(t Tool, mgr *topology.TopologyManager, reg *scanner.Registry, mode helper.ReadScanMode) Tool {
	if t == nil || mgr == nil || reg == nil || mode == "" || mode == helper.ReadScanNone {
		return t
	}
	if !isReadOrGrepTool(t.Name()) {
		return t
	}
	return &scanningTool{inner: t, mgr: mgr, reg: reg, mode: mode}
}
