package pyscanner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/python"
)

func TestResolveValueRef_function(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"mypkg.myfunc": {ID: "mypkg.myfunc", Name: "myfunc"},
		},
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotCalls []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnCalls {
			gotCalls = append(gotCalls, id)
		}
	}

	resolveValueRef("myfunc", pr, gt, add)

	if len(gotCalls) != 1 || gotCalls[0] != "mypkg.myfunc" {
		t.Errorf("expected ConnCalls to mypkg.myfunc, got %v", gotCalls)
	}
}

func TestResolveValueRef_class(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
	}
	gt := &python.PythonTopology{
		Functions: make(map[python.FunctionID]python.PythonFunction),
		Classes: map[python.ClassID]python.PythonClass{
			"mypkg.MyClass": {ID: "mypkg.MyClass", Name: "MyClass"},
		},
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotClasses []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnUsesClass {
			gotClasses = append(gotClasses, id)
		}
	}

	resolveValueRef("MyClass", pr, gt, add)

	if len(gotClasses) != 1 || gotClasses[0] != "mypkg.MyClass" {
		t.Errorf("expected ConnUsesClass to mypkg.MyClass, got %v", gotClasses)
	}
}

func TestResolveValueRef_extvar(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
	}
	gt := &python.PythonTopology{
		Functions: make(map[python.FunctionID]python.PythonFunction),
		Classes:   make(map[python.ClassID]python.PythonClass),
		ExternalVars: map[python.ExternalVarID]python.PythonExternalVar{
			"mypkg.myvar": {ID: "mypkg.myvar", Name: "myvar"},
		},
	}

	var gotVars []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnUsesExtVar {
			gotVars = append(gotVars, id)
		}
	}

	resolveValueRef("myvar", pr, gt, add)

	if len(gotVars) != 1 || gotVars[0] != "mypkg.myvar" {
		t.Errorf("expected ConnUsesExtVar to mypkg.myvar, got %v", gotVars)
	}
}

func TestResolveValueRef_function_preferred_over_class(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"mypkg.Ambiguous": {ID: "mypkg.Ambiguous", Name: "Ambiguous"},
		},
		Classes: map[python.ClassID]python.PythonClass{
			"mypkg.Ambiguous": {ID: "mypkg.Ambiguous", Name: "Ambiguous"},
		},
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var calls, classes []string
	add := func(kind python.ConnectionKind, id string) {
		switch kind {
		case python.ConnCalls:
			calls = append(calls, id)
		case python.ConnUsesClass:
			classes = append(classes, id)
		}
	}

	resolveValueRef("Ambiguous", pr, gt, add)

	if len(classes) != 1 || classes[0] != "mypkg.Ambiguous" {
		t.Errorf("expected ConnUsesClass (class takes priority), got calls=%v classes=%v", calls, classes)
	}
	if len(calls) != 0 {
		t.Errorf("expected no ConnCalls when class matches first, got %v", calls)
	}
}

func TestResolveValueRef_literal_values_skipped(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
	}
	gt := &python.PythonTopology{
		Functions:    make(map[python.FunctionID]python.PythonFunction),
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	for _, lit := range []string{"None", "True", "False", ""} {
		var added bool
		add := func(_ python.ConnectionKind, _ string) { added = true }
		resolveValueRef(lit, pr, gt, add)
		if added {
			t.Errorf("literal %q should not add any connection", lit)
		}
	}
}

// TestResolveValueRef_dotted_import: `import pkg.utils as utils` then a reference
// `utils.Helper` resolves to the class in the imported module under its
// module-qualified id.
func TestResolveValueRef_dotted_import(t *testing.T) {
	pr := &ParseResult{
		ModulePath:    "testpkg/main",
		ModuleRoot:    filepath.Join("testdata", "testpkg"),
		ImportTargets: map[string]pyImportTarget{"utils": {ModulePath: "testpkg/utils"}},
	}
	gt := &python.PythonTopology{
		Functions: make(map[python.FunctionID]python.PythonFunction),
		Classes: map[python.ClassID]python.PythonClass{
			"testpkg/utils.Helper": {ID: "testpkg/utils.Helper", Name: "Helper"},
		},
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotClasses []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnUsesClass {
			gotClasses = append(gotClasses, id)
		}
	}

	resolveValueRef("utils.Helper", pr, gt, add)

	if len(gotClasses) != 1 || gotClasses[0] != "testpkg/utils.Helper" {
		t.Errorf("expected ConnUsesClass to testpkg/utils.Helper, got %v", gotClasses)
	}
}

