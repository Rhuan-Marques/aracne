package shellcmd

import (
	"strings"
	"testing"
)

func TestConsumerClassification(t *testing.T) {
	for command, want := range map[string]ConsumerClass{
		"head": ConsumerCapper, "tail": ConsumerCapper, "head -30": ConsumerCapper,
		"tail -n +5": ConsumerCapper, "head -20": ConsumerCapper,
		"grep": ConsumerLineFilter, "grep -v": ConsumerLineFilter, "rg": ConsumerLineFilter,
		"cat": ConsumerLineFilter, "grep -v _test": ConsumerLineFilter,
		"grep -i -n pat": ConsumerLineFilter, "cat -n": ConsumerLineFilter,
		// Transforms: reorder or rewrite lines, breaking annotation-to-match adjacency.
		"sort": ConsumerOpaque, "uniq": ConsumerOpaque, "cut": ConsumerOpaque,
		"sed": ConsumerOpaque, "awk": ConsumerOpaque,
		// Opaque: each would silently change what the pipeline means.
		"wc": ConsumerOpaque, "xargs": ConsumerOpaque, "tee": ConsumerOpaque,
		"jq": ConsumerOpaque, "python3": ConsumerOpaque, "sh": ConsumerOpaque,
		"": ConsumerOpaque, "somethingelse": ConsumerOpaque,
	} {
		if got := ClassifyConsumer(strings.Fields(command)); got != want {
			t.Errorf("ClassifyConsumer(%q) = %v, want %v", command, got, want)
		}
	}
}

// A FLAG CAN MAKE A FILTER A COUNTER, and the word alone cannot see it. Each of these is the
// same objection the file makes to `wc -l`, spelled as an option: the stage stops emitting the
// lines it was given and emits something derived from them, so aracne's extra rows change the
// answer. `grep … | grep -c util` answered 2 for the real pipeline and 3 for the intercepted
// one before the flags were read.
func TestConsumerFlagsCanMakeAFilterOpaque(t *testing.T) {
	for _, command := range []string{
		"grep -c util", "grep --count util", "grep -l util", "grep -L util",
		"grep -o util", "grep -q util", "grep -m 1 util", "grep -m1 util",
		"grep --max-count=1 util", "grep -Z util", "grep -vc util",
		"head -c 40", "head --bytes=40", "head -c40", "tail -f", "tail -F", "tail --follow",
	} {
		if ConsumerPreservesLines(strings.Fields(command)) {
			t.Errorf("%q changes the unit of its output and must be opaque", command)
		}
	}
	// And a flag that only narrows WHICH lines survive, or decorates them, still preserves them.
	for _, command := range []string{
		"grep -v _test", "grep -in pat", "grep -F pat", "grep -e pat", "grep -- -pat",
		"head -n 30", "head -30", "tail -n +900", "cat -n",
	} {
		if !ConsumerPreservesLines(strings.Fields(command)) {
			t.Errorf("%q keeps whole lines in order and must stay line-preserving", command)
		}
	}
}

// The counter is the one that looks harmless and is not: aracne's grep answer interleaves
// `# path:a-b` annotation lines with the matches, so a count of it is a count of a different
// thing -- returned as a bare number with nothing to reveal the substitution.
func TestCountersAreNotLinePreserving(t *testing.T) {
	if ConsumerPreservesLines([]string{"wc"}) {
		t.Error("wc must not be treated as line-preserving")
	}
	if !ConsumerPreservesLines([]string{"head"}) || !ConsumerPreservesLines([]string{"grep"}) {
		t.Error("cappers and filters must be line-preserving")
	}
}

// Paths and .exe suffixes must not smuggle an opaque consumer past the allow-list.
func TestConsumerClassificationUnwrapsPaths(t *testing.T) {
	for _, w := range []string{"/usr/bin/head", "/bin/grep", "HEAD"} {
		if !ConsumerPreservesLines([]string{w}) {
			t.Errorf("%q should classify by its base name", w)
		}
	}
	if ConsumerPreservesLines([]string{"/usr/bin/wc"}) {
		t.Error("/usr/bin/wc must still be opaque")
	}
}
