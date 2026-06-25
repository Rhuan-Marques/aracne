package helper

import (
	"os"
	"strings"
	"testing"

	"aracne/internal/topology/domain"
)

func applyOne(t *testing.T, path, src, id, name string, kind domain.ResourceKind, startLine int, desc string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })
	topo := &domain.Topology{Resources: map[string]domain.Resource{
		id: {ID: id, Kind: kind, Name: name, Description: desc, Location: domain.Location{StartsAt: startLine, Path: path}},
	}}
	if err := ApplyDescriptions(topo); err != nil {
		t.Fatalf("ApplyDescriptions: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// A doc comment for a var that carries a //go:embed directive must be inserted
// ABOVE the directive, leaving the directive byte-for-byte intact.
func TestApply_PreservesGoEmbedDirective(t *testing.T) {
	src := "package demo\n\nimport \"embed\"\n\n//go:embed static/*\nvar embeddedStatic embed.FS\n"
	out := applyOne(t, "test_embed.go", src, "var:embeddedStatic", "embeddedStatic", domain.ResourceVariable, 6, "Embedded static assets.")

	if !strings.Contains(out, "//go:embed static/*") {
		t.Fatalf("directive was corrupted:\n%s", out)
	}
	if strings.Contains(out, "// go:embed") {
		t.Fatalf("directive gained a space (broken):\n%s", out)
	}
	if i, j := strings.Index(out, "// Embedded static assets."), strings.Index(out, "//go:embed"); i < 0 || i > j {
		t.Fatalf("description should sit above the directive:\n%s", out)
	}
}

// A section banner separated from the declaration by a blank line is not a doc
// comment and must not be replaced.
func TestApply_DoesNotClobberSectionBanner(t *testing.T) {
	src := "package demo\n\n// ----------\n// Defaults\n// ----------\n\nfunc Foo() int {\n\treturn 1\n}\n"
	out := applyOne(t, "test_banner.go", src, "fn:Foo", "Foo", domain.ResourceFunction, 7, "Foo does a thing.")

	if !strings.Contains(out, "// Defaults") || !strings.Contains(out, "// ----------") {
		t.Fatalf("section banner was clobbered:\n%s", out)
	}
	if !strings.Contains(out, "// Foo does a thing.\nfunc Foo()") {
		t.Fatalf("description should be inserted directly above the func:\n%s", out)
	}
}

// A stale/off-by-one location (pointing into the body) must be skipped, never
// inserted inside the function body.
func TestApply_SkipsStaleLocation(t *testing.T) {
	src := "package demo\n\nfunc Foo() int {\n\treturn 1\n}\n"
	// Foo is on line 3; deliberately pass line 4 (the body).
	out := applyOne(t, "test_stale.go", src, "fn:Foo", "Foo", domain.ResourceFunction, 4, "Foo does a thing.")

	if out != src {
		t.Fatalf("stale location should be skipped, but file changed:\n%s", out)
	}
}
