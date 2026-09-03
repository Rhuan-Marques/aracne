package helper

import (
	"encoding/json"
	"testing"

	"aracne/internal/topology/domain"
)

// A resource reaches SignatureBaseline in two different Go shapes: freshly
// parsed, carrying the scanner's typed structs, and read back from SQLite,
// where Properties has been through JSON and is []any of map[string]any.
// roundTrip produces the second from the first.
func roundTrip(t *testing.T, res domain.Resource) domain.Resource {
	t.Helper()
	raw, err := json.Marshal(res.Properties)
	if err != nil {
		t.Fatal(err)
	}
	var props map[string]any
	if err := json.Unmarshal(raw, &props); err != nil {
		t.Fatal(err)
	}
	res.Properties = props
	return res
}

type param struct {
	Name   string
	Typing string
}

func fn(name string, in, out []param) domain.Resource {
	return domain.Resource{
		ID:         "pkg." + name,
		Kind:       domain.ResourceFunction,
		Name:       name,
		Properties: map[string]any{"input": in, "output": out},
	}
}

// TestSignatureBaselineIsIndependentOfHowTheResourceWasBuilt is the trap the
// whole mechanism turns on. The baseline is stored when a warning is raised and
// compared on a later scan, and those two moments read the resource from
// different places. json.Marshal writes a struct in field order and a map in
// sorted-key order, so a fingerprint taken straight off the raw value would
// give one function two different answers and the discharge would silently
// never fire.
func TestSignatureBaselineIsIndependentOfHowTheResourceWasBuilt(t *testing.T) {
	typed := fn("Throw", []param{{"rt", "*sobek.Runtime"}, {"err", "error"}}, nil)
	if a, b := SignatureBaseline(typed), SignatureBaseline(roundTrip(t, typed)); a != b {
		t.Errorf("parsed and persisted forms fingerprint differently:\n parsed:    %s\n persisted: %s", a, b)
	}
}