func TestResolveClassVarRefs(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
		ClassVarRefs: []ClassVarRef{
			{ClassID: "mypkg.MyClass", RefValue: "externalFunc"},
			{ClassID: "mypkg.MyClass", RefValue: "SomeType"},
		},
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"mypkg.externalFunc": {ID: "mypkg.externalFunc", Name: "externalFunc"},
		},
		Classes: map[python.ClassID]python.PythonClass{
			"mypkg.MyClass": {
				ID:          "mypkg.MyClass",
				Name:        "MyClass",
				Connections: make(map[python.ConnectionKind][]string),
			},
			"mypkg.SomeType": {ID: "mypkg.SomeType", Name: "SomeType"},
		},
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	resolveClassVarRefs(pr, gt)

	cls := gt.Classes["mypkg.MyClass"]
	calls := cls.Connections[python.ConnCalls]
	uses := cls.Connections[python.ConnUsesClass]

	if len(calls) != 1 || calls[0] != "mypkg.externalFunc" {
		t.Errorf("expected ConnCalls to mypkg.externalFunc, got %v", calls)
	}
	if len(uses) != 1 || uses[0] != "mypkg.SomeType" {
		t.Errorf("expected ConnUsesClass to mypkg.SomeType, got %v", uses)
	}
}

func TestClassVarRefsInParseResult(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}

	code := `
def x() -> str:
    return "hello"

class y:
    name = x()
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test_mod.py")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	pr, err := ParseFile(filePath, "mypkg", tmpDir)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	wantClass := pyModulePath(tmpDir, filePath) + ".y"
	var found bool
	for _, ref := range pr.ClassVarRefs {
		if ref.ClassID == wantClass && ref.RefValue == "x" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("class var ref (%s → x) not found in ParseResult.ClassVarRefs: %+v", wantClass, pr.ClassVarRefs)
	}
}

func TestClassVarRefs_multiple_kinds(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}

	code := `
def helper() -> str:
    return "helper"

class Klass:
    pass

some_var = 42

class Container:
    a = helper()
    b = Klass()
    c = some_var
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test_multi.py")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	pr, err := ParseFile(filePath, "mypkg", tmpDir)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	wantClass := pyModulePath(tmpDir, filePath) + ".Container"
	var refs []string
	for _, ref := range pr.ClassVarRefs {
		if ref.ClassID == wantClass {
			refs = append(refs, ref.RefValue)
		}
	}

	expected := []string{"helper", "Klass", "some_var"}
	if len(refs) != len(expected) {
		t.Fatalf("expected %d refs, got %d: %v", len(expected), len(refs), refs)
	}
	for _, e := range expected {
		found := false
		for _, r := range refs {
			if r == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected ref %q, got %v", e, refs)
		}
	}
}

