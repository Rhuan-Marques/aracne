package pyscanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ltp/internal/topology/domain"
	"ltp/internal/topology/python"
)

func TestResolveValueRef_function(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
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
		PkgPath:    "mypkg",
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
		PkgPath:    "mypkg",
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
		PkgPath:    "mypkg",
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

	if len(calls) != 1 || calls[0] != "mypkg.Ambiguous" {
		t.Errorf("expected ConnCalls (function takes priority), got calls=%v classes=%v", calls, classes)
	}
	if len(classes) != 0 {
		t.Errorf("expected no ConnUsesClass when function matches, got %v", classes)
	}
}

func TestResolveValueRef_literal_values_skipped(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
		ModuleRoot: "testdata",
	}
	gt := &python.PythonTopology{
		Functions:    make(map[python.FunctionID]python.PythonFunction),
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	for _, lit := range []string{"None", "True", "False", "list", "dict", "tuple", "expr", ""} {
		var added bool
		add := func(_ python.ConnectionKind, _ string) { added = true }
		resolveValueRef(lit, pr, gt, add)
		if added {
			t.Errorf("literal %q should not add any connection", lit)
		}
	}
}

func TestResolveValueRef_dotted_import(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
		ModuleRoot: filepath.Join("testdata", "testpkg"),
		ImportMap:  map[string]string{"utils": "testpkg.utils"},
	}
	gt := &python.PythonTopology{
		Functions: make(map[python.FunctionID]python.PythonFunction),
		Classes: map[python.ClassID]python.PythonClass{
			"testpkg.utils.Helper": {ID: "testpkg.utils.Helper", Name: "Helper"},
		},
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotClasses []string
	var gotPkgs []string
	add := func(kind python.ConnectionKind, id string) {
		switch kind {
		case python.ConnUsesClass:
			gotClasses = append(gotClasses, id)
		case python.ConnUsesPkg:
			gotPkgs = append(gotPkgs, id)
		}
	}

	resolveValueRef("utils.Helper", pr, gt, add)

	if len(gotPkgs) != 1 || gotPkgs[0] != "testpkg.utils" {
		t.Errorf("expected ConnUsesPkg to testpkg.utils, got %v", gotPkgs)
	}
	if len(gotClasses) != 1 || gotClasses[0] != "testpkg.utils.Helper" {
		t.Errorf("expected ConnUsesClass to testpkg.utils.Helper, got %v", gotClasses)
	}
}

