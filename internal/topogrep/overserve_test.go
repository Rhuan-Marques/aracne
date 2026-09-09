package topogrep

import (
	"strings"
	"testing"
)

// annotated is a result shaped like the one that motivated the ceiling: a single line the
// caller actually grepped for, and a crowd of nodes that surfaced because their stored
// descriptions said the word the file's text never did.
func annotated() (*Result, Options) {
	res := &Result{
		Matches: []Match{
			{Path: "pkg/a.go", Line: 12, Text: "\tvalue := compute()", ResourceID: "pkg.Compute",
				Description: "Computes the value.", MatchedOn: MatchContent},
		},
		DistinctResources: 1,
	}
	for i := 0; i < 12; i++ {
		res.Matches = append(res.Matches, Match{
			Path:        "pkg/b.go",
			Line:        10 + i,
			Text:        "func Node" + string(rune('A'+i)) + "() {",
			ResourceID:  "github.com/demo/pkg.Node" + string(rune('A'+i)),
			Description: strings.Repeat("prose about the value this node computes, ", 4),
			MatchedOn:   MatchDescription,
			NodeHit:     true,
		})
	}
	// LineNumbers, because the assertions below spell out the `12:` a `-n` search prints.
	return res, Options{Pattern: "value", Terse: true, LineNumbers: true}
}

// RawBytes is the denominator, so it has to be what a plain grep would have printed and
// nothing else. Counting the node rows would make the answer look proportionate to itself.
func TestRawBytesCountsOnlyWhatAPlainGrepWouldPrint(t *testing.T) {
	res, opt := annotated()

	want := len("pkg/a.go") + 1 + len("12") + 2 + len("\tvalue := compute()")
	if got := RawBytes(res, opt); got != want {
		t.Errorf("RawBytes = %d, want %d -- the one textual row, not the node rows", got, want)
	}
	// And the row shape follows the same single-file rule the renderer follows.
	res.RootIsFile = true
	if got, bare := RawBytes(res, opt), want-len("pkg/a.go")-1; got != bare {
		t.Errorf("RawBytes for one named file = %d, want %d with no path field", got, bare)
	}
}

// The fallback for a surface with no real command to run is this same result rendered the way
// grep would have rendered it: the lines the file actually holds, and nothing around them.
func TestWithoutTopologyLeavesAPlainGrepAnswer(t *testing.T) {
	res, opt := annotated()

	full := FormatResult(res, opt)
	if !strings.Contains(full, "# ") {
		t.Fatalf("the annotated result carries no headers to strip:\n%s", full)
	}

	plain := FormatResult(WithoutTopology(res), opt)
	if strings.Contains(plain, "# ") {
		t.Errorf("a stripped result still carries resource headers:\n%s", plain)
	}
	if strings.Contains(plain, "func NodeA") {
		t.Errorf("a stripped result still carries node rows:\n%s", plain)
	}
	if want := "pkg/a.go:12:\tvalue := compute()"; !strings.Contains(plain, want) {
		t.Errorf("stripping lost the real match %q:\n%s", want, plain)
	}
	if len(plain) > len(full) {
		t.Errorf("the plain rendering is longer than the annotated one: %d > %d",
			len(plain), len(full))
	}
}

// The exemption. A search that answered entirely from names and descriptions has no plain-grep
// answer to be disproportionate to, and measuring it against zero would delete the one thing a
// topology search can do that grep cannot.
func TestASearchWithNoTextualMatchIsExempt(t *testing.T) {
	res, _ := annotated()
	if !HasTextualMatch(res) {
		t.Error("a result holding a line match reported none")
	}

	res.Matches = res.Matches[1:]
	if HasTextualMatch(res) {
		t.Error("a result of node rows only reported a textual match")
	}

	// A binary file matched on its bytes; grep would have said so, so it counts.
	res.BinaryFiles = []string{"blob.bin"}
	if !HasTextualMatch(res) {
		t.Error("a binary-file match reported no textual match")
	}
}
