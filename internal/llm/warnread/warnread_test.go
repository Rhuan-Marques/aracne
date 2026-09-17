package warnread

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"

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

// A callee is read for its signature alone -- unless it is also a fix site on the same page, as
// in a chain: run() calls GetServer, whose signature moved, and GetServer calls newHandler, whose
// signature moved too. GetServer's body is where the second fix goes, so it is shown in full.
func TestSignatureOnlySparesCalleesThatAreAlsoFixSites(t *testing.T) {
	page := []domain.TopologyWarning{
		{ID: "1", Kind: domain.WarnSignatureChanged, SourceID: "api.GetServer", TargetID: "cmd.run"},
		{ID: "2", Kind: domain.WarnSignatureChanged, SourceID: "api.newHandler", TargetID: "api.GetServer"},
		{ID: "3", Kind: domain.WarnSignatureChanged, SourceID: "v1.handleGetStatus", TargetID: "v1.NewHandler"},
		{ID: "4", Kind: domain.WarnSignatureChanged, SourceID: "v1.annotated", TargetID: "v1.Other"},
		{ID: "5", Kind: domain.WarnNodeRemoved, SourceID: "pkg.Referrer", TargetID: "pkg.Gone"},
	}
	anns := map[string][]readunit.Annotation{"v1.annotated": {{Note: "x"}}}
	got := signatureOnly(page, anns)
	want := map[string]bool{"api.newHandler": true, "v1.handleGetStatus": true}
	if len(got) != len(want) {
		t.Fatalf("signature-only = %v, want %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("%s should be signature-only; got %v", id, got)
		}
	}
}
