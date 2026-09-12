package topogrep

import (
	"reflect"
	"sort"
	"testing"
)

// The intercepted `rg -t NAME` is answered with this table for exactly these names
// (shellcmd.exactTypes), so for them it must be ripgrep's own file set. Each want is taken from
// `rg --type-list` (ripgrep 14.1.1); every other name passes through to the real rg.
func TestTypeTableMatchesRipgrepForTheTypesItAnswers(t *testing.T) {
	for name, want := range map[string][]string{
		"go":         {".go"},
		"py":         {".py", ".pyi"},
		"python":     {".py", ".pyi"},
		"js":         {".cjs", ".js", ".jsx", ".mjs", ".vue"},
		"ts":         {".cts", ".mts", ".ts", ".tsx"},
		"typescript": {".cts", ".mts", ".ts", ".tsx"},
		"rust":       {".rs"},
		"yaml":       {".yaml", ".yml"},
	} {
		exts, err := typeExtensions(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var got []string
		for e := range exts {
			got = append(got, e)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("type %s = %q, rg's is %q", name, got, want)
		}
	}
	// `arac grep -type cpp` must reach the headers: rg's cpp takes .h, .hxx and .inl too.
	cpp, _ := typeExtensions("cpp")
	for _, e := range []string{".h", ".hxx", ".inl", ".cpp", ".hpp"} {
		if !cpp[e] {
			t.Errorf("type cpp misses %s", e)
		}
	}
}