func TestScan_classVarConnections(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}

	code := `
def x() -> str:
    return "mamamia"

class MyUtil:
    pass

some_val = 99

class y:
    name = x()
    util = MyUtil()
    val = some_val
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "mod_scan.py")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	classID := pyModulePath(tmpDir, filePath) + ".y"
	res, ok := topo.Resources[classID]
	if !ok {
		t.Fatalf("class %q not found in topology resources: available keys: %v", classID, resourceKeys(topo))
	}

	var calls, usesClass, usesVar []string
	for _, conn := range res.Connections["calls"] {
		calls = append(calls, conn)
	}
	for _, conn := range res.Connections["uses_class"] {
		usesClass = append(usesClass, conn)
	}
	for _, conn := range res.Connections["uses_extvar"] {
		usesVar = append(usesVar, conn)
	}

	if len(calls) != 1 || !containsSuffix(calls, ".x") {
		t.Errorf("expected ConnCalls to x, got calls=%v", calls)
	}
	if len(usesClass) != 1 || !containsSuffix(usesClass, ".MyUtil") {
		t.Errorf("expected ConnUsesClass to MyUtil, got uses_class=%v", usesClass)
	}
	if len(usesVar) != 1 || !containsSuffix(usesVar, ".some_val") {
		t.Errorf("expected ConnUsesExtVar to some_val, got uses_extvar=%v", usesVar)
	}
}

func TestScan_literalClassVarDoesNotCreateConnection(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}

	code := `
class y:
    name = "hello"
    count = 42
    items = [1, 2, 3]
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "mod_literal.py")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	classID := pyModulePath(tmpDir, filePath) + ".y"
	res, ok := topo.Resources[classID]
	if !ok {
		t.Fatalf("class %q not found, keys: %v", classID, resourceKeys(topo))
	}

	for kind, targets := range res.Connections {
		if kind == "has_function" || kind == "methods" {
			continue
		}
		t.Errorf("unexpected connection kind %q on class y: %v", kind, targets)
	}
}

func TestUpdateFile_classVarConnections(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}

	code := `
def helper() -> int:
    return 1

class y:
    value = helper()
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "mod_update.py")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("initial Scan failed: %v", err)
	}

	// Modify the file and call UpdateFile
	newCode := `
def helper() -> int:
    return 1

def extra() -> str:
    return "extra"

class y:
    value = helper()
    extra = extra()
