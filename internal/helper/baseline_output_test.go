package helper

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The output segment of a baseline is what lets a return-type-only change outlive the
// call-site rule, so reading it back has to survive a '|' inside a parameter type.
func TestBaselineOutputReadsTheOutputSegment(t *testing.T) {
	for _, c := range []struct {
		name  string
		props map[string]any
	}{
		{"typed params", map[string]any{
			"input":  []any{map[string]any{"Name": "a", "Typing": "int"}},
			"output": []any{map[string]any{"Typing": "string"}},
		}},
		{"a union parameter spells a pipe", map[string]any{
			"input":  []any{map[string]any{"Name": "a", "Typing": "string | number"}},
			"output": []any{map[string]any{"Typing": "boolean | null"}},
		}},
		{"no parameters", map[string]any{
			"output": []any{map[string]any{"Typing": "int"}},
		}},
		{"no output", map[string]any{
			"input": []any{map[string]any{"Name": "a", "Typing": "int"}},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := domain.Resource{Name: "f", Properties: c.props}
			got, ok := baselineOutput(SignatureBaseline(res))
			if !ok {
				t.Fatalf("could not read the output of %q", SignatureBaseline(res))
			}
			if want := canonicalJSON(c.props["output"]); got != want {
				t.Errorf("output = %q, want %q", got, want)
			}
		})
	}
}

func TestOutputChangedSinceBaseline(t *testing.T) {
	before := domain.Resource{Name: "get", Properties: map[string]any{
		"input":  []any{map[string]any{"Name": "a", "Typing": "int"}},
		"output": []any{map[string]any{"Typing": "int"}},
	}}
	w := domain.TopologyWarning{Kind: domain.WarnSignatureChanged, Baseline: SignatureBaseline(before)}

	sameOutput := domain.Resource{Name: "get", Properties: map[string]any{
		"input":  []any{map[string]any{"Name": "a", "Typing": "int"}, map[string]any{"Name": "b", "Typing": "int"}},
		"output": []any{map[string]any{"Typing": "int"}},
	}}
	if outputChangedSinceBaseline(w, sameOutput) {
		t.Error("only the parameters changed; the output must read as unchanged")
	}
	newOutput := domain.Resource{Name: "get", Properties: map[string]any{
		"input":  []any{map[string]any{"Name": "a", "Typing": "int"}},
		"output": []any{map[string]any{"Typing": "string"}},
	}}
	if !outputChangedSinceBaseline(w, newOutput) {
		t.Error("int -> string must read as a changed output")
	}
	if outputChangedSinceBaseline(domain.TopologyWarning{Kind: domain.WarnSignatureChanged}, newOutput) {
		t.Error("with no baseline there is nothing to compare, and the answer must be no")
	}
}

// TestSignatureBaselineCarriesOverloads.
//
// A TypeScript overload set's Input/Output are the implementation's, which is deliberately
// the widest signature of the set and does not move when an overload is deleted. Without the
// overloads in the baseline, DischargeSignatureWarnings saw the subject as "back to the
// signature the warning was raised against" and retired the warning immediately.
func TestSignatureBaselineCarriesOverloads(t *testing.T) {
	impl := domain.Resource{Name: "parse", Properties: map[string]any{
		"input":  []map[string]any{{"Name": "s", "Typing": "string"}},
		"output": []map[string]any{{"Typing": "number"}},
	}}
	both := impl
	both.Properties = map[string]any{
		"input": impl.Properties["input"], "output": impl.Properties["output"],
		"overloads": []map[string]any{
			{"Input": []map[string]any{{"Name": "s", "Typing": "string"}}},
			{"Input": []map[string]any{{"Name": "s", "Typing": "string"}, {"Name": "radix", "Typing": "number"}}},
		},
	}
	one := impl
	one.Properties = map[string]any{
		"input": impl.Properties["input"], "output": impl.Properties["output"],
		"overloads": []map[string]any{
			{"Input": []map[string]any{{"Name": "s", "Typing": "string"}, {"Name": "radix", "Typing": "number"}}},
		},
	}

	if SignatureBaseline(both) == SignatureBaseline(one) {
		t.Error("deleting an overload must change the baseline")
	}
	// A resource with no overloads keeps the baseline string it already has stored, so an
	// upgrade does not invalidate every warning in the database.
	if SignatureBaseline(impl) != `parse|[{"Name":"s","Typing":"string"}]|[{"Typing":"number"}]` {
		t.Errorf("baseline shape changed for an ordinary function: %q", SignatureBaseline(impl))
	}
	// And the output segment is still readable with a further segment behind it.
	w := domain.TopologyWarning{Kind: domain.WarnSignatureChanged, Baseline: SignatureBaseline(both)}
	if outputChangedSinceBaseline(w, both) {
		t.Error("an unchanged return type must not read as changed")
	}
	moved := both
	moved.Properties = map[string]any{
		"input": both.Properties["input"], "overloads": both.Properties["overloads"],
		"output": []map[string]any{{"Typing": "string"}},
	}
	if !outputChangedSinceBaseline(w, moved) {
		t.Error("outputChangedSinceBaseline stopped seeing a real difference")
	}
}
