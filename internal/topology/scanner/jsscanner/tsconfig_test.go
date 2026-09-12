package jsscanner

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestStripJSONC(t *testing.T) {
	in := `{
  // line comment with "quotes" and a trailing comma,
  "a": "keep // this and /* this */",
  /* block
     comment */ "b": [1, 2,],
  "c": { "d": "x\"y", },
}`
	var got map[string]any
	if err := json.Unmarshal(stripJSONC([]byte(in)), &got); err != nil {
		t.Fatalf("not JSON after stripping: %v\n%s", err, stripJSONC([]byte(in)))
	}
	if got["a"] != "keep // this and /* this */" {
		t.Errorf("a string was edited: %q", got["a"])
	}
	if d := got["c"].(map[string]any)["d"]; d != `x"y` {
		t.Errorf("escaped quote mishandled: %q", d)
	}
}

// The mapping follows a relative `extends`, resolves `paths` against the effective baseUrl,
// keeps catch-all and package redirects as packages, and treats a prefixed alias with a
// missing target as the project's own (broken) import.
func TestPathAliasResolution(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "configs/base.json"), `{ "compilerOptions": { "paths": { "@app/*": ["src/*"], "*": ["types/*"] } } }`)
	writeFile(t, filepath.Join(dir, "tsconfig.json"), `{ "extends": "./configs/base", "compilerOptions": { "baseUrl": "." } }`)
	writeFile(t, filepath.Join(dir, "src/models/shape.ts"), "export class Shape {}\n")
	writeFile(t, filepath.Join(dir, "types/ext.ts"), "export interface Ext {}\n")
	writeFile(t, filepath.Join(dir, "lib/util.ts"), "export const u = 1;\n")

	a := loadPathAliases(dir, filepath.Join(dir, "src"))
	if a == nil {
		t.Fatal("no aliases loaded")
	}
	for spec, want := range map[string]string{
		"@app/models/shape": filepath.Join(dir, "src/models/shape"),
		"@app/missing":      filepath.Join(dir, "src/missing"), // broken, but still ours
		"ext":               filepath.Join(dir, "types/ext"),
		"lib/util":          filepath.Join(dir, "lib/util"), // baseUrl
	} {
		if got, ok := a.resolve(dir, spec); !ok || got != want {
			t.Errorf("resolve(%q) = %q, %v; want %q", spec, got, ok, want)
		}
	}
	for _, spec := range []string{"react", "lib/nothing", "@scope/pkg"} {
		if got, ok := a.resolve(dir, spec); ok {
			t.Errorf("resolve(%q) = %q; a package must stay a package", spec, got)
		}
	}
}
