package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJavaScriptScanDetectsByPackageJson(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"name":"mypkg","version":"1.0.0"}`)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "src", "core.js"), `
export function greet(name) {
  return "Hello " + name;
}
`)

	mustRun(t, dir, "scan", "-root", dir)
	out := mustRun(t, dir, "resource", "list", "greet")
	if !strings.Contains(out, "greet") {
		t.Fatalf("expected greet in search, got:\n%s", out)
	}
}

func TestJavaScriptScanIgnoresTestFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"name":"mypkg"}`)
	writeFile(t, filepath.Join(dir, "core.js"), `
export function production() {
  return "ok";
}
`)
	writeFile(t, filepath.Join(dir, "core.test.js"), `
import { production } from './core';
test('production', () => { expect(production()).toBe("ok"); });
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "resource", "list", "production")
	if !strings.Contains(out, "production") {
		t.Fatalf("expected production function in search, got:\n%s", out)
	}
}

func TestJavaScriptScanIgnoresNodeModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"name":"mypkg"}`)
	writeFile(t, filepath.Join(dir, "app.js"), `
export function appFunc() {
  return "app";
}
`)
	dep := filepath.Join(dir, "node_modules", "left-pad")
	if err := os.MkdirAll(dep, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dep, "index.js"), `
export function depFunc() {
  return "dep";
}
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

func TestJavaScriptClassInheritance(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"name":"mypkg"}`)
	writeFile(t, filepath.Join(dir, "models.js"), `
export class Base {
  method() { return "base"; }
}

export class Child extends Base {
  extra() { return 1; }
}
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "resource", "list", "Base")
	if !strings.Contains(out, "Base") {
		t.Fatalf("expected Base in search, got:\n%s", out)
	}
	out2 := mustRun(t, dir, "resource", "list", "Child")
	if !strings.Contains(out2, "Child") {
		t.Fatalf("expected Child in search, got:\n%s", out2)
	}
}

func TestJavaScriptConstructorDetection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"name":"mypkg"}`)
	writeFile(t, filepath.Join(dir, "service.js"), `
export class Service {
  constructor(name) { this.name = name; }
  run() { return this.name; }
}
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "resource", "list", "Service")
	if !strings.Contains(out, "Service") {
		t.Fatalf("expected Service in search, got:\n%s", out)
	}
	out2 := mustRun(t, dir, "resource", "list", "run")
	if !strings.Contains(out2, "run") {
		t.Fatalf("expected run method in search, got:\n%s", out2)
	}
}
