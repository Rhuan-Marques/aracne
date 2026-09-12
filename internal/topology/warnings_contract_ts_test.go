package topology_test

// TypeScript call-site regressions, end to end.
//
// The tables in internal/topology/contract pin the rules; these pin that a real scan
// reaches them, which for TypeScript is where the interesting failures were: a warning
// raised against a call that already fits can never be retired, because no edit to the
// caller can make a fitting call fit harder. The agent is then told to go re-verify
// correct code, every edit, until the callee is reverted.
//
// Shared harness (contractLang, newContractProj, edit, callerWarnings) lives in
// warnings_contract_langs_test.go.

import "testing"

func tsProject(t *testing.T, files map[string]string) *contractProj {
	t.Helper()
	files["package.json"] = `{ "name": "tsreg", "version": "1.0.0" }` + "\n"
	files["tsconfig.json"] = `{ "compilerOptions": { "target": "ES2020" } }` + "\n"
	return newContractProj(t, contractLang{name: "typescript", files: files})
}

// TestTypeScriptRetiresWarningsForCallsThatStillFit covers the three legal argument forms
// the matcher used to judge as mismatches: a primitive passed to its wrapper type, a
// number passed to a numeric enum, and `undefined` passed to an optional parameter. Adding
// an optional parameter to each callee is a change no caller can notice, so the warning it
// raises must be gone by the time the agent sees it.
func TestTypeScriptRetiresWarningsForCallsThatStillFit(t *testing.T) {
	const orig = `export enum Kind { Red, Blue }
export function boxed(n: Number): number { return 1; }
export function numEnum(k: Kind): number { return 2; }
export function optStr(x?: string): number { return 3; }
export function byPrim(id: string): number { return 4; }
`
	// Every callee gains an optional parameter: still callable exactly as before.
	const compatible = `export enum Kind { Red, Blue }
export function boxed(n: Number, extra?: number): number { return 1; }
export function numEnum(k: Kind, extra?: number): number { return 2; }
export function optStr(x?: string, extra?: number): number { return 3; }
export function byPrim(id: string, extra?: number): number { return 4; }
`
	// The same edit with the parameter REQUIRED: every call is now short an argument.
	const breaking = `export enum Kind { Red, Blue }
export function boxed(n: Number, extra: number): number { return 1; }
export function numEnum(k: Kind, extra: number): number { return 2; }
export function optStr(x: string, extra: number): number { return 3; }
export function byPrim(id: string, extra: number): number { return 4; }
`
	caller := "import { boxed, numEnum, optStr, byPrim, Kind } from './a';\n" +
		"export function useAll(): number {\n" +
		"  return boxed(1) + numEnum(0) + optStr(undefined) + byPrim(\"a\");\n" +
		"}\n"

	p := tsProject(t, map[string]string{"a.ts": orig, "b.ts": caller})
	if n := p.callerWarnings(); n != 0 {
		t.Fatalf("a freshly scanned project must be clean, got %d warnings", n)
	}

	p.edit("a.ts", compatible)
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("every call still fits; %d warning(s) left: %+v", n, w)
	}

	// Counter-case: the same shape of edit, breaking, must still be reported.
	p.edit("a.ts", breaking)
	if n := p.callerWarnings(); n == 0 {
		t.Error("a required parameter no caller passes must warn")
	}

	// And fixing the caller retires it.
	p.edit("b.ts", "import { boxed, numEnum, optStr, byPrim, Kind } from './a';\n"+
		"export function useAll(): number {\n"+
		"  return boxed(1, 2) + numEnum(0, 2) + optStr(\"s\", 2) + byPrim(\"a\", 2);\n"+
		"}\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("fixing every call must clear the warnings, %d left: %+v", n, w)
	}
}

// TestTypeScriptWarnsWhenATypeShapeChanges covers the other direction: the signature record
// used to keep only the base name a type resolves to, so `Item` -> `Item[]` was not a change
// and the edit that breaks every caller produced no warning at all.
func TestTypeScriptWarnsWhenATypeShapeChanges(t *testing.T) {
	const orig = `export class Item {}
export function ret(m: Map<string, Item>): Item { return new Item(); }
`
	const arrayed = `export class Item {}
export function ret(m: Map<string, Item>): Item[] { return [new Item()]; }
`
	// The same declaration, respaced. A spelling the author is free to vary must not read
	// as a signature change.
	const respaced = `export class Item {}
export function ret(m: Map<string,Item>): Item { return new Item(); }
`
	caller := "import { ret, Item } from './a';\n" +
		"export function use(m: Map<string, Item>): Item { return ret(m); }\n"

	p := tsProject(t, map[string]string{"a.ts": orig, "b.ts": caller})
	if n := p.callerWarnings(); n != 0 {
		t.Fatalf("a freshly scanned project must be clean, got %d warnings", n)
	}

	p.edit("a.ts", arrayed)
	if n := p.callerWarnings(); n == 0 {
		t.Fatal("a return type going from Item to Item[] must warn its callers")
	}

	p.edit("a.ts", orig)
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("reverting must clear the warning, %d left: %+v", n, w)
	}

	p.edit("a.ts", respaced)
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("respacing an annotation is not a signature change, %d warning(s): %+v", n, w)
	}
}

