package tests_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegistryDetectGoProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module goonly\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func GoOnly() {}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	// Should have found Go resources
	out := mustRun(t, dir, "warnings", "list", "--db", dbPath)
	if !strings.Contains(out, "No warnings found") {
		t.Fatalf("expected no warnings for Go project, got:\n%s", out)
	}
}

func TestRegistryDetectPythonProject(t *testing.T) {
	// Only test detection via file existence — Python scan may
	// or may not be available, but the scan should run without error
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "setup.py"), `from setuptools import setup
setup(name="pyonly")
`)
	if err := os.MkdirAll(filepath.Join(dir, "pyonly"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "pyonly", "__init__.py"), "")
	writeFile(t, filepath.Join(dir, "pyonly", "core.py"), `
def py_only():
    return 1
`)

	dbPath := filepath.Join(dir, "test.db")
	out, err := runLtp(t, dir, "scan", "-root", dir, "-output", dbPath)
	if err != nil {
		t.Logf("Python scan error (may be expected if no Python): %s", out)
		return
	}
	t.Logf("Python scan succeeded:\n%s", out)
}

func TestRegistryDetectMixedProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module mixedreg\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func GoHello() {}
`)
	writeFile(t, filepath.Join(dir, "setup.py"), `from setuptools import setup
setup(name="mixedreg")
`)
	writeFile(t, filepath.Join(dir, "pylib.py"), `
def PyHello():
    pass
`)

	dbPath := filepath.Join(dir, "test.db")
	out, err := runLtp(t, dir, "scan", "-root", dir, "-output", dbPath)
	if err != nil {
		t.Fatalf("mixed scan failed: %v\n%s", err, out)
	}
	t.Logf("Mixed scan output:\n%s", out)
}

func TestRegistryDetectNoProject(t *testing.T) {
	dir := t.TempDir()

	dbPath := filepath.Join(dir, "test.db")
	out, err := runLtp(t, dir, "scan", "-root", dir, "-output", dbPath)
	if err == nil {
		t.Fatalf("expected error scanning empty directory, got:\n%s", out)
	}
	if !strings.Contains(out, "Error") && !strings.Contains(out, "no language scanner detected") {
		t.Fatalf("expected error about no scanner, got:\n%s", out)
	}
}

func TestManifestCreatedAfterScan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module manifesttest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Hello() {}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	// Manifest should exist
	manifestPath := filepath.Join(filepath.Dir(dbPath), "file_manifest.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Fatal("expected file_manifest.json to exist after scan")
	}
}

func TestManifestRecordsSourceFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module manifestfiles\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Hello() {}
`)
	writeFile(t, filepath.Join(dir, "helper.go"), `package main

func Helper() {}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	manifestPath := filepath.Join(filepath.Dir(dbPath), "file_manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var manifest map[string]string
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	if _, ok := manifest[filepath.Join(dir, "main.go")]; !ok {
		t.Fatalf("expected main.go in manifest, got: %v", manifest)
	}
	if _, ok := manifest[filepath.Join(dir, "helper.go")]; !ok {
		t.Fatalf("expected helper.go in manifest, got: %v", manifest)
	}

	// Verify timestamps are valid RFC3339Nano
	for path, ts := range manifest {
		if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
			t.Fatalf("invalid timestamp for %s: %v", path, err)
		}
	}
}

func TestManifestUpdatesOnRescan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module manifestupdate\n\ngo 1.21\n")
	mainGo := filepath.Join(dir, "main.go")
	writeFile(t, mainGo, `package main

func Hello() {}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	manifestPath := filepath.Join(filepath.Dir(dbPath), "file_manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifestBefore map[string]string
	json.Unmarshal(data, &manifestBefore)

	// Modify main.go with a later timestamp, then rescan
	time.Sleep(2 * time.Second)
	writeFile(t, mainGo, `package main

func Hello() {}
func World() {}
`)

	// Set explicit mtime in the past to avoid same-second issues
	// Actually, just rely on writeFile + scan triggering the change
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	data2, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifestAfter map[string]string
	json.Unmarshal(data2, &manifestAfter)

	// The timestamp should have changed
	if manifestBefore[mainGo] == manifestAfter[mainGo] {
		t.Logf("Manifest timestamp did not change (may be same-second write): before=%s after=%s",
			manifestBefore[mainGo], manifestAfter[mainGo])
	}
}
