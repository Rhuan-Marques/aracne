package python

import (
	"testing"

	"aracne/internal/topology/domain"
)

// A recursive function records itself in its own Calls(); it must not appear in
// its own "# CONTEXT:" called-functions list.
func TestFilterFunctionContextExcludesSelf(t *testing.T) {
	m := NewPythonManager(nil)
	gt := &PythonTopology{}
	ctx := &PythonFunctionContext{
		CalledFunctions: []SimplifiedFunction{
			{ID: "mod.recur", Name: "recur", Description: "d", Location: domain.Location{Path: "main.py", StartsAt: 1, EndsAt: 5}},
			{ID: "mod.other", Name: "other", Description: "d", Location: domain.Location{Path: "main.py", StartsAt: 10, EndsAt: 12}},
		},
	}

	m.filterFunctionContext(gt, ctx, domain.DefaultContextFilter(), "mod.recur")

	if len(ctx.CalledFunctions) != 1 {
		t.Fatalf("expected 1 called function after self-exclusion, got %d", len(ctx.CalledFunctions))
	}
	if ctx.CalledFunctions[0].ID != "mod.other" {
		t.Errorf("expected only mod.other to remain, got %q", ctx.CalledFunctions[0].ID)
	}
}

// A self-referential class records itself in its own usage list; it must not
// appear in its own "# CONTEXT:" classes-used.
func TestFilterClassContextExcludesSelf(t *testing.T) {
	m := NewPythonManager(nil)
	gt := &PythonTopology{}
	ctx := &PythonClassContext{
		ClassesUsed: []ClassUsage{
			{ID: "mod.Node", Name: "Node", Description: "d", Location: domain.Location{Path: "main.py", StartsAt: 1, EndsAt: 5}},
			{ID: "mod.Other", Name: "Other", Description: "d", Location: domain.Location{Path: "main.py", StartsAt: 10, EndsAt: 12}},
		},
	}

	m.filterClassContext(gt, ctx, domain.DefaultContextFilter(), "mod.Node")

	if len(ctx.ClassesUsed) != 1 {
		t.Fatalf("expected 1 class used after self-exclusion, got %d", len(ctx.ClassesUsed))
	}
	if ctx.ClassesUsed[0].ID != "mod.Other" {
		t.Errorf("expected only mod.Other to remain, got %q", ctx.ClassesUsed[0].ID)
	}
}