// TestTypeScriptWarnsWhenAnOverloadIsDeleted.
//
// Deleting an overload breaks every call that fitted only it, and it is invisible in the
// implementation signature -- which is deliberately the widest of the set and does not move.
// Every declaration of the set mints the same id, so before this the graph held only the
// implementation and the deletion looked like no change at all.
func TestTypeScriptWarnsWhenAnOverloadIsDeleted(t *testing.T) {
	const orig = `export function parse(s: string): number;
export function parse(s: string, radix: number): number;
export function parse(s: string, radix?: number): number { return radix ?? 0; }
`
	// The one-argument overload is gone; parse("1") no longer compiles.
	const deleted = `export function parse(s: string, radix: number): number;
export function parse(s: string, radix?: number): number { return radix ?? 0; }
`
	// A third overload, added: nothing that already compiled stops compiling.
	const widened = orig + `export function parseAlso(s: string, radix: number, flag: boolean): number { return 0; }
`
	caller := "import { parse } from './a';\n" +
		"export function use(): number { return parse(\"1\"); }\n"

	p := tsProject(t, map[string]string{"a.ts": orig, "b.ts": caller})
	if n := p.callerWarnings(); n != 0 {
		t.Fatalf("a freshly scanned project must be clean, got %d warnings", n)
	}

	p.edit("a.ts", deleted)
	if n := p.callerWarnings(); n == 0 {
		t.Fatal("deleting the overload the caller was written against must warn")
	}

	p.edit("a.ts", orig)
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("restoring the overload must clear the warning, %d left: %+v", n, w)
	}

	p.edit("a.ts", widened)
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("adding a declaration breaks no caller, %d warning(s): %+v", n, w)
	}

	// Counter-case: the caller rewritten to fit the surviving overload is clean.
	p.edit("a.ts", deleted)
	if n := p.callerWarnings(); n == 0 {
		t.Fatal("deleting the overload again must warn")
	}
	p.edit("b.ts", "import { parse } from './a';\n"+
		"export function use(): number { return parse(\"1\", 10); }\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("fixing the call must clear the warning, %d left: %+v", n, w)
	}
}

// TestTypeScriptWarnsCallersReachingThroughAnImportedNamespace.
//
// `import {Geo} from './ns'; Geo.dist()` records no calls edge before this, so the callee had
// no incoming reference: the whole warning mechanism is keyed on referrers, and a caller the
// graph cannot see is a caller nobody warns.
func TestTypeScriptWarnsCallersReachingThroughAnImportedNamespace(t *testing.T) {
	const orig = `export namespace Geo {
  export function dist(a: number): number { return a; }
}
`
	const wide = `export namespace Geo {
  export function dist(a: number, b: number): number { return a + b; }
}
`
	const compatible = `export namespace Geo {
  export function dist(a: number, b?: number): number { return a + (b ?? 0); }
}
`
	caller := "import { Geo } from './ns';\n" +
		"export function useIt(): number { return Geo.dist(1); }\n"

	p := tsProject(t, map[string]string{"ns.ts": orig, "app.ts": caller})
	if n := p.callerWarnings(); n != 0 {
		t.Fatalf("a freshly scanned project must be clean, got %d warnings", n)
	}

	p.edit("ns.ts", wide)
	if n := p.callerWarnings(); n == 0 {
		t.Fatal("widening a namespace member must warn the caller that reaches it")
	}

	p.edit("app.ts", "import { Geo } from './ns';\n"+
		"export function useIt(): number { return Geo.dist(1, 2); }\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("fixing the call must clear the warning, %d left: %+v", n, w)
	}

	// A change no caller can notice stays silent.
	p.edit("ns.ts", compatible)
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("an optional parameter breaks no caller, %d warning(s): %+v", n, w)
	}
}