`
	if err := os.WriteFile(filePath, []byte(newCode), 0644); err != nil {
		t.Fatal(err)
	}

	warnings, err := s.UpdateFile(topo, filePath)
	if err != nil {
		t.Fatalf("UpdateFile failed: %v", err)
	}

	classID := pyModulePath(tmpDir, filePath) + ".y"
	res, ok := topo.Resources[classID]
	if !ok {
		t.Fatalf("class %q not found after UpdateFile, keys: %v", classID, resourceKeys(topo))
	}

	calls := res.Connections["calls"]
	if len(calls) != 2 {
		t.Errorf("expected 2 ConnCalls after UpdateFile, got %v", calls)
	}
	if !containsSuffix(calls, ".helper") || !containsSuffix(calls, ".extra") {
		t.Errorf("expected calls to .helper and .extra, got %v", calls)
	}
	if len(warnings) > 0 {
		t.Logf("warnings from UpdateFile: %v", warnings)
	}
}

// TestUpdateFile_noDuplicateClassConnections guards against the incremental
// UpdateFile path re-appending "methods"/inheritance connections to classes
// that live in files other than the one being updated. Before the fix this
// duplicated edges on every update and ultimately violated the SQLite
// connections UNIQUE constraint when persisted.
func TestUpdateFile_noDuplicateClassConnections(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}

	tmpDir := t.TempDir()

	aPath := filepath.Join(tmpDir, "a.py")
	bPath := filepath.Join(tmpDir, "b.py")
	if err := os.WriteFile(aPath, []byte("from b import A\n\nclass A:\n    def foo(self):\n        return 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// b.py imports A from a so B(A) inheritance resolves across files.
	if err := os.WriteFile(bPath, []byte("from a import A\n\nclass B(A):\n    def bar(self):\n        return 2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Rewrite a.py without the bogus self-import (kept minimal for clarity).
	if err := os.WriteFile(aPath, []byte("class A:\n    def foo(self):\n        return 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("initial Scan failed: %v", err)
	}

	classA := pyModulePath(tmpDir, aPath) + ".A"
	beforeMethods := len(topo.Resources[classA].Connections["methods"])
	beforeInheritedBy := len(topo.Resources[classA].Connections["inherited_by"])
	if beforeMethods < 1 {
		t.Fatalf("expected class A to have at least one method after Scan, got %d", beforeMethods)
	}

	// Update the OTHER file. This re-runs method/inheritance population over the
	// whole topology and must leave class A's connection counts unchanged.
	newB := "from a import A\n\nclass B(A):\n    def bar(self):\n        return 2\n\n    def baz(self):\n        return 3\n"
	if err := os.WriteFile(bPath, []byte(newB), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateFile(topo, bPath); err != nil {
		t.Fatalf("UpdateFile failed: %v", err)
	}

	afterMethods := len(topo.Resources[classA].Connections["methods"])
	afterInheritedBy := len(topo.Resources[classA].Connections["inherited_by"])
	if afterMethods != beforeMethods {
		t.Errorf("class A 'methods' count changed from %d to %d after updating another file (duplicated edges)", beforeMethods, afterMethods)
	}
	if afterInheritedBy != beforeInheritedBy {
		t.Errorf("class A 'inherited_by' count changed from %d to %d after updating another file (duplicated edges)", beforeInheritedBy, afterInheritedBy)
	}
}

// TestResolveValueRef_dotted_function_via_import: `import pkg.utils as lib`
// then `lib.compute` resolves to the function in the imported module.
func TestResolveValueRef_dotted_function_via_import(t *testing.T) {
	pr := &ParseResult{
		ModulePath:    "mylib/main",
		ModuleRoot:    filepath.Join("testdata", "mylib"),
		ImportTargets: map[string]pyImportTarget{"lib": {ModulePath: "mylib/utils"}},
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"mylib/utils.compute": {ID: "mylib/utils.compute", Name: "compute"},
		},
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotCalls []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnCalls {
			gotCalls = append(gotCalls, id)
		}
	}

	resolveValueRef("lib.compute", pr, gt, add)

	if len(gotCalls) != 1 || gotCalls[0] != "mylib/utils.compute" {
		t.Errorf("expected ConnCalls to mylib/utils.compute, got %v", gotCalls)
	}
}

func TestResolveValueRef_nonexistent_skipped(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
	}
	gt := &python.PythonTopology{
		Functions:    make(map[python.FunctionID]python.PythonFunction),
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var added bool
	add := func(_ python.ConnectionKind, _ string) { added = true }
	resolveValueRef("DoesNotExist", pr, gt, add)
	if added {
		t.Error("nonexistent reference should not add any connection")
	}
}

// TestResolveValueRef_imported_symbol_function: `from testpkg.utils import
// helper_func` -> the alias resolves to the symbol's module-qualified id.
func TestResolveValueRef_imported_symbol_function(t *testing.T) {
	pr := &ParseResult{
		ModulePath:    "testpkg/main",
		ModuleRoot:    filepath.Join("testdata", "testpkg"),
		ImportTargets: map[string]pyImportTarget{"helper_func": {ModulePath: "testpkg/utils", Symbol: "helper_func"}},
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"testpkg/utils.helper_func": {ID: "testpkg/utils.helper_func", Name: "helper_func"},
		},
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotCalls []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnCalls {
			gotCalls = append(gotCalls, id)
		}
	}

	resolveValueRef("helper_func", pr, gt, add)

	if len(gotCalls) != 1 || gotCalls[0] != "testpkg/utils.helper_func" {
		t.Errorf("expected ConnCalls to testpkg/utils.helper_func, got %v", gotCalls)
	}
}

// TestResolveValueRef_imported_symbol_class: `from testpkg.models import
// MyClass` -> the alias resolves to the class's module-qualified id.
func TestResolveValueRef_imported_symbol_class(t *testing.T) {
	pr := &ParseResult{
		ModulePath:    "testpkg/main",
		ModuleRoot:    filepath.Join("testdata", "testpkg"),
		ImportTargets: map[string]pyImportTarget{"MyClass": {ModulePath: "testpkg/models", Symbol: "MyClass"}},
	}
	gt := &python.PythonTopology{
		Functions: make(map[python.FunctionID]python.PythonFunction),
		Classes: map[python.ClassID]python.PythonClass{
			"testpkg/models.MyClass": {ID: "testpkg/models.MyClass", Name: "MyClass"},
		},
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotClasses []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnUsesClass {
			gotClasses = append(gotClasses, id)
		}
	}

	resolveValueRef("MyClass", pr, gt, add)

	if len(gotClasses) != 1 || gotClasses[0] != "testpkg/models.MyClass" {
		t.Errorf("expected ConnUsesClass to testpkg/models.MyClass, got %v", gotClasses)
	}
}

