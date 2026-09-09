//go:build audit

// Package-local reproducers for the confirmed findings in aracne-fault-ledger.html.
// Build-tagged so `go test ./...` stays green; run them with:
//
//	go test -tags audit ./...
package shellcmd

import (
	"fmt"
	"testing"
)

// A-01: Parse is documented as "a pure function of argv" -- both callers depend on it, and
// RunCmd parses argv and then execs the same slice on the passthrough branch. The cluster
// expansion in parseGrep writes through the slice header into the caller's backing array.
func TestAudit_ParseDoesNotMutateCallerArgv(t *testing.T) {
	argv := make([]string, 0, 16) // spare capacity: any append-built argv has it
	argv = append(argv, "grep", "-rn", "foo", ".")
	before := fmt.Sprint(argv)

	Parse(argv)

	if got := fmt.Sprint(argv); got != before {
		t.Errorf("Parse mutated the caller's argv:\n  before %s\n  after  %s", before, got)
	}
}

// A-02: breToRE2's own comment says a caret is "an anchor only where it leads, ordinary
// anywhere else". Because the '^' case deliberately leaves `leading` set (so a following '*'
// stays literal), every later caret is emitted as an anchor too.
func TestAudit_BREInteriorCaretIsLiteral(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"^^foo", `^\^foo`}, // POSIX BRE: a line starting with the literal "^foo"
	} {
		got, ok := breToRE2(tc.in)
		if !ok {
			t.Errorf("breToRE2(%q) refused; want %q", tc.in, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("breToRE2(%q) = %q, want %q (the interior caret became a second anchor,\n"+
				"so the pattern matches lines the real grep does not)", tc.in, got, tc.want)
		}
	}
}
