package warnread

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Which half of a warning holds the code to change depends on the kind, and the batch renders
// whatever is named first in full. The fix site has to be first.
func TestWarningReadTargetsPutTheFixSiteFirst(t *testing.T) {
	for _, tc := range []struct {
		kind  domain.WarningKind
		first string
	}{
		{domain.WarnSignatureChanged, "caller"},  // the call site is the edit
		{domain.WarnInterfaceConflict, "caller"}, // TargetID is the implementer
		{domain.WarnNodeRemoved, "callee"},       // SourceID still points at something gone
		{domain.WarnUseMissingNode, "callee"},    //  "
	} {
		got := readTargets(domain.TopologyWarning{
			Kind: tc.kind, SourceID: "callee", TargetID: "caller",
		})
		if len(got) != 2 || got[0] != tc.first {
			t.Errorf("%s: got %v, want %q first", tc.kind, got, tc.first)
		}
	}
}