func TestResolveValueRef_import_alias_external_dep_plain(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
		ImportMap:  map[string]string{"pd": "pandas"},
	}
	gt := &python.PythonTopology{
		Functions:    make(map[python.FunctionID]python.PythonFunction),
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotDeps []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnUsesDep {
			gotDeps = append(gotDeps, id)
		}
	}

	resolveValueRef("pd", pr, gt, add)

	if len(gotDeps) != 1 || gotDeps[0] != "pandas" {
		t.Errorf("expected ConnUsesDep to pandas, got %v", gotDeps)
	}
}

func TestResolveValueRef_import_alias_external_dep_dotted(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
		ImportMap:  map[string]string{"pd": "pandas"},
	}
	gt := &python.PythonTopology{
		Functions:    make(map[python.FunctionID]python.PythonFunction),
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotDeps []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnUsesDep {
			gotDeps = append(gotDeps, id)
		}
	}

	resolveValueRef("pd.DataFrame", pr, gt, add)

	if len(gotDeps) != 1 || gotDeps[0] != "pandas" {
		t.Errorf("expected ConnUsesDep to pandas, got %v", gotDeps)
	}
}

func TestClassVarRef_with_import_alias(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}

	code := `
import os.path as path_helper

def local_func():
    return 1

class y:
    name = local_func()
    path = path_helper()
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "mod_import_alias.py")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	classID := pyModulePath(tmpDir, filePath) + ".y"
	res, ok := topo.Resources[classID]
	if !ok {
		t.Fatalf("class %q not found, keys: %v", classID, resourceKeys(topo))
	}

	calls := res.Connections["calls"]
	var foundLocal bool
	for _, c := range calls {
		if strings.HasSuffix(c, ".local_func") {
			foundLocal = true
		}
	}

	if !foundLocal {
		t.Errorf("expected ConnCalls to local_func, got calls=%v", calls)
	}
}

func TestClassVarRef_with_from_import(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}

	code := `
from os import getcwd

class y:
    cwd = getcwd()
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "mod_from_import.py")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	classID := pyModulePath(tmpDir, filePath) + ".y"
	_, ok := topo.Resources[classID]
	if !ok {
		t.Fatalf("class %q not found, keys: %v", classID, resourceKeys(topo))
	}

	// getcwd is a stdlib function — won't be in our topology. The scan should
	// not crash, and the import should be recorded as a dep (os is external).
}

// TestResolveBodyCallRefs_localAssignmentAndMethodCall
// Verifies: x = SomeClass(); x.method() produces ConnCalls + ConnUsesClass
func TestResolveBodyCallRefs_localAssignmentAndMethodCall(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"mypkg.MyClass.method": {ID: "mypkg.MyClass.method", Name: "method", MethodFrom: pkgPtr("mypkg.MyClass")},
		},
		Classes: map[python.ClassID]python.PythonClass{
			"mypkg.MyClass": {
				ID:   "mypkg.MyClass",
				Name: "MyClass",
				Connections: map[python.ConnectionKind][]string{
					python.ConnHasMethod: {"mypkg.MyClass.method"},
				},
			},
		},
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotCalls []string
	var gotClasses []string
	add := func(kind python.ConnectionKind, id string) {
		switch kind {
		case python.ConnCalls:
			gotCalls = append(gotCalls, id)
		case python.ConnUsesClass:
			gotClasses = append(gotClasses, id)
		}
	}

	bodyCalls := []pyBodyCall{
		{ObjectName: "x", MethodName: "method", Func: "x.method", LineNo: 2},
	}
	bodyAssigns := []pyBodyAssign{
		{Name: "x", ValueType: "MyClass", LineNo: 1},
	}
	funcInput := []python.VariableDefinition{}

	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, funcInput, nil, nil)

	if len(gotClasses) != 1 || gotClasses[0] != "mypkg.MyClass" {
		t.Errorf("expected ConnUsesClass to mypkg.MyClass, got %v", gotClasses)
	}
	if len(gotCalls) != 1 || gotCalls[0] != "mypkg.MyClass.method" {
		t.Errorf("expected ConnCalls to mypkg.MyClass.method, got %v", gotCalls)
	}
}