// TestSignatureBaselineTreatsEveryEmptySignatureAlike covers the second half of
// the same problem. "No parameters" arrives as an absent key, a Go nil, a nil
// slice (`null`) or an empty slice (`[]`) depending only on provenance.
func TestSignatureBaselineTreatsEveryEmptySignatureAlike(t *testing.T) {
	want := SignatureBaseline(domain.Resource{Name: "F", Properties: map[string]any{}})
	cases := map[string]domain.Resource{
		"nil properties":    {Name: "F"},
		"nil value":         {Name: "F", Properties: map[string]any{"input": nil, "output": nil}},
		"nil slice":         fn("F", nil, nil),
		"empty slice":       fn("F", []param{}, []param{}),
		"persisted empties": roundTrip(t, fn("F", []param{}, []param{})),
	}
	for name, res := range cases {
		res.Name = "F"
		if got := SignatureBaseline(res); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

func TestSignatureBaselineDistinguishesRealChanges(t *testing.T) {
	base := fn("F", []param{{"x", "int"}}, []param{{"", "error"}})
	for _, c := range []struct {
		name string
		res  domain.Resource
	}{
		{"an added parameter", fn("F", []param{{"x", "int"}, {"y", "int"}}, []param{{"", "error"}})},
		{"a retyped parameter", fn("F", []param{{"x", "string"}}, []param{{"", "error"}})},
		{"a changed result", fn("F", []param{{"x", "int"}}, []param{{"", "int"}})},
		{"a rename", fn("G", []param{{"x", "int"}}, []param{{"", "error"}})},
	} {
		if SignatureBaseline(base) == SignatureBaseline(c.res) {
			t.Errorf("%s must change the fingerprint", c.name)
		}
	}
}

// --- the lifecycle ----------------------------------------------------------

func sigWarning(id, source, target, baseline string) domain.TopologyWarning {
	return domain.TopologyWarning{
		ID: id, SourceID: source, TargetID: target,
		Kind: domain.WarnSignatureChanged, Baseline: baseline,
	}
}

// sigTopo builds on referrers_test.go's topoWith, adding the warnings.
func sigTopo(res []domain.Resource, ws ...domain.TopologyWarning) *domain.Topology {
	topo := topoWith(res...)
	for _, w := range ws {
		topo.Warnings[w.ID] = w
	}
	return topo
}

func TestDischargeSignatureWarningsDropsOnlyWhatCameBack(t *testing.T) {
	oneArg := fn("Throw", []param{{"rt", "*sobek.Runtime"}}, nil)
	twoArgs := fn("Throw", []param{{"rt", "*sobek.Runtime"}, {"ctx", "context.Context"}}, nil)
	original := SignatureBaseline(oneArg)

	t.Run("back to the callers' signature", func(t *testing.T) {
		topo := sigTopo([]domain.Resource{oneArg},
			sigWarning("c1@sig@pkg.Throw", "pkg.Throw", "pkg.CallerOne", original),
			sigWarning("c2@sig@pkg.Throw", "pkg.Throw", "pkg.CallerTwo", original))
		if n := DischargeSignatureWarnings(topo); n != 2 {
			t.Errorf("discharged %d, want both", n)
		}
		if len(topo.Warnings) != 0 {
			t.Errorf("warnings left: %+v", topo.Warnings)
		}
	})

	t.Run("still changed", func(t *testing.T) {
		topo := sigTopo([]domain.Resource{twoArgs},
			sigWarning("c1@sig@pkg.Throw", "pkg.Throw", "pkg.CallerOne", original))
		if n := DischargeSignatureWarnings(topo); n != 0 {
			t.Errorf("discharged %d, want 0: the caller is still broken", n)
		}
	})

	t.Run("no baseline recorded", func(t *testing.T) {
		// A row written before the baseline column existed. It must behave as it
		// always did -- never discharging -- rather than discharging wrongly.
		topo := sigTopo([]domain.Resource{oneArg},
			sigWarning("c1@sig@pkg.Throw", "pkg.Throw", "pkg.CallerOne", ""))
		if n := DischargeSignatureWarnings(topo); n != 0 {
			t.Errorf("discharged %d, want 0", n)
		}
	})

	t.Run("subject is gone", func(t *testing.T) {
		// CleanupOrphanedWarnings owns that case; this must not double-handle it.
		topo := sigTopo(nil,
			sigWarning("c1@sig@pkg.Throw", "pkg.Throw", "pkg.CallerOne", original))
		if n := DischargeSignatureWarnings(topo); n != 0 {
			t.Errorf("discharged %d, want 0", n)
		}
	})

	t.Run("other kinds are untouched", func(t *testing.T) {
		topo := sigTopo([]domain.Resource{oneArg}, domain.TopologyWarning{
			ID: "w", SourceID: "pkg.Throw", Kind: domain.WarnUseMissingNode, Baseline: original,
		})
		if n := DischargeSignatureWarnings(topo); n != 0 || len(topo.Warnings) != 1 {
			t.Errorf("a use_missing_node warning must survive; discharged %d", n)
		}
	})
}

func TestStampSignatureBaselinesRecordsThePreUpdateSignature(t *testing.T) {
	before := fn("Throw", []param{{"rt", "*sobek.Runtime"}}, nil)
	after := fn("Throw", []param{{"rt", "*sobek.Runtime"}, {"ctx", "context.Context"}}, nil)

	topo := sigTopo([]domain.Resource{after},
		sigWarning("c1@sig@pkg.Throw", "pkg.Throw", "pkg.CallerOne", ""),
		sigWarning("c2@sig@pkg.Throw", "pkg.Throw", "pkg.CallerTwo", "already-set"),
		sigWarning("c3@sig@pkg.Unknown", "pkg.Unknown", "pkg.CallerThree", ""))

	StampSignatureBaselines(topo, map[string]domain.Resource{before.ID: before})

	if got, want := topo.Warnings["c1@sig@pkg.Throw"].Baseline, SignatureBaseline(before); got != want {
		t.Errorf("baseline = %q, want the signature from before the update %q", got, want)
	}
	if got := topo.Warnings["c2@sig@pkg.Throw"].Baseline; got != "already-set" {
		t.Errorf("an existing baseline must not be overwritten, got %q", got)
	}
	// Stamping this one with the current signature would discharge a warning
	// that is still true; leaving it empty only preserves the old behaviour.
	if got := topo.Warnings["c3@sig@pkg.Unknown"].Baseline; got != "" {
		t.Errorf("a subject absent from the pre-update set must stay unstamped, got %q", got)
	}
}

// TestRestoreSignatureBaselinesKeepsTheOriginalAcrossRepeatedEdits is why the
// baseline is not simply recomputed each time: A->B->C must still point at A.
func TestRestoreSignatureBaselinesKeepsTheOriginalAcrossRepeatedEdits(t *testing.T) {
	a := SignatureBaseline(fn("F", []param{{"x", "int"}}, nil))
	b := SignatureBaseline(fn("F", []param{{"x", "int"}, {"y", "int"}}, nil))

	previous := map[string]domain.TopologyWarning{
		"w": sigWarning("w", "pkg.F", "pkg.Caller", a),
	}
	// The scanner re-raises the same id knowing only the shape B left behind.
	topo := sigTopo(nil, sigWarning("w", "pkg.F", "pkg.Caller", ""))

	RestoreSignatureBaselines(topo, previous)
	if got := topo.Warnings["w"].Baseline; got != a {
		t.Errorf("baseline = %q, want the original %q (not %q)", got, a, b)
	}

	// A warning that arrives WITH a baseline is the authority on itself.
	topo = sigTopo(nil, sigWarning("w", "pkg.F", "pkg.Caller", b))
	RestoreSignatureBaselines(topo, previous)
	if got := topo.Warnings["w"].Baseline; got != b {
		t.Errorf("an explicit baseline must win, got %q", got)
	}
}
