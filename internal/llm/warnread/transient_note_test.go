package warnread

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// noteFor writes the text that lands ON the offending line in an expanded warning read, which
// is the surface an agent actually reads after an edit. A transient has to be legible as a
// maybe there too -- the inline note is often the only thing seen, since the expansion
// REPLACES the summary rather than sitting under it.
func TestNoteForMarksATransientAsUnverified(t *testing.T) {
	topo := &domain.Topology{Resources: map[string]domain.Resource{}}
	base := domain.TopologyWarning{
		Kind: domain.WarnSignatureChanged, SourceID: "pkg.Target", TargetID: "pkg.Caller",
	}

	stored := noteFor(base, topo)
	if strings.Contains(strings.ToUpper(stored), "UNVERIFIED") {
		t.Errorf("a stored warning must not read as unverified: %q", stored)
	}
	if !strings.Contains(stored, "pkg.Target") {
		t.Errorf("note must name the callee whose signature moved: %q", stored)
	}

	transient := base
	transient.Transient = true
	got := noteFor(transient, topo)
	if !strings.Contains(got, "UNVERIFIED") {
		t.Errorf("a transient must read as unverified: %q", got)
	}
	if !strings.Contains(got, "ignore if still fits") {
		t.Errorf("a transient must tell the reader what to do with it: %q", got)
	}
	// Both still identify the callee; the tag is an addition, not a replacement.
	if !strings.Contains(got, "pkg.Target") {
		t.Errorf("transient note dropped the callee: %q", got)
	}
}