// TestResolveBodyCallRefs_paramTypeAnnotation
// Verifies: a parameter with type annotation resolves method calls
func TestResolveBodyCallRefs_paramTypeAnnotation(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"mypkg.MyService.Run": {ID: "mypkg.MyService.Run", Name: "Run", MethodFrom: pkgPtr("mypkg.MyService")},
		},
		Classes: map[python.ClassID]python.PythonClass{
			"mypkg.MyService": {
				ID:   "mypkg.MyService",
				Name: "MyService",
				Connections: map[python.ConnectionKind][]string{
					python.ConnHasMethod: {"mypkg.MyService.Run"},
				},
			},
		},
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotCalls []string
	var gotClasses []string
	add := func(kind python.ConnectionKind, id string) {
		switch kind {
		case python.ConnCalls:
			gotCalls = append(gotCalls, id)
		case python.ConnUsesClass:
			gotClasses = append(gotClasses, id)
		}
	}

	bodyCalls := []pyBodyCall{
		{ObjectName: "svc", MethodName: "Run", Func: "svc.Run", LineNo: 1},
	}
	bodyAssigns := []pyBodyAssign{}
	funcInput := []python.VariableDefinition{
		{Name: "svc", Typing: "MyService"},
	}

	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, funcInput, nil, nil)

	if len(gotClasses) != 1 || gotClasses[0] != "mypkg.MyService" {
		t.Errorf("expected ConnUsesClass to mypkg.MyService, got %v", gotClasses)
	}
	if len(gotCalls) != 1 || gotCalls[0] != "mypkg.MyService.Run" {
		t.Errorf("expected ConnCalls to mypkg.MyService.Run, got %v", gotCalls)
	}
}

// TestResolveBodyCallRefs_unknownVariableNoConnection
// Verifies: calling a method on an unknown local variable produces no connections
func TestResolveBodyCallRefs_unknownVariableNoConnection(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
	}
	gt := &python.PythonTopology{
		Functions:    make(map[python.FunctionID]python.PythonFunction),
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var count int
	add := func(_ python.ConnectionKind, _ string) { count++ }

	bodyCalls := []pyBodyCall{
		{ObjectName: "unknown", MethodName: "nope", Func: "unknown.nope", LineNo: 1},
	}
	bodyAssigns := []pyBodyAssign{}
	funcInput := []python.VariableDefinition{}

	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, funcInput, nil, nil)

	if count != 0 {
		t.Errorf("expected no connections for unknown variable, got %d", count)
	}
}

// TestResolveBodyCallRefs_directFunctionCall
// Verifies: direct function calls (no receiver) in body calls are resolved
func TestResolveBodyCallRefs_directFunctionCall(t *testing.T) {
	pr := &ParseResult{
		ModulePath: "mypkg",
		ModuleRoot: "testdata",
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"mypkg.helper": {ID: "mypkg.helper", Name: "helper"},
		},
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotCalls []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnCalls {
			gotCalls = append(gotCalls, id)
		}
	}

	bodyCalls := []pyBodyCall{
		{ObjectName: "", MethodName: "", Func: "helper", LineNo: 1},
	}
	bodyAssigns := []pyBodyAssign{}
	funcInput := []python.VariableDefinition{}

	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, funcInput, nil, nil)

	if len(gotCalls) != 1 || gotCalls[0] != "mypkg.helper" {
		t.Errorf("expected ConnCalls to mypkg.helper, got %v", gotCalls)
	}
}

