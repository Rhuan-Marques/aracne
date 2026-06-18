package gotools

import (
	"strings"
	"testing"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
)

func TestFormatGoFunctionContextVisibility(t *testing.T) {
	ctx := &golang.GoFunctionContext{
		Function: &golang.FunctionCut{Cut: "func Foo() {}"},
		CalledFunctions: []golang.SimplifiedFunction{
			{ID: "pkg.helperA", Description: "does A", Visibility: domain.VisibilityNormal},
			{ID: "pkg.tiny", Visibility: domain.VisibilityFull, Full: &golang.FullBlock{Cut: "func tiny() { return }"}},
		},
		StructsUsed: []golang.StructUsage{
			{
				ID:          "pkg.StructA",
				Description: "a struct",
				Visibility:  domain.VisibilityFull,
				Full:        &golang.FullBlock{Cut: "type StructA struct{ x int }"},
				Methods: []golang.SimplifiedFunction{
					{ID: "pkg.(StructA).M2", Visibility: domain.VisibilityFull, Full: &golang.FullBlock{Cut: "func (s StructA) M2() {}"}},
					{ID: "pkg.(StructA).M1", Description: "m one", Visibility: domain.VisibilityNormal},
				},
			},
		},
		ExtVarsUsed: []golang.SimplifiedExtVar{
			{ID: "pkg.Conf", Visibility: domain.VisibilityFull, Full: &golang.FullBlock{Cut: "var Conf = 2"}},
		},
		Incoming: []domain.ResourceRef{
			{ID: "pkg.Caller", Kind: domain.ResourceFunction, Description: "calls Foo"},
		},
	}

	out := FormatGoFunctionContext(ctx)

	mustContain := []string{
		"# CONTEXT:",
		"## pkg.helperA: does A",       // normal called function
		"## pkg.tiny:",                 // full called function header
		"func tiny() { return }",       // full called function cut
		"## pkg.StructA: a struct",     // elevated struct header
		"type StructA struct{ x int }", // struct cut (Full)
		"func (s StructA) M2() {}",     // full method cut
		"\tpkg.(StructA).M1: m one",    // normal sibling method, indented
		"var Conf = 2",                 // full ext var cut
		"# USED BY:",
		"## pkg.Caller (function): calls Foo",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q\n---\n%s", s, out)
		}
	}

	// Full entries must render before the Normal-only entry (pkg.helperA).
	normalIdx := strings.Index(out, "## pkg.helperA: does A")
	for _, fullMarker := range []string{"type StructA struct{ x int }", "func tiny() { return }", "var Conf = 2"} {
		i := strings.Index(out, fullMarker)
		if i < 0 || i > normalIdx {
			t.Errorf("Full entry %q (idx %d) should precede Normal entry (idx %d)\n---\n%s", fullMarker, i, normalIdx, out)
		}
	}
}

func TestFormatGoFileContextNoPackageImports(t *testing.T) {
	ctx := &golang.GoFileContext{
		File:        &golang.FileCut{Cut: "package x\n\nimport \"fmt\"\n\nfunc Foo() {}"},
		FromPackage: "x",
		Functions:   []golang.SimplifiedFunction{{ID: "x.Foo", Description: "foo"}},
		Imports:     []golang.PackagePath{"fmt"},
	}
	out := FormatGoFileContext(ctx)
	if strings.Contains(out, "## Package:") {
		t.Errorf("file context should not repeat the Package line\n%s", out)
	}
	if strings.Contains(out, "## import") {
		t.Errorf("file context should not repeat import lines\n%s", out)
	}
	if !strings.Contains(out, "## func x.Foo: foo") {
		t.Errorf("file context missing function listing\n%s", out)
	}
}

func TestFormatGoFunctionContextNormalUnchanged(t *testing.T) {
	// Under the default (all-Normal) filter, output carries no fenced neighbor
	// blocks and no USED BY section.
	ctx := &golang.GoFunctionContext{
		Function: &golang.FunctionCut{Cut: "func Foo() {}"},
		CalledFunctions: []golang.SimplifiedFunction{
			{ID: "pkg.helperA", Description: "does A", Visibility: domain.VisibilityNormal},
		},
	}
	out := FormatGoFunctionContext(ctx)
	if !strings.Contains(out, "## pkg.helperA: does A") {
		t.Errorf("missing normal line:\n%s", out)
	}
	if strings.Contains(out, "# USED BY:") {
		t.Errorf("unexpected USED BY section:\n%s", out)
	}
}
