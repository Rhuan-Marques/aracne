package jsscanner

import (
	"path/filepath"
	"testing"

	"aracne/internal/topology/domain"
)

// TestTSReturnTypeFollowingSameFile checks that `const x = f(); x.method()` resolves the method
// call when f, x's class, and the caller all live in the same file (the local case).
func TestTSReturnTypeFollowingSameFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "local.ts"), `
export class Stu {
  method(): void {}
}

export function make(): Stu {
  return new Stu();
}

export function caller(): void {
  const x = make();
  x.method();
}
`)
	topo := scanTS(t, dir)

	caller := findByName(topo, domain.ResourceFunction, "caller")
	if caller == nil {
		t.Fatal("expected function caller")
	}
	if !connHasSuffix(caller, "uses_class", "local.Stu") {
		t.Errorf("expected caller uses_class Stu, uses_class=%v", caller.Connections["uses_class"])
	}
	if !connHasSuffix(caller, "calls", "local.Stu.method") {
		t.Errorf("expected caller to call Stu.method via return-type following, calls=%v", caller.Connections["calls"])
	}
}

// TestTSReturnTypeFollowingTransitiveCrossFile is the headline parity test: it threads a class,
// a function returning it, and a caller across THREE files (the function is imported from a
// second file, and its return type lives in a third). Following the return type requires
// storing the return type's canonical id in the defining file's context (Output[i].TypingID)
// so the caller can resolve x.method() without the callee's import context.
//
// Before the TypingID change this FAILS (JS did not follow function return types at all); after
// it, caller f gets a `calls` edge to file1's Stu.method.
func TestTSReturnTypeFollowingTransitiveCrossFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "file1.ts"), `
export class Stu {
  method(): void {}
}
`)
	writeFile(t, filepath.Join(dir, "file2.ts"), `
import { Stu } from './file1';

export function extFunc(): Stu {
  return new Stu();
}
`)
	writeFile(t, filepath.Join(dir, "file3.ts"), `
import { extFunc } from './file2';

export function f(): void {
  const x = extFunc();
  x.method();
}
`)
	topo := scanTS(t, dir)

	f := findByName(topo, domain.ResourceFunction, "f")
	if f == nil {
		t.Fatal("expected function f")
	}
	if !connHasSuffix(f, "uses_class", "file1.Stu") {
		t.Errorf("expected f uses_class Stu (defined in file1, returned by file2.extFunc), uses_class=%v", f.Connections["uses_class"])
	}
	if !connHasSuffix(f, "calls", "file1.Stu.method") {
		t.Errorf("expected f to call file1.Stu.method transitively via extFunc's return type, calls=%v", f.Connections["calls"])
	}
}

// TestJSUntypedNoReturnTypeFollowing guards the untyped-JS behavior: with no return-type
// annotation there is no TypingID, so `const x = f(); x.bar()` produces no method-call edge.
func TestJSUntypedNoReturnTypeFollowing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plain.js"), `
export class Stu {
  method() {}
}

export function make() {
  return new Stu();
}

export function caller() {
  const x = make();
  x.method();
}
`)
	topo := scanProject(t, dir)

	caller := findByName(topo, domain.ResourceFunction, "caller")
	if caller == nil {
		t.Fatal("expected function caller")
	}
	if connHasSuffix(caller, "calls", "plain.Stu.method") {
		t.Errorf("expected NO call edge for untyped JS return value, calls=%v", caller.Connections["calls"])
	}
}
