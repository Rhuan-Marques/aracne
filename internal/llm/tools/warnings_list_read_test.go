package tools

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/toolapi"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The `read` option on warnings_list is gated on the project's own config, and the gate covers
// the SCHEMA and not only the effect. These pin the two halves that live in this package: what
// the tool advertises, and what render does with the flag.

// hasParam reports whether the tool's schema carries a property by that name.
func hasParam(params []toolapi.Parameter, name string) bool {
	for _, p := range params {
		if p.Name == name {
			return true
		}
	}
	return false
}

// readsOn is a config with features.warning_reads turned on.
func readsOn() *helper.Config {
	cfg := helper.DefaultConfig()
	cfg.Features.WarningReads = true
	return cfg
}

// A schema property is re-sent on every request, so a project that left features.warning_reads
// off must not be charged for an option the tool will not honour.
func TestWarningsListHidesReadWhenTheFeatureIsOff(t *testing.T) {
	tool := NewWarningsList(nil, helper.DefaultConfig(), nil)
	if hasParam(tool.Parameters(), "read") {
		t.Error("read is advertised with features.warning_reads off")
	}
	if strings.Contains(tool.Description(), "read=true") {
		t.Error("the description advertises an option the schema does not carry")
	}
}

func TestWarningsListAdvertisesReadWhenTheFeatureIsOn(t *testing.T) {
	tool := NewWarningsList(nil, readsOn(), nil)
	if !hasParam(tool.Parameters(), "read") {
		t.Fatal("read is not advertised even though the feature is on")
	}
	if !strings.Contains(tool.Description(), "read=true") {
		t.Error("the description does not mention the option the schema carries")
	}
}

// A nil config is the switched-off state, not a panic: a caller with no config to hand -- a
// test, a probe -- gets the plain listing.
func TestWarningsListWithNilConfigStaysOff(t *testing.T) {
	tool := NewWarningsList(nil, nil, nil)
	if hasParam(tool.Parameters(), "read") {
		t.Error("a nil config was read as an enabled feature")
	}
	if out := tool.render([]domain.TopologyWarning{
		{ID: "1", Kind: domain.WarnNodeRemoved, SourceID: "pkg.A", TargetID: "pkg.B", Message: "gone"},
	}, true); !strings.Contains(out, "node_removed") {
		t.Errorf("read=true with a nil config did not fall back to a plain listing:\n%s", out)
	}
}

// render sorts before it hands anything on, because the expansion takes the FIRST N and
// promises the next call continues from there.
func TestWarningsListRendersInTheSharedOrder(t *testing.T) {
	warnings := []domain.TopologyWarning{
		{ID: "3", Kind: domain.WarnSignatureChanged, SourceID: "pkg.Callee", TargetID: "pkg.Zulu", Message: "z"},
		{ID: "1", Kind: domain.WarnSignatureChanged, SourceID: "pkg.Callee", TargetID: "pkg.Alpha", Message: "a"},
		{ID: "2", Kind: domain.WarnNodeRemoved, SourceID: "pkg.Mike", TargetID: "pkg.Gone", Message: "m"},
	}
	out := NewWarningsList(nil, helper.DefaultConfig(), nil).render(warnings, false)

	// node_removed sorts before signature_changed, then by source, then by target.
	iMike := strings.Index(out, "pkg.Mike")
	iAlpha := strings.Index(out, "pkg.Alpha")
	iZulu := strings.Index(out, "pkg.Zulu")
	if iMike < 0 || iAlpha < 0 || iZulu < 0 || !(iMike < iAlpha && iAlpha < iZulu) {
		t.Errorf("listing is not in domain.SortWarnings order:\n%s", out)
	}
}