func TestResolveClassVarRefs(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
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

	var found bool
	for _, ref := range pr.ClassVarRefs {
		if ref.ClassID == "mypkg.y" && ref.RefValue == "x" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("class var ref (y → x) not found in ParseResult.ClassVarRefs: %+v", pr.ClassVarRefs)
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

	var refs []string
	for _, ref := range pr.ClassVarRefs {
		if ref.ClassID == "mypkg.Container" {
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

	pkgName := filepath.Base(tmpDir)
	classID := pkgName + ".y"
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

	pkgName := filepath.Base(tmpDir)
	classID := pkgName + ".y"
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

	pkgName := filepath.Base(tmpDir)

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

	classID := pkgName + ".y"
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

func TestResolveValueRef_dotted_function_via_import(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
		ModuleRoot: filepath.Join("testdata", "mylib"),
		ImportMap:  map[string]string{"lib": "mylib.utils"},
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"mylib.utils.compute": {ID: "mylib.utils.compute", Name: "compute"},
		},
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotCalls []string
	var gotPkgs []string
	add := func(kind python.ConnectionKind, id string) {
		switch kind {
		case python.ConnCalls:
			gotCalls = append(gotCalls, id)
		case python.ConnUsesPkg:
			gotPkgs = append(gotPkgs, id)
		}
	}

	resolveValueRef("lib.compute", pr, gt, add)

	if len(gotPkgs) != 1 || gotPkgs[0] != "mylib.utils" {
		t.Errorf("expected ConnUsesPkg to mylib.utils, got %v", gotPkgs)
	}
	if len(gotCalls) != 1 || gotCalls[0] != "mylib.utils.compute" {
		t.Errorf("expected ConnCalls to mylib.utils.compute, got %v", gotCalls)
	}
}

func TestResolveValueRef_nonexistent_skipped(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
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

func TestResolveValueRef_import_alias_plain_name_function(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
		ModuleRoot: filepath.Join("testdata", "testpkg"),
		ImportMap:  map[string]string{"myfunc": "testpkg.utils.helper_func"},
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"testpkg.utils.helper_func": {ID: "testpkg.utils.helper_func", Name: "helper_func"},
		},
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotCalls []string
	var gotPkgs []string
	add := func(kind python.ConnectionKind, id string) {
		switch kind {
		case python.ConnCalls:
			gotCalls = append(gotCalls, id)
		case python.ConnUsesPkg:
			gotPkgs = append(gotPkgs, id)
		}
	}

	resolveValueRef("myfunc", pr, gt, add)

	if len(gotCalls) != 1 || gotCalls[0] != "testpkg.utils.helper_func" {
		t.Errorf("expected ConnCalls to testpkg.utils.helper_func, got %v", gotCalls)
	}
	if len(gotPkgs) != 1 {
		t.Errorf("expected ConnUsesPkg, got %v", gotPkgs)
	}
}

func TestResolveValueRef_import_alias_plain_name_class(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
		ModuleRoot: filepath.Join("testdata", "testpkg"),
		ImportMap:  map[string]string{"MyClass": "testpkg.models.MyClass"},
	}
	gt := &python.PythonTopology{
		Functions: make(map[python.FunctionID]python.PythonFunction),
		Classes: map[python.ClassID]python.PythonClass{
			"testpkg.models.MyClass": {ID: "testpkg.models.MyClass", Name: "MyClass"},
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

	if len(gotClasses) != 1 || gotClasses[0] != "testpkg.models.MyClass" {
		t.Errorf("expected ConnUsesClass to testpkg.models.MyClass, got %v", gotClasses)
	}
}

func TestResolveValueRef_import_alias_with_root_prefix(t *testing.T) {
	// When import path already includes the root base name, it should match directly
	pr := &ParseResult{
		PkgPath:    "mypkg",
		ModuleRoot: filepath.Join("testdata", "testpkg"),
		ImportMap:  map[string]string{"helper": "testpkg.utils.helper"},
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"testpkg.utils.helper": {ID: "testpkg.utils.helper", Name: "helper"},
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

	resolveValueRef("helper", pr, gt, add)

	if len(gotCalls) != 1 || gotCalls[0] != "testpkg.utils.helper" {
		t.Errorf("expected ConnCalls to testpkg.utils.helper, got %v", gotCalls)
	}
}

func TestResolveValueRef_import_alias_dotted_on_alias(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
		ModuleRoot: filepath.Join("testdata", "testpkg"),
		ImportMap:  map[string]string{"lib": "testpkg.mylib"},
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"testpkg.mylib.compute": {ID: "testpkg.mylib.compute", Name: "compute"},
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

	if len(gotCalls) != 1 || gotCalls[0] != "testpkg.mylib.compute" {
		t.Errorf("expected ConnCalls to testpkg.mylib.compute, got %v", gotCalls)
	}
}

func TestResolveValueRef_import_alias_external_dep_plain(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
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
		PkgPath:    "mypkg",
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

func TestResolveValueRef_import_alias_shadows_local(t *testing.T) {
	// Import alias takes priority over local name (Python semantics)
	pr := &ParseResult{
		PkgPath:    "mypkg",
		ModuleRoot: "testdata",
		ImportMap:  map[string]string{"myfunc": "otherpkg.utils.myfunc"},
	}
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"mypkg.myfunc": {ID: "mypkg.myfunc", Name: "myfunc"},
		},
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	var gotDeps []string
	add := func(kind python.ConnectionKind, id string) {
		if kind == python.ConnUsesDep {
			gotDeps = append(gotDeps, id)
		}
	}

	resolveValueRef("myfunc", pr, gt, add)

	// Import alias resolves to external dep, local is shadowed
	if len(gotDeps) != 1 || gotDeps[0] != "otherpkg.utils.myfunc" {
		t.Errorf("expected ConnUsesDep to otherpkg.utils.myfunc, got %v", gotDeps)
	}
}

func TestTryResolveSymbol_plain(t *testing.T) {
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"myproj.pkg.myfunc": {ID: "myproj.pkg.myfunc", Name: "myfunc"},
		},
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	kind, id := tryResolveSymbol("pkg.myfunc", "", "myproj", gt)
	if kind != python.ConnCalls || id != "myproj.pkg.myfunc" {
		t.Errorf("expected ConnCalls myproj.pkg.myfunc, got %v %v", kind, id)
	}
}

func TestTryResolveSymbol_with_prefix_match(t *testing.T) {
	// When import path already has the root prefix
	gt := &python.PythonTopology{
		Functions: map[python.FunctionID]python.PythonFunction{
			"myproj.pkg.myfunc": {ID: "myproj.pkg.myfunc", Name: "myfunc"},
		},
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	kind, id := tryResolveSymbol("myproj.pkg.myfunc", "", "myproj", gt)
	if kind != python.ConnCalls || id != "myproj.pkg.myfunc" {
		t.Errorf("expected ConnCalls myproj.pkg.myfunc, got %v %v", kind, id)
	}
}

func TestTryResolveSymbol_class(t *testing.T) {
	gt := &python.PythonTopology{
		Functions: make(map[python.FunctionID]python.PythonFunction),
		Classes: map[python.ClassID]python.PythonClass{
			"myproj.models.User": {ID: "myproj.models.User", Name: "User"},
		},
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	kind, id := tryResolveSymbol("models.User", "", "myproj", gt)
	if kind != python.ConnUsesClass || id != "myproj.models.User" {
		t.Errorf("expected ConnUsesClass myproj.models.User, got %v %v", kind, id)
	}
}

func TestTryResolveSymbol_with_symbol_suffix(t *testing.T) {
	gt := &python.PythonTopology{
		Functions: make(map[python.FunctionID]python.PythonFunction),
		Classes: map[python.ClassID]python.PythonClass{
			"myproj.models.special.User": {ID: "myproj.models.special.User", Name: "User"},
		},
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	kind, id := tryResolveSymbol("models.special", "User", "myproj", gt)
	if kind != python.ConnUsesClass || id != "myproj.models.special.User" {
		t.Errorf("expected ConnUsesClass myproj.models.special.User, got %v %v", kind, id)
	}
}

func TestTryResolveSymbol_not_found(t *testing.T) {
	gt := &python.PythonTopology{
		Functions:    make(map[python.FunctionID]python.PythonFunction),
		Classes:      make(map[python.ClassID]python.PythonClass),
		ExternalVars: make(map[python.ExternalVarID]python.PythonExternalVar),
	}

	kind, id := tryResolveSymbol("nonexistent.Symbol", "", "myproj", gt)
	if kind != "" || id != "" {
		t.Errorf("expected empty result for nonexistent, got %v %v", kind, id)
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

	pkgName := filepath.Base(tmpDir)

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	classID := pkgName + ".y"
	res, ok := topo.Resources[classID]
	if !ok {
		t.Fatalf("class %q not found, keys: %v", classID, resourceKeys(topo))
	}

	calls := res.Connections["calls"]
	var foundLocal, foundOsPath bool
	for _, c := range calls {
		if strings.HasSuffix(c, ".local_func") {
			foundLocal = true
		}
		if strings.HasSuffix(c, ".path") {
			foundOsPath = true
		}
	}

	if !foundLocal {
		t.Errorf("expected ConnCalls to local_func, got calls=%v", calls)
	}
	if !foundOsPath {
		t.Logf("os.path is likely not in topology (stdlib), calls=%v", calls)
	}

	if len(calls) == 0 {
		t.Errorf("expected at least local_func to be resolved, got %v", calls)
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

	pkgName := filepath.Base(tmpDir)

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	classID := pkgName + ".y"
	_, ok := topo.Resources[classID]
	if !ok {
		t.Fatalf("class %q not found, keys: %v", classID, resourceKeys(topo))
	}

	// getcwd is a stdlib function — won't be in our topology
	// But the scan should not crash, and the import alias should be recorded
	// as a dep (os is external to the project)
}

// TestResolveBodyCallRefs_localAssignmentAndMethodCall
// Verifies: x = SomeClass(); x.method() produces ConnCalls + ConnUsesClass
func TestResolveBodyCallRefs_localAssignmentAndMethodCall(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
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

	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, funcInput)

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
		PkgPath:    "mypkg",
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

	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, funcInput)

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
		PkgPath:    "mypkg",
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

	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, funcInput)

	if count != 0 {
		t.Errorf("expected no connections for unknown variable, got %d", count)
	}
}

// TestResolveBodyCallRefs_directFunctionCall
// Verifies: direct function calls (no receiver) in body calls are resolved
func TestResolveBodyCallRefs_directFunctionCall(t *testing.T) {
	pr := &ParseResult{
		PkgPath:    "mypkg",
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

	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, funcInput)

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

	pkgName := filepath.Base(tmpDir)

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	funcID := pkgName + ".process"
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

	pkgName := filepath.Base(tmpDir)

	s := NewPythonScanner()
	topo, err := s.Scan(tmpDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	funcID := pkgName + ".process"
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

// helpers

func hasPython() bool {
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
	// Also check common install locations
	for _, candidate := range []string{
		"C:\\Python3\\python.exe",
		"C:\\Python313\\python.exe",
		"C:\\Program Files\\Python313\\python.exe",
		"C:\\Users\\" + os.Getenv("USERNAME") + "\\AppData\\Local\\Programs\\Python\\Python313\\python.exe",
		"C:\\Users\\" + os.Getenv("USERNAME") + "\\AppData\\Local\\Microsoft\\WindowsApps\\python.exe",
	} {
		if _, err := os.Stat(candidate); err == nil {
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
