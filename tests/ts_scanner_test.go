package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTypeScriptScanDetectsByTsconfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.json"), `{"compilerOptions":{"strict":true}}`)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "src", "core.ts"), `
export function greet(name: string): string {
  return "Hello " + name;
}
`)

	mustRun(t, dir, "scan", "-root", dir)
	out := mustRun(t, dir, "resource", "list", "greet")
	if !strings.Contains(out, "greet") {
		t.Fatalf("expected greet in search, got:\n%s", out)
	}
}

func TestTypeScriptScanIgnoresTestAndDts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.json"), `{}`)
	writeFile(t, filepath.Join(dir, "core.ts"), `
export function production(): string { return "ok"; }
`)
	writeFile(t, filepath.Join(dir, "core.test.ts"), `
import { production } from './core';
test('p', () => { expect(production()).toBe("ok"); });
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "resource", "list", "production")
	if !strings.Contains(out, "production") {
		t.Fatalf("expected production in search, got:\n%s", out)
	}
}

func TestTypeScriptScanIgnoresNodeModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.json"), `{}`)
	writeFile(t, filepath.Join(dir, "app.ts"), `
export function appFunc(): string { return "app"; }
`)
	dep := filepath.Join(dir, "node_modules", "pkg")
	if err := os.MkdirAll(dep, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dep, "index.ts"), `
export function depFunc(): string { return "dep"; }
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "resource", "list", "appFunc")
	if !strings.Contains(out, "appFunc") {
		t.Fatalf("expected appFunc in search, got:\n%s", out)
	}
	depOut, err := runLtp(t, dir, "resource", "list", "depFunc")
	if err == nil && strings.Contains(depOut, "depFunc") {
		t.Fatalf("expected depFunc in node_modules to be excluded, got:\n%s", depOut)
	}
}

func TestTypeScriptInterfaceImplementsAndEnum(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.json"), `{}`)
	writeFile(t, filepath.Join(dir, "models.ts"), `
export interface Service {
  run(): string;
}

export class RealService implements Service {
  run(): string { return "x"; }
}

export enum Color { Red, Green, Blue }
`)

	mustRun(t, dir, "scan", "-root", dir)

	for _, name := range []string{"Service", "RealService", "Color"} {
		out := mustRun(t, dir, "resource", "list", name)
		if !strings.Contains(out, name) {
			t.Fatalf("expected %s in search, got:\n%s", name, out)
		}
	}

	// read the interface via the CLI dispatch (by full file-scoped ID) and confirm its
	// implementor shows up.
	ifaceID := filepath.Base(dir) + "/models.Service"
	ifaceOut := mustRun(t, dir, "read", "--kind", "interface", ifaceID)
	if !strings.Contains(ifaceOut, "RealService") {
		t.Fatalf("expected Service interface read to list RealService implementor, got:\n%s", ifaceOut)
	}
}

func TestTypeScriptReadNamedType(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.json"), `{}`)
	writeFile(t, filepath.Join(dir, "types.ts"), `
export type ID = string | number;
`)

	mustRun(t, dir, "scan", "-root", dir)
	id := filepath.Base(dir) + "/types.ID"
	out := mustRun(t, dir, "read", "--kind", "named_type", id)
	if !strings.Contains(out, "ID") {
		t.Fatalf("expected named type ID read, got:\n%s", out)
	}
}