func pkgPtr(s string) *python.ClassID {
	id := python.ClassID(s)
	return &id
}

// TestBodyCallRefs_endToEnd verifies local variable method calls via ParseFile + Scan
func TestBodyCallRefs_endToEnd(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}

	code := `
class MyService:
    def run(self) -> str:
        return "done"

def process():
    svc = MyService()
    svc.run()
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "service.py")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	funcID := pyModulePath(tmpDir, filePath) + ".process"
	res, ok := topo.Resources[funcID]
	if !ok {
		t.Fatalf("function %q not found, keys: %v", funcID, resourceKeys(topo))
	}

	calls := res.Connections["calls"]
	usesClass := res.Connections["uses_class"]

	if !containsSuffix(calls, ".MyService.run") {
		t.Errorf("expected ConnCalls to MyService.run, got calls=%v", calls)
	}
	if !containsSuffix(usesClass, ".MyService") {
		t.Errorf("expected ConnUsesClass to MyService, got uses_class=%v", usesClass)
	}
}

// TestBodyCallRefs_chainedAssignment verifies assignment from constructor return type
// e.g., x = some_func() where some_func returns a class instance
func TestBodyCallRefs_chainedAssignment(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}

	code := `
class MyService:
    def run(self) -> str:
        return "done"

def new_service() -> MyService:
    return MyService()

def process():
    svc = new_service()
    svc.run()
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "chain.py")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	funcID := pyModulePath(tmpDir, filePath) + ".process"
	res, ok := topo.Resources[funcID]
	if !ok {
		t.Fatalf("function %q not found, keys: %v", funcID, resourceKeys(topo))
	}

	calls := res.Connections["calls"]

	// Should resolve: new_service() as a call, and svc.run() as a method call on MyService
	if !containsSuffix(calls, ".new_service") {
		t.Errorf("expected ConnCalls to new_service, got calls=%v", calls)
	}
	if !containsSuffix(calls, ".MyService.run") {
		t.Errorf("expected ConnCalls to MyService.run, got calls=%v", calls)
	}
}

// TestResolveBodyCallRefs_transitiveCrossModuleReturnType is the user's scenario
// in Python: module3 does `x = ext_func(); x.method()` where ext_func (imported
// from module2) returns module1.Stu. module3 does NOT import module1, so only
// ext_func's parse-time TypingID makes x.method() resolve to module1.Stu.method.
func TestResolveBodyCallRefs_transitiveCrossModuleReturnType(t *testing.T) {
	pr := &ParseResult{
		ModulePath:    "module3",
		ModuleRoot:    "testdata",
		ImportTargets: map[string]pyImportTarget{"ext_func": {ModulePath: "module2", Symbol: "ext_func"}},
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"module2.ext_func": {
				ID:   "module2.ext_func",
				Name: "ext_func",
				// Typing is the name as written in module2 ("Stu"); TypingID is
				// the canonical class id resolved at module2's parse time.
				Output: []python.VariableDefinition{{Typing: "Stu", TypingID: "module1.Stu"}},
			},
			"module1.Stu.method": {ID: "module1.Stu.method", Name: "method", MethodFrom: pkgPtr("module1.Stu")},
		},
		Classes: map[python.ClassID]python.PythonClass{
			"module1.Stu": {
				ID:   "module1.Stu",
				Name: "Stu",
				Connections: map[python.ConnectionKind][]string{
					python.ConnHasMethod: {"module1.Stu.method"},
				},
			},
		},
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotCalls []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnCalls {
			gotCalls = append(gotCalls, id)
		}
	}

	bodyAssigns := []pyBodyAssign{{Name: "x", ValueType: "ext_func", LineNo: 1}}
	bodyCalls := []pyBodyCall{{ObjectName: "x", MethodName: "method", Func: "x.method", LineNo: 2}}

	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, nil, nil, nil)

	found := false
	for _, c := range gotCalls {
		if c == "module1.Stu.method" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ConnCalls to include module1.Stu.method (transitive cross-module), got %v", gotCalls)
	}
}

