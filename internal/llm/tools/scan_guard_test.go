package tools

import (
	"encoding/json"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/scanner"
)

type fakeScanTool struct {
	name string
	ran  bool
}

func (f *fakeScanTool) Name() string                        { return f.name }
func (f *fakeScanTool) Description() string                 { return "fake" }
func (f *fakeScanTool) Parameters() []Parameter             { return nil }
func (f *fakeScanTool) Run(json.RawMessage) (string, error) { f.ran = true; return "ok", nil }

func TestWrapWithReadScan_Decision(t *testing.T) {
	mgr := topology.New()
	reg := scanner.NewRegistry()
	read := &fakeScanTool{name: "read_file"}

	// Disabled (none / empty / nil registry) leaves read tools unwrapped.
	if WrapWithReadScan(read, mgr, reg, helper.ReadScanNone) != Tool(read) {
		t.Fatal("none mode must return the tool unchanged")
	}
	if WrapWithReadScan(read, mgr, reg, "") != Tool(read) {
		t.Fatal("empty mode must return the tool unchanged")
	}
	if WrapWithReadScan(read, mgr, nil, helper.ReadScanDefault) != Tool(read) {
		t.Fatal("nil registry must return the tool unchanged")
	}

	// Non-read tools are never wrapped, even when scanning is enabled.
	edit := &fakeScanTool{name: "edit"}
	if WrapWithReadScan(edit, mgr, reg, helper.ReadScanFull) != Tool(edit) {
		t.Fatal("non-read tool must not be wrapped")
	}

	// Read/grep tools get wrapped when enabled, preserving identity metadata.
	for _, name := range []string{"read", "read_function", "read_struct", "read_interface", "read_named_type", "read_file", "read_package", "read_dependency", "grep"} {
		ft := &fakeScanTool{name: name}
		got := WrapWithReadScan(ft, mgr, reg, helper.ReadScanDefault)
		if got == Tool(ft) {
			t.Fatalf("%s must be wrapped when scanning enabled", name)
		}
		if _, ok := got.(*scanningTool); !ok {
			t.Fatalf("%s: expected *scanningTool, got %T", name, got)
		}
		if got.Name() != name {
			t.Fatalf("wrapped name = %q, want %q", got.Name(), name)
		}
	}
}

func TestWrapWithReadScan_RunIsBestEffort(t *testing.T) {
	mgr := topology.New()        // no db => scan resolves to a no-op/error path
	reg := scanner.NewRegistry() // no scanners registered => scan errors out
	read := &fakeScanTool{name: "read_file"}
	wrapped := WrapWithReadScan(read, mgr, reg, helper.ReadScanDefault)

	out, err := wrapped.Run(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("wrapped Run must not surface scan errors: %v", err)
	}
	if out != "ok" || !read.ran {
		t.Fatalf("wrapped Run must still delegate to the inner tool (out=%q ran=%v)", out, read.ran)
	}
}
