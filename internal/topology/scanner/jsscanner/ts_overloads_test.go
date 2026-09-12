package jsscanner

import (
	"encoding/json"
	"path/filepath"
	"testing"

	js "github.com/Rhuan-Marques/aracne/internal/topology/javascript"
)

// TypeScript overload sets.
//
// An overload set is N bodiless signatures followed by one implementation, and every one of
// them mints the same resource id, so the last declaration parsed simply overwrote the rest.
// What survived was the implementation -- the one signature TypeScript does NOT let anyone
// call. The overloads ARE the API: they are what a caller is checked against, what a read
// should show, and what has to be compared to notice one being deleted.

const overloadSrc = `export function overloaded(s: string): number;
export function overloaded(s: string, n: number): number;
export function overloaded(s: string, n?: number): number { return n ?? 0; }
`

// tsOverloadsOf reads the stored overload signatures of one function.
func tsOverloadsOf(t *testing.T, res map[string]any) []js.FunctionDefinition {
	t.Helper()
	raw, err := json.Marshal(res["overloads"])
	if err != nil {
		t.Fatalf("marshal overloads: %v", err)
	}
	var defs []js.FunctionDefinition
	if err := json.Unmarshal(raw, &defs); err != nil {
		t.Fatalf("unmarshal overloads: %v", err)
	}
	return defs
}

func TestTSOverloadSetKeepsEverySignatureAndTheWholeSpan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ns.ts"), overloadSrc)
	topo := scanTS(t, dir)

	var found bool
	for _, res := range topo.Resources {
		if res.Name != "overloaded" {
			continue
		}
		found = true
		// The span has to cover the overloads, or a read shows the one signature
		// nobody may call and none of the ones they may.
		if res.Location.StartsAt != 1 {
			t.Errorf("span starts at line %d, want the first overload (line 1)", res.Location.StartsAt)
		}
		if res.Location.EndsAt != 3 {
			t.Errorf("span ends at line %d, want the implementation (line 3)", res.Location.EndsAt)
		}
		defs := tsOverloadsOf(t, res.Properties)
		if len(defs) != 2 {
			t.Fatalf("stored %d overload signatures, want 2: %+v", len(defs), defs)
		}
		if len(defs[0].Input) != 1 || len(defs[1].Input) != 2 {
			t.Errorf("overload arities = %d, %d; want 1, 2", len(defs[0].Input), len(defs[1].Input))
		}
	}
	if !found {
		t.Fatal("function overloaded not found")
	}
}

// TestTSBodilessOverloadSetKeepsEverySignature is the .d.ts / `declare function` shape: no
// implementation at all, so the last overload used to stand in for the whole set.
func TestTSBodilessOverloadSetKeepsEverySignature(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "amb.d.ts"), `export declare function parse(s: string): number;
export declare function parse(s: string, radix: number): number;
`)
	topo := scanTS(t, dir)
	for _, res := range topo.Resources {
		if res.Name != "parse" {
			continue
		}
		if res.Location.StartsAt != 1 || res.Location.EndsAt != 2 {
			t.Errorf("span = %d..%d, want 1..2", res.Location.StartsAt, res.Location.EndsAt)
		}
		if defs := tsOverloadsOf(t, res.Properties); len(defs) != 2 {
			t.Fatalf("stored %d signatures, want 2: %+v", len(defs), defs)
		}
		return
	}
	t.Fatal("function parse not found")
}

// TestSignaturesEqualJSSeesADeletedOverload: deleting an overload is a breaking change that
// the implementation's own signature -- deliberately the widest of the set -- cannot show.
func TestSignaturesEqualJSSeesADeletedOverload(t *testing.T) {
	sig := func(n int) js.FunctionDefinition {
		in := make([]js.VariableDefinition, n)
		for i := range in {
			in[i] = js.VariableDefinition{Name: "p", Typing: "number", Annotation: "number"}
		}
		return js.FunctionDefinition{Name: "f", Input: in}
	}
	impl := js.JavaScriptFunction{Name: "f", Input: sig(2).Input}
	both := impl
	both.Overloads = []js.FunctionDefinition{sig(1), sig(2)}
	one := impl
	one.Overloads = []js.FunctionDefinition{sig(2)}

	if signaturesEqualJS(both, one, true) {
		t.Error("deleting an overload must read as a signature change")
	}
	if !signaturesEqualJS(both, both, true) {
		t.Error("an unchanged overload set must read as unchanged")
	}
}