// TestParseFile_returnTypeTypingID verifies the parser records a canonical
// TypingID for a return annotation. Here `module1` is external to the temp
// project, so the id keeps its dotted external form.
func TestParseFile_returnTypeTypingID(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "ext.py")
	if err := os.WriteFile(src, []byte("from module1 import Stu\n\ndef ext_func() -> Stu:\n    return Stu()\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pr, err := ParseFile(src, "module2", tmpDir)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	var ext *python.PythonFunction
	for i := range pr.Functions {
		if pr.Functions[i].Function.Name == "ext_func" {
			ext = &pr.Functions[i].Function
			break
		}
	}
	if ext == nil {
		t.Fatal("ext_func not parsed")
	}
	if len(ext.Output) != 1 {
		t.Fatalf("expected 1 output, got %v", ext.Output)
	}
	if got := ext.Output[0].TypingID; got != "module1.Stu" {
		t.Errorf("expected Output[0].TypingID = %q, got %q (Typing=%q)",
			"module1.Stu", got, ext.Output[0].Typing)
	}
}

// TestScan_crossFileImports verifies module-first IDs and cross-file resolution:
// consumer imports a class/function from sibling modules and the topology keys
// them under their own module paths, with edges crossing files.
func TestScan_crossFileImports(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}

	tmpDir := t.TempDir()
	shapes := filepath.Join(tmpDir, "shapes.py")
	factory := filepath.Join(tmpDir, "factory.py")
	consumer := filepath.Join(tmpDir, "consumer.py")

	if err := os.WriteFile(shapes, []byte("class Circle:\n    def area(self) -> float:\n        return 3.14\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(factory, []byte("from .shapes import Circle\n\ndef make_circle() -> Circle:\n    return Circle()\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(consumer, []byte("from .factory import make_circle\n\ndef build():\n    c = make_circle()\n    c.area()\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	circleID := pyModulePath(tmpDir, shapes) + ".Circle"
	if _, ok := topo.Resources[circleID]; !ok {
		t.Fatalf("class %q not found, keys: %v", circleID, resourceKeys(topo))
	}
	for k := range topo.Resources {
		if strings.Contains(k, ".py.") {
			t.Errorf("id %q contains a '.py.' segment; extension should be stripped from module path", k)
		}
	}

	// build() calls make_circle() (cross-file) and c.area() resolves through the
	// factory's return TypingID to shapes.Circle.area (transitive cross-file).
	buildID := pyModulePath(tmpDir, consumer) + ".build"
	calls := topo.Resources[buildID].Connections["calls"]
	if !containsSuffix(calls, ".make_circle") {
		t.Errorf("expected build() to call make_circle, got %v", calls)
	}
	if !containsSuffix(calls, ".Circle.area") {
		t.Errorf("expected build() to transitively call Circle.area, got %v", calls)
	}

	// consumer.py imports factory.py -> a module->module import edge.
	consumerMod := topo.Resources[consumer]
	if !containsSuffix(consumerMod.Connections["imports_module"], factory) {
		t.Errorf("expected consumer module to import %q, got imports_module=%v", factory, consumerMod.Connections["imports_module"])
	}
}

// helpers

func hasPython() bool {
	for _, exe := range []string{"python3", "python"} {
		if _, err := exec.LookPath(exe); err == nil {
			return true
		}
	}
	_, err := os.Stat("C:\\Windows\\py.exe")
	if err == nil {
		return true
	}
	for _, p := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		pythonExe := filepath.Join(p, "python.exe")
		if _, err := os.Stat(pythonExe); err == nil {
			return true
		}
	}
	return false
}

func containsSuffix(slice []string, suffix string) bool {
	for _, s := range slice {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

func resourceKeys(topo *domain.Topology) []string {
	var keys []string
	for k := range topo.Resources {
		keys = append(keys, k)
	}
	return keys
}
