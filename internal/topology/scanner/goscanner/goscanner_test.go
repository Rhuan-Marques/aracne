package goscanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoScannerName(t *testing.T) {
	s := NewGoScanner()
	if s.Name() != "go" {
		t.Errorf("expected name 'go', got %q", s.Name())
	}
}

func TestGoScannerExtensions(t *testing.T) {
	s := NewGoScanner()
	exts := s.Extensions()
	if len(exts) != 1 || exts[0] != ".go" {
		t.Errorf("expected [.go], got %v", exts)
	}
}

func TestGoScannerDetect(t *testing.T) {
	s := NewGoScanner()

	dir := t.TempDir()

	if s.Detect(dir) {
		t.Error("expected false for dir without go.mod")
	}

	goModPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if !s.Detect(dir) {
		t.Error("expected true for dir with go.mod")
	}
}

func TestReadModulePath(t *testing.T) {
	dir := t.TempDir()
	goModPath := filepath.Join(dir, "go.mod")
	os.WriteFile(goModPath, []byte("module github.com/foo/bar\n\ngo 1.21\n"), 0644)

	modPath, err := readModulePath(dir)
	if err != nil {
		t.Fatalf("readModulePath: %v", err)
	}
	if modPath != "github.com/foo/bar" {
		t.Errorf("expected 'github.com/foo/bar', got %q", modPath)
	}
}

func TestReadModulePathNoGoMod(t *testing.T) {
	dir := t.TempDir()
	_, err := readModulePath(dir)
	if err == nil {
		t.Error("expected error for dir without go.mod")
	}
}

func TestReadModulePathNoModule(t *testing.T) {
	dir := t.TempDir()
	goModPath := filepath.Join(dir, "go.mod")
	os.WriteFile(goModPath, []byte("// just a comment\n"), 0644)

	_, err := readModulePath(dir)
	if err == nil {
		t.Error("expected error for go.mod without module directive")
	}
}

func TestGetPackagePath(t *testing.T) {
	root := "/project"
	modulePath := "github.com/foo/bar"

	rootPkg := getPackagePath(root, root, modulePath)
	if string(rootPkg) != "github.com/foo/bar" {
		t.Errorf("expected root package 'github.com/foo/bar', got %q", rootPkg)
	}
}

func TestCollectGoFiles(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "util.go"), []byte("package util"), 0644)
	os.WriteFile(filepath.Join(dir, "main_test.go"), []byte("package main"), 0644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0755)
	os.WriteFile(filepath.Join(dir, "subdir", "sub.go"), []byte("package sub"), 0644)

	files := collectGoFiles(dir)
	if len(files) != 3 {
		t.Errorf("expected 3 non-test .go files, got %d", len(files))
	}
}

func TestCollectGoFilesSkipsVendor(t *testing.T) {
	dir := t.TempDir()

	os.Mkdir(filepath.Join(dir, "vendor"), 0755)
	os.WriteFile(filepath.Join(dir, "vendor", "dep.go"), []byte("package dep"), 0644)

	files := collectGoFiles(dir)
	if len(files) != 0 {
		t.Errorf("expected 0 files from vendor-only dir, got %d", len(files))
	}
}

func TestRemoveString(t *testing.T) {
	result := removeString([]string{"a", "b", "c"}, "b")
	if len(result) != 2 || result[0] != "a" || result[1] != "c" {
		t.Errorf("removeString([a b c], b) = %v, want [a c]", result)
	}

	noChange := removeString([]string{"a", "b"}, "c")
	if len(noChange) != 2 {
		t.Errorf("expected no change, got %v", noChange)
	}
}

func TestRemoveStrings(t *testing.T) {
	result := removeStrings([]string{"a", "b", "c", "b"}, "b", "c")
	if len(result) != 1 || result[0] != "a" {
		t.Errorf("removeStrings([a b c b], b, c) = %v, want [a]", result)
	}

	emptyArgs := removeStrings([]string{"a", "b"})
	if len(emptyArgs) != 2 {
		t.Errorf("expected no change with empty items, got %v", emptyArgs)
	}
}
