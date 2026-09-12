package jsscanner

import (
	"encoding/json"
	"path/filepath"
	"testing"

	js "github.com/Rhuan-Marques/aracne/internal/topology/javascript"
)

// TypeScript signature fidelity.
//
// Typing keeps only the BASE name a type resolves to -- `Item[]`, `Map<string, Item[]>` and
// `Item | null` all reduce to something that resolves to a resource -- which is exactly
// right for following a value's type and wrong for a signature. Comparing signatures on
// that text made `items: Item` -> `items: Item[]` a no-op, so a change that breaks every
// caller produced no warning at all. Annotation carries the whole declaration alongside it.

// tsParamAnnotations reads the stored per-parameter annotations of one function.
func tsParamAnnotations(t *testing.T, dir, fnName string) []js.VariableDefinition {
	t.Helper()
	topo := scanTS(t, dir)
	for _, res := range topo.Resources {
		if res.Name != fnName {
			continue
		}
		raw, err := json.Marshal(res.Properties["input"])
		if err != nil {
			t.Fatalf("marshal input: %v", err)
		}
		var defs []js.VariableDefinition
		if err := json.Unmarshal(raw, &defs); err != nil {
			t.Fatalf("unmarshal input: %v", err)
		}
		return defs
	}
	t.Fatalf("function %q not found", fnName)
	return nil
}

func TestTSRecordsTheWholeAnnotationNotJustItsBaseName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "m.ts"), `
export class Item {}
export function arr(items: Item[], m: Map<string, Item[]>, maybe: Item | null): Item[] {
  return items;
}
`)
	defs := tsParamAnnotations(t, dir, "arr")
	if len(defs) != 3 {
		t.Fatalf("expected 3 parameters, got %d", len(defs))
	}
	want := []string{"Item[]", "Map<string,Item[]>", "Item|null"}
	for i, w := range want {
		if defs[i].Annotation != w {
			t.Errorf("parameter %d: Annotation = %q, want %q", i, defs[i].Annotation, w)
		}
	}
	// Typing keeps the resolvable base name, which is what type resolution reads.
	if defs[0].Typing != "Item" {
		t.Errorf("Typing must still be the resolvable base name, got %q", defs[0].Typing)
	}
}

func TestTSRecordsTheWholeReturnAnnotation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "m.ts"), `
export class Item {}
export function ret(): Item[] { return []; }
`)
	topo := scanTS(t, dir)
	for _, res := range topo.Resources {
		if res.Name != "ret" {
			continue
		}
		raw, _ := json.Marshal(res.Properties["output"])
		var defs []js.VariableDefinition
		if err := json.Unmarshal(raw, &defs); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		if len(defs) != 1 || defs[0].Annotation != "Item[]" {
			t.Fatalf("return annotation = %+v, want one entry annotated Item[]", defs)
		}
		return
	}
	t.Fatal("function ret not found")
}

// TestSignaturesEqualJSSeesTypeShapeChanges is the comparison the warning is raised from.
func TestSignaturesEqualJSSeesTypeShapeChanges(t *testing.T) {
	fn := func(in, out js.VariableDefinition) js.JavaScriptFunction {
		return js.JavaScriptFunction{
			Name:   "f",
			Input:  []js.VariableDefinition{in},
			Output: []js.VariableDefinition{out},
		}
	}
	item := js.VariableDefinition{Name: "items", Typing: "Item", Annotation: "Item"}
	items := js.VariableDefinition{Name: "items", Typing: "Item", Annotation: "Item[]"}
	out := js.VariableDefinition{Typing: "Item", Annotation: "Item"}
	outs := js.VariableDefinition{Typing: "Item", Annotation: "Item[]"}

	cases := []struct {
		name     string
		a, b     js.JavaScriptFunction
		wantSame bool
	}{
		{"Item -> Item[] is a change", fn(item, out), fn(items, out), false},
		{"a changed return shape is a change", fn(item, out), fn(item, outs), false},
		{"an unchanged signature is unchanged", fn(item, out), fn(item, out), true},
		{
			// A row written before annotations were recorded carries none. Treating
			// that as a difference would make the first rescan after an upgrade warn
			// about every TypeScript function in the repository.
			"a missing annotation falls back to Typing",
			fn(js.VariableDefinition{Name: "items", Typing: "Item"}, js.VariableDefinition{Typing: "Item"}),
			fn(item, out), true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := signaturesEqualJS(c.a, c.b, true); got != c.wantSame {
				t.Errorf("signaturesEqualJS = %v, want %v", got, c.wantSame)
			}
			// JavaScript stops at parameter names on purpose; annotations never
			// decide anything there.
			if !signaturesEqualJS(c.a, c.b, false) {
				t.Error("JavaScript must not judge annotations")
			}
		})
	}
}
