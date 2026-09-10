package helper

import (
	"path/filepath"
	"testing"
)

// IsSourceFile's catch-all arm -- reached for a language no scanner owns, like the "multi"
// aggregate -- applies the same test-file exclusions as the named arms. It used to accept every
// *_test.go and test_*.py, which a multi-language project's index-health diff then reported as
// unindexed on every run.
func TestIsSourceFileExcludesTestFilesForAnyLanguageName(t *testing.T) {
	root := t.TempDir()
	for _, language := range []string{"multi", ""} {
		for name, want := range map[string]bool{
			"a.go": true, "a_test.go": false,
			"mod.py": true, "test_mod.py": false,
			"x.java": true, "XTest.java": false,
		} {
			if got := IsSourceFile(root, filepath.Join(root, name), language); got != want {
				t.Errorf("IsSourceFile(%s, language=%q) = %v, want %v", name, language, got, want)
			}
		}
	}
}
