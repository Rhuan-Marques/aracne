package topology_test

import (
	"os"
	"path/filepath"
	"testing"

	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/scanner"
	"aracne/internal/topology/scanner/goscanner"
	"aracne/internal/topology/scanner/pyscanner"
)

func TestMultiLanguageScanAndUpdatePreservesClusters(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/mixed\n\ngo 1.21\n")
	goPath := filepath.Join(root, "main.go")
	pyPath := filepath.Join(root, "tool.py")
	mustWrite(t, goPath, "package main\n\nfunc GoFunc() {}\n")
	mustWrite(t, pyPath, "def py_func():\n    return 1\n")

	dbPath := filepath.Join(t.TempDir(), "topology.db")
	manager := topology.New()
	manager.Load(dbPath)
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())

	if err := manager.FullScan(root, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}
	topo, err := manager.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	assertLanguagePresent(t, topo.Languages, "go")
	assertLanguagePresent(t, topo.Languages, "python")
	assertResourceNamedLanguage(t, topo, "GoFunc", domain.ResourceFunction, "go")
	assertResourceNamedLanguage(t, topo, "py_func", domain.ResourceFunction, "python")

	mustWrite(t, pyPath, "def py_func():\n    return 1\n\ndef py_func2():\n    return 2\n")
	if _, err := manager.UpdateFile(pyPath, reg); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	topo, err = manager.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll after update: %v", err)
	}
	assertResourceNamedLanguage(t, topo, "GoFunc", domain.ResourceFunction, "go")
	assertResourceNamedLanguage(t, topo, "py_func2", domain.ResourceFunction, "python")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertLanguagePresent(t *testing.T, languages []string, want string) {
	t.Helper()
	for _, language := range languages {
		if language == want {
			return
		}
	}
	t.Fatalf("expected language %q in %v", want, languages)
}

func assertResourceNamedLanguage(t *testing.T, topo *domain.Topology, name string, kind domain.ResourceKind, language string) {
	t.Helper()
	for _, res := range topo.Resources {
		if res.Name == name && res.Kind == kind && res.Language == language {
			return
		}
	}
	t.Fatalf("expected %s resource named %q with language %q", kind, name, language)
}
