package goscanner

import (
	"os"
	"testing"

	"aracne/internal/topology/golang"
)

// A //go:embed (or any //go: directive) must never be captured as a resource
// description: re-emitting it as a normal comment corrupts the directive.
func TestParseFile_DirectivesAreNotDescriptions(t *testing.T) {
	src := "package demo\n" +
		"\n" +
		"import \"embed\"\n" +
		"\n" +
		"//go:embed static/*\n" +
		"var embeddedStatic embed.FS\n" +
		"\n" +
		"// Real doc.\n" +
		"//go:noinline\n" +
		"func Foo() {}\n"

	path := "zz_directive_demo.go"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	pr, err := ParseFile(path, golang.PackagePath("demo"), "demo", ".")
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range pr.ExternalVars {
		if v.Name == "embeddedStatic" && v.Description != "" {
			t.Fatalf("embeddedStatic description = %q, want empty (a //go:embed directive is not documentation)", v.Description)
		}
	}

	var fooDesc string
	for _, fp := range pr.Functions {
		if fp.Function.Name == "Foo" {
			fooDesc = fp.Function.Description
		}
	}
	if fooDesc != "Real doc." {
		t.Fatalf("Foo description = %q, want \"Real doc.\" (directive line dropped, real doc kept)", fooDesc)
	}
}

func TestIsDirectiveComment(t *testing.T) {
	cases := map[string]bool{
		"go:embed static/*": true,
		"go:build linux":    true,
		"go:noinline":       true,
		"line 12":           true,
		"export Foo":        true,
		" Returns the set:": false, // normal sentence with a trailing colon
		"Foo returns 42":    false,
		"":                  false,
	}
	for in, want := range cases {
		if got := isDirectiveComment(in); got != want {
			t.Errorf("isDirectiveComment(%q) = %v, want %v", in, got, want)
		}
	}
}
