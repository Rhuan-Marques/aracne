package helper

import (
	"os"
	"strings"
	"testing"

	"aracne/internal/topology/domain"
)

func applyLang(t *testing.T, path, src, lang string, res domain.Resource) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })
	res.Location.Path = path
	res.Language = lang
	topo := &domain.Topology{Resources: map[string]domain.Resource{res.ID: res}}
	if err := ApplyDescriptions(topo); err != nil {
		t.Fatalf("ApplyDescriptions: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// --- JavaScript / TypeScript: JSDoc ---

func TestApply_JS_InsertsJSDoc(t *testing.T) {
	src := "function render(shape) {\n  return shape.describe();\n}\n"
	out := applyLang(t, "test_js.js", src, "javascript", domain.Resource{
		ID: "render", Kind: domain.ResourceFunction, Name: "render",
		Description: "Renders the shape.", Location: domain.Location{StartsAt: 1},
	})
	if !strings.Contains(out, "/** Renders the shape. */\nfunction render(shape)") {
		t.Fatalf("expected JSDoc above function, got:\n%s", out)
	}
}

func TestApply_TS_ReplacesJSDocIdempotently(t *testing.T) {
	src := "/** Old. */\nfunction render(shape) {\n  return shape.describe();\n}\n"
	out := applyLang(t, "test_ts.ts", src, "typescript", domain.Resource{
		ID: "render", Kind: domain.ResourceFunction, Name: "render",
		Description: "New.", Location: domain.Location{StartsAt: 2},
	})
	if strings.Contains(out, "Old.") {
		t.Fatalf("old JSDoc should be replaced, got:\n%s", out)
	}
	if !strings.Contains(out, "/** New. */\nfunction render") {
		t.Fatalf("expected new JSDoc, got:\n%s", out)
	}
	if strings.Count(out, "/**") != 1 {
		t.Fatalf("expected exactly one JSDoc block, got:\n%s", out)
	}
}

// --- Python: docstrings inside the body ---

func TestApply_Python_InsertsDocstring(t *testing.T) {
	src := "def render(shape):\n    return shape.describe()\n"
	out := applyLang(t, "test_py.py", src, "python", domain.Resource{
		ID: "render", Kind: domain.ResourceFunction, Name: "render",
		Description: "Renders the shape.", Location: domain.Location{StartsAt: 1},
		Properties: map[string]any{"py_body_line": 2, "py_doc_start": 0, "py_doc_end": 0},
	})
	want := "def render(shape):\n    \"\"\"Renders the shape.\"\"\"\n    return shape.describe()\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestApply_Python_ReplacesDocstring(t *testing.T) {
	src := "def render(shape):\n    \"\"\"Old.\"\"\"\n    return shape.describe()\n"
	res := domain.Resource{
		ID: "render", Kind: domain.ResourceFunction, Name: "render",
		Description: "New doc.", Location: domain.Location{StartsAt: 1},
		Properties: map[string]any{"py_body_line": 2, "py_doc_start": 2, "py_doc_end": 2},
	}
	out := applyLang(t, "test_py2.py", src, "python", res)
	if strings.Contains(out, "Old.") {
		t.Fatalf("old docstring should be replaced:\n%s", out)
	}
	want := "def render(shape):\n    \"\"\"New doc.\"\"\"\n    return shape.describe()\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
	// Replacing an existing docstring is idempotent (positions stay valid).
	out2 := applyLang(t, "test_py2.py", out, "python", res)
	if out2 != out {
		t.Fatalf("replace not idempotent:\n1: %q\n2: %q", out, out2)
	}
}

func TestApply_Python_ClassDocstring(t *testing.T) {
	src := "class Shape:\n    pass\n"
	out := applyLang(t, "test_pyc.py", src, "python", domain.Resource{
		ID: "Shape", Kind: domain.ResourceType, Name: "Shape",
		Description: "A shape.", Location: domain.Location{StartsAt: 1},
		Properties: map[string]any{"py_body_line": 2, "py_doc_start": 0, "py_doc_end": 0},
	})
	want := "class Shape:\n    \"\"\"A shape.\"\"\"\n    pass\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestApply_Python_MultiLineSignature(t *testing.T) {
	src := "def render(\n    shape,\n):\n    return shape.describe()\n"
	out := applyLang(t, "test_pym.py", src, "python", domain.Resource{
		ID: "render", Kind: domain.ResourceFunction, Name: "render",
		Description: "Renders.", Location: domain.Location{StartsAt: 1},
		// Scanner reports body[0] (the return) at line 4.
		Properties: map[string]any{"py_body_line": 4, "py_doc_start": 0, "py_doc_end": 0},
	})
	want := "def render(\n    shape,\n):\n    \"\"\"Renders.\"\"\"\n    return shape.describe()\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

// A stale Python location (def line no longer matches the name) is skipped.
func TestApply_Python_SkipsStaleLocation(t *testing.T) {
	src := "def render(shape):\n    return shape.describe()\n"
	out := applyLang(t, "test_pys.py", src, "python", domain.Resource{
		ID: "render", Kind: domain.ResourceFunction, Name: "missing",
		Description: "x.", Location: domain.Location{StartsAt: 1},
		Properties: map[string]any{"py_body_line": 2},
	})
	if out != src {
		t.Fatalf("stale location should be skipped, got:\n%s", out)
	}
}
