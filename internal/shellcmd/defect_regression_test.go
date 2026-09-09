package shellcmd

import (
	"reflect"
	"testing"
)

// TestParseDoesNotMutateArgv pins AR-23.
//
// Cluster expansion spliced with `append(args[:i], ...)` where args is argv[1:] -- the
// CALLER's backing array -- so a slice with spare capacity was rewritten in place. Both
// shipped callers happen to pass len==cap today and the append reallocated, but RunCmd hands
// the same slice on to passthrough(), so a caller that built argv with append would have run a
// command the model never typed. Measured on a slice with capacity: `grep -rn foo .` came back
// as `grep -r -n foo`, with the path operand gone.
func TestParseDoesNotMutateArgv(t *testing.T) {
	cases := [][]string{
		{"grep", "-rn", "foo", "."},
		{"grep", "-rm1", "foo", "src"},
		{"grep", "-in", "x", "a.go", "b.go"},
	}
	for _, argv := range cases {
		spare := make([]string, 0, len(argv)+8) // capacity beyond length is the trigger
		spare = append(spare, argv...)
		before := append([]string(nil), spare...)

		Parse(spare)

		if !reflect.DeepEqual(spare, before) {
			t.Fatalf("Parse mutated its caller's argv: %v -> %v", before, spare)
		}
	}
}

// TestParseStillExpandsClusters: copying instead of splicing must not change what the parser
// understands.
func TestParseStillExpandsClusters(t *testing.T) {
	spare := make([]string, 0, 16)
	spare = append(spare, "grep", "-rn", "foo", ".")
	req := Parse(spare)
	if req.Kind != KindGrep {
		t.Fatalf("expected a grep, got kind %v (%s)", req.Kind, req.Why)
	}
	if req.Grep.Pattern != "foo" {
		t.Fatalf("pattern = %q, want foo", req.Grep.Pattern)
	}
	if len(req.Operands) != 1 || req.Operands[0] != "." {
		t.Fatalf("operands = %v, want [.]", req.Operands)
	}

	withCount := make([]string, 0, 16)
	withCount = append(withCount, "grep", "-rm1", "foo", "src")
	req = Parse(withCount)
	if req.Kind != KindGrep || req.Grep.MaxCount != 1 {
		t.Fatalf("expected -m 1 from the cluster, got kind=%v max=%d (%s)", req.Kind, req.Grep.MaxCount, req.Why)
	}
}
