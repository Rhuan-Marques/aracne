package shellcmd

import "testing"

func TestConsumerClassification(t *testing.T) {
	for word, want := range map[string]ConsumerClass{
		"head": ConsumerCapper, "tail": ConsumerCapper,
		"grep": ConsumerLineFilter, "grep -v": ConsumerLineFilter, "rg": ConsumerLineFilter,
		"cat": ConsumerLineFilter,
		// Transforms: reorder or rewrite lines, breaking annotation-to-match adjacency.
		"sort": ConsumerOpaque, "uniq": ConsumerOpaque, "cut": ConsumerOpaque,
		"sed": ConsumerOpaque, "awk": ConsumerOpaque,
		// Opaque: each would silently change what the pipeline means.
		"wc": ConsumerOpaque, "xargs": ConsumerOpaque, "tee": ConsumerOpaque,
		"jq": ConsumerOpaque, "python3": ConsumerOpaque, "sh": ConsumerOpaque,
		"": ConsumerOpaque, "somethingelse": ConsumerOpaque,
	} {
		first := word
		if i := len(word); i > 0 {
			for j, r := range word {
				if r == ' ' {
					first = word[:j]
					break
				}
			}
		}
		if got := ClassifyConsumer(first); got != want {
			t.Errorf("ClassifyConsumer(%q) = %v, want %v", first, got, want)
		}
	}
}

// The counter is the one that looks harmless and is not: aracne's grep answer interleaves
// `# path:a-b` annotation lines with the matches, so a count of it is a count of a different
// thing -- returned as a bare number with nothing to reveal the substitution.
func TestCountersAreNotLinePreserving(t *testing.T) {
	if ConsumerPreservesLines("wc") {
		t.Error("wc must not be treated as line-preserving")
	}
	if !ConsumerPreservesLines("head") || !ConsumerPreservesLines("grep") {
		t.Error("cappers and filters must be line-preserving")
	}
}

// Paths and .exe suffixes must not smuggle an opaque consumer past the allow-list.
func TestConsumerClassificationUnwrapsPaths(t *testing.T) {
	for _, w := range []string{"/usr/bin/head", "/bin/grep", "HEAD"} {
		if !ConsumerPreservesLines(w) {
			t.Errorf("%q should classify by its base name", w)
		}
	}
	if ConsumerPreservesLines("/usr/bin/wc") {
		t.Error("/usr/bin/wc must still be opaque")
	}
}
