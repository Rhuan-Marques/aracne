package golang

import (
	"testing"

	"aracne/internal/topology/domain"
)

// A recursive function records itself in its own Calls(); it must not appear in
// its own "# CONTEXT:" called-functions list.
func TestFilterFunctionContextExcludesSelf(t *testing.T) {
	m := NewGoManager(nil)
	gt := &GolangTopology{}
	ctx := &GoFunctionContext{
		CalledFunctions: []SimplifiedFunction{
			{ID: "pkg.Recur", Name: "Recur", Description: "d", Location: domain.Location{Path: "main.go", StartsAt: 1, EndsAt: 5}},
			{ID: "pkg.Other", Name: "Other", Description: "d", Location: domain.Location{Path: "main.go", StartsAt: 10, EndsAt: 12}},
		},
	}

	m.filterFunctionContext(gt, ctx, domain.DefaultContextFilter(), "pkg.Recur")

	if len(ctx.CalledFunctions) != 1 {
		t.Fatalf("expected 1 called function after self-exclusion, got %d", len(ctx.CalledFunctions))
	}
	if ctx.CalledFunctions[0].ID != "pkg.Other" {
		t.Errorf("expected only pkg.Other to remain, got %q", ctx.CalledFunctions[0].ID)
	}
}

// A self-referential struct (e.g. type Node struct { Next *Node }) records itself
// in its own usage list; it must not appear in its own "# CONTEXT:" structs-used.
func TestFilterStructContextExcludesSelf(t *testing.T) {
	m := NewGoManager(nil)
	gt := &GolangTopology{}
	ctx := &GoStructContext{
		StructsUsed: []StructUsage{
			{ID: "pkg.Node", Name: "Node", Description: "d", Location: domain.Location{Path: "main.go", StartsAt: 1, EndsAt: 5}},
			{ID: "pkg.Other", Name: "Other", Description: "d", Location: domain.Location{Path: "main.go", StartsAt: 10, EndsAt: 12}},
		},
	}

	m.filterStructContext(gt, ctx, domain.DefaultContextFilter(), "pkg.Node")

	if len(ctx.StructsUsed) != 1 {
		t.Fatalf("expected 1 struct used after self-exclusion, got %d", len(ctx.StructsUsed))
	}
	if ctx.StructsUsed[0].ID != "pkg.Other" {
		t.Errorf("expected only pkg.Other to remain, got %q", ctx.StructsUsed[0].ID)
	}
}
