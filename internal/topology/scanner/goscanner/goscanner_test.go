package goscanner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
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

// helper to build a minimal GolangTopology for analyzeFunctionBody tests
func buildTestTopology(t *testing.T, modulePath, pkgPath string) *golang.GolangTopology {
	t.Helper()
	return &golang.GolangTopology{
		Root:         "",
		Functions:    make(map[golang.FunctionID]golang.GolangFunction),
		Structs:      make(map[golang.StructID]golang.GolangStruct),
		Interfaces:   make(map[golang.InterfaceID]golang.GolangInterface),
		NamedTypes:   make(map[golang.NamedTypeID]golang.GolangNamedType),
		ExternalVars: make(map[golang.ExternalVarID]golang.GolangExternalVar),
		Files:        make(map[golang.FileID]golang.GolangFile),
		Packages:     make(map[golang.PackagePath]golang.GolangPackage),
		Warnings:     make(map[string]domain.TopologyWarning),
		Errors:       make(map[string]string),
	}
}

// parseGoExpr parses a Go expression or small block and returns an ast.Node.
func parseGoExpr(t *testing.T, src string) ast.Node {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", "package p; func _() {\n"+src+"\n}", parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse error: %v\nsource:\n%s", err, src)
	}
	return f.Decls[0].(*ast.FuncDecl).Body
}

// TestAnalyzeFunctionBody_assignCompositeLitAndMethodCall
// Verifies: x := MyStruct{} followed by x.Method() produces ConnCalls + ConnUsesStruct
func TestAnalyzeFunctionBody_assignCompositeLitAndMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		x := MyStruct{}
		x.Method()
	`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	methodID := golang.FunctionID(string(pkgPath) + ".(MyStruct).Method")
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	calls := conns[golang.ConnCalls]
	usesStruct := conns[golang.ConnUsesStruct]

	if len(calls) != 1 || calls[0] != string(methodID) {
		t.Errorf("expected ConnCalls to %q, got %v", methodID, calls)
	}
	if len(usesStruct) != 1 || usesStruct[0] != string(structID) {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, usesStruct)
	}
}

// TestAnalyzeFunctionBody_assignConstructorAndMethodCall
// Verifies: x := NewMyStruct() followed by x.Method() produces ConnCalls (to constructor + method) + ConnUsesStruct
func TestAnalyzeFunctionBody_assignConstructorAndMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		x := NewMyStruct()
		x.Method()
	`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	ctorID := golang.FunctionID(string(pkgPath) + ".NewMyStruct")
	methodID := golang.FunctionID(string(pkgPath) + ".(MyStruct).Method")

	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[ctorID] = golang.GolangFunction{
		ID:   ctorID,
		Name: "NewMyStruct",
		Output: []golang.VariableDefinition{
			{Typing: "*MyStruct"},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	calls := conns[golang.ConnCalls]
	usesStruct := conns[golang.ConnUsesStruct]

	if len(calls) != 2 {
		t.Fatalf("expected 2 ConnCalls (ctor + method), got %v", calls)
	}
	if calls[0] != string(ctorID) && calls[1] != string(ctorID) {
		t.Errorf("expected ConnCalls to include %q, got %v", ctorID, calls)
	}
	if calls[0] != string(methodID) && calls[1] != string(methodID) {
		t.Errorf("expected ConnCalls to include %q, got %v", methodID, calls)
	}
	if len(usesStruct) != 1 || usesStruct[0] != string(structID) {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, usesStruct)
	}
}

// TestAnalyzeFunctionBody_assignRefCompositeLitAndMethodCall
// Verifies: x := &MyStruct{} followed by x.Method() produces ConnCalls + ConnUsesStruct
func TestAnalyzeFunctionBody_assignRefCompositeLitAndMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		x := &MyStruct{}
		x.Method()
	`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	methodID := golang.FunctionID(string(pkgPath) + ".(MyStruct).Method")
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	calls := conns[golang.ConnCalls]
	usesStruct := conns[golang.ConnUsesStruct]

	if len(calls) != 1 || calls[0] != string(methodID) {
		t.Errorf("expected ConnCalls to %q, got %v", methodID, calls)
	}
	if len(usesStruct) != 1 || usesStruct[0] != string(structID) {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, usesStruct)
	}
}

// TestAnalyzeFunctionBody_varDeclAndMethodCall
// Verifies: var x MyStruct; x.Method() produces ConnCalls + ConnUsesStruct
func TestAnalyzeFunctionBody_varDeclAndMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		var x MyStruct
		x.Method()
	`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	methodID := golang.FunctionID(string(pkgPath) + ".(MyStruct).Method")
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	calls := conns[golang.ConnCalls]
	usesStruct := conns[golang.ConnUsesStruct]

	if len(calls) != 1 || calls[0] != string(methodID) {
		t.Errorf("expected ConnCalls to %q, got %v", methodID, calls)
	}
	if len(usesStruct) != 1 || usesStruct[0] != string(structID) {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, usesStruct)
	}
}

// TestAnalyzeFunctionBody_qualifiedCompositeLitAndMethodCall
// Verifies: x := pkg.MyStruct{} + x.Method() resolves both ConnCalls + ConnUsesStruct for external pkg types
func TestAnalyzeFunctionBody_qualifiedCompositeLitAndMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		x := other.MyStruct{}
		x.Method()
	`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  map[string]string{"other": "example.com/other"},
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	otherPkg := golang.PackagePath("example.com/other")
	structID := golang.StructID(string(otherPkg) + ".MyStruct")
	methodID := golang.FunctionID(string(otherPkg) + ".(MyStruct).Method")
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}
	gt.Packages[otherPkg] = golang.GolangPackage{Path: otherPkg}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	calls := conns[golang.ConnCalls]
	usesStruct := conns[golang.ConnUsesStruct]

	if len(calls) != 1 || calls[0] != string(methodID) {
		t.Errorf("expected ConnCalls to %q, got %v", methodID, calls)
	}
	if len(usesStruct) != 1 || usesStruct[0] != string(structID) {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, usesStruct)
	}
}

// TestAnalyzeFunctionBody_paramMethodCall
// Verifies: func foo(m *MyStruct) { m.Method() } produces ConnCalls + ConnUsesStruct
func TestAnalyzeFunctionBody_paramMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `m.Method()`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	methodID := golang.FunctionID(string(pkgPath) + ".(MyStruct).Method")
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}

	funcInput := []golang.VariableDefinition{
		{Name: "m", Typing: "*MyStruct"},
	}
	conns := analyzeFunctionBody(body, pr, gt, funcInput, "", nil, "example.com/test.caller", nil)

	calls := conns[golang.ConnCalls]
	usesStruct := conns[golang.ConnUsesStruct]

	if len(calls) != 1 || calls[0] != string(methodID) {
		t.Errorf("expected ConnCalls to %q, got %v", methodID, calls)
	}
	if len(usesStruct) != 1 || usesStruct[0] != string(structID) {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, usesStruct)
	}
}

// TestAnalyzeFunctionBody_receiverMethodCall verifies existing receiver tracking still works
func TestAnalyzeFunctionBody_receiverMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `m.OtherMethod()`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	methodID := golang.FunctionID(string(pkgPath) + ".(MyStruct).OtherMethod")
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "OtherMethod",
	}

	receiverStruct := &structID
	conns := analyzeFunctionBody(body, pr, gt, nil, "m", receiverStruct, "example.com/test.caller", nil)

	calls := conns[golang.ConnCalls]
	usesStruct := conns[golang.ConnUsesStruct]

	if len(calls) != 1 || calls[0] != string(methodID) {
		t.Errorf("expected ConnCalls to %q, got %v", methodID, calls)
	}
	if len(usesStruct) != 1 || usesStruct[0] != string(structID) {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, usesStruct)
	}
}

// TestAnalyzeFunctionBody_interfaceParamMethodCall
// Verifies: func foo(iface SomeInterface) { iface.Method() } produces ConnUsesIface
func TestAnalyzeFunctionBody_interfaceParamMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `iface.Method()`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	ifaceID := golang.InterfaceID(string(pkgPath) + ".SomeInterface")
	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	methodID := golang.FunctionID(string(pkgPath) + ".(MyStruct).Method")

	gt.Interfaces[ifaceID] = golang.GolangInterface{
		ID:   ifaceID,
		Name: "SomeInterface",
		Methods: []golang.FunctionDefinition{
			{Name: "Method", Input: nil, Output: nil},
		},
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnImplBy: {string(structID)},
		},
	}
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}

	funcInput := []golang.VariableDefinition{
		{Name: "iface", Typing: "SomeInterface"},
	}
	conns := analyzeFunctionBody(body, pr, gt, funcInput, "", nil, "example.com/test.caller", nil)

	usesIface := conns[golang.ConnUsesIface]
	calls := conns[golang.ConnCalls]

	if len(usesIface) != 1 || usesIface[0] != string(ifaceID) {
		t.Errorf("expected ConnUsesIface to %q, got %v", ifaceID, usesIface)
	}
	// Body analysis is ImplementedBy-independent (in a cold scan it runs before
	// matchStructsToInterfaces), so an interface method call records only the
	// interface usage, never a fan-out call into each implementer's method.
	if len(calls) != 0 {
		t.Errorf("expected no ConnCalls (no implementer fan-out), got %v", calls)
	}
}

// TestAnalyzeFunctionBody_structImplementsInterfaceMethod
// Verifies: when a struct implements an interface, calling a method on the struct
// tracks both ConnUsesStruct and ConnUsesIface
func TestAnalyzeFunctionBody_structImplementsInterfaceMethod(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		x := MyStruct{}
		x.Method()
	`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	ifaceID := golang.InterfaceID(string(pkgPath) + ".SomeInterface")
	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	methodID := golang.FunctionID(string(pkgPath) + ".(MyStruct).Method")

	gt.Interfaces[ifaceID] = golang.GolangInterface{
		ID:   ifaceID,
		Name: "SomeInterface",
		Methods: []golang.FunctionDefinition{
			{Name: "Method", Input: nil, Output: nil},
		},
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnImplBy: {string(structID)},
		},
	}
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	usesIface := conns[golang.ConnUsesIface]
	usesStruct := conns[golang.ConnUsesStruct]
	calls := conns[golang.ConnCalls]

	if len(usesStruct) != 1 || usesStruct[0] != string(structID) {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, usesStruct)
	}
	// A concrete struct variable's method call resolves to the concrete method
	// only; body analysis no longer emits a ghost uses_interface edge for the
	// interface the struct happens to implement (a cold scan never does).
	if len(usesIface) != 0 {
		t.Errorf("expected no ConnUsesIface for a concrete call, got %v", usesIface)
	}
	if len(calls) != 1 || calls[0] != string(methodID) {
		t.Errorf("expected ConnCalls to %q, got %v", methodID, calls)
	}
}

// TestAnalyzeFunctionBody_qualifiedConstructorAndMethodCall
// Verifies: x := otherpkg.NewMyStruct() + x.Method() resolves across package boundaries
func TestAnalyzeFunctionBody_qualifiedConstructorAndMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		x := other.NewMyStruct()
		x.Method()
	`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  map[string]string{"other": "example.com/other"},
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	otherPkg := golang.PackagePath("example.com/other")
	structID := golang.StructID(string(otherPkg) + ".MyStruct")
	ctorID := golang.FunctionID(string(otherPkg) + ".NewMyStruct")
	methodID := golang.FunctionID(string(otherPkg) + ".(MyStruct).Method")

	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[ctorID] = golang.GolangFunction{
		ID:   ctorID,
		Name: "NewMyStruct",
		Output: []golang.VariableDefinition{
			{Typing: "*other.MyStruct"},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}
	gt.Packages[otherPkg] = golang.GolangPackage{Path: otherPkg}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	calls := conns[golang.ConnCalls]
	usesStruct := conns[golang.ConnUsesStruct]

	if len(calls) != 2 {
		t.Fatalf("expected 2 ConnCalls (ctor + method), got %v", calls)
	}
	if calls[0] != string(ctorID) && calls[1] != string(ctorID) {
		t.Errorf("expected ConnCalls to include %q, got %v", ctorID, calls)
	}
	if calls[0] != string(methodID) && calls[1] != string(methodID) {
		t.Errorf("expected ConnCalls to include %q, got %v", methodID, calls)
	}
	if len(usesStruct) != 1 || usesStruct[0] != string(structID) {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, usesStruct)
	}
}

// TestAnalyzeFunctionBody_interfaceAssignAndMethodCall
// Verifies: var iface SomeInterface; iface.Method() produces ConnUsesIface
func TestAnalyzeFunctionBody_interfaceAssignAndMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		var iface SomeInterface
		iface.Method()
	`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	ifaceID := golang.InterfaceID(string(pkgPath) + ".SomeInterface")
	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	methodID := golang.FunctionID(string(pkgPath) + ".(MyStruct).Method")

	gt.Interfaces[ifaceID] = golang.GolangInterface{
		ID:   ifaceID,
		Name: "SomeInterface",
		Methods: []golang.FunctionDefinition{
			{Name: "Method", Input: nil, Output: nil},
		},
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnImplBy: {string(structID)},
		},
	}
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	usesIface := conns[golang.ConnUsesIface]
	calls := conns[golang.ConnCalls]

	if len(usesIface) != 1 || usesIface[0] != string(ifaceID) {
		t.Errorf("expected ConnUsesIface to %q, got %v", ifaceID, usesIface)
	}
	// Interface usage is recorded; no implementer-method fan-out (cold scan has
	// no implemented_by edges at body-analysis time).
	if len(calls) != 0 {
		t.Errorf("expected no ConnCalls (no implementer fan-out), got %v", calls)
	}
}

// TestAnalyzeFunctionBody_noLocalVariableMethodCall ensures that a direct struct name used
// as a call target (e.g. "MyStruct()" as a constructor call) still works correctly
func TestAnalyzeFunctionBody_directStructCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `MyStruct()`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
		Functions:  nil,
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
	}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	usesStruct := conns[golang.ConnUsesStruct]
	if len(usesStruct) != 1 || usesStruct[0] != string(structID) {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, usesStruct)
	}
}

// TestAnalyzeFunctionBody_knownNameSkipsResolution
// Verifies that known names (like builtins, params, locals) skip resolution attempts
func TestAnalyzeFunctionBody_knownNameSkipsResolution(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		x := MyStruct{}
		x.SomeBuiltin()
	`
	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
	}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	// x is a known local, x.SomeBuiltin() should not produce any warnings or connections
	if _, ok := conns[golang.ConnCalls]; ok {
		t.Errorf("expected no ConnCalls for known local var, got %v", conns[golang.ConnCalls])
	}
}

// TestAnalyzeFunctionBody_endToEndWithFullScan validates the full pipeline:
// ParseFile -> analyzeFunctionBody for a real Go file with local var method calls
func TestAnalyzeFunctionBody_externalImportCallDoesNotWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap: map[string]string{
			"fmt":  "fmt",
			"http": "net/http",
		},
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))
	body := parseGoExpr(t, `
		fmt.Println("hello")
		http.ListenAndServe(":0", nil)
	`).(*ast.BlockStmt)

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(conns) != 0 {
		t.Fatalf("expected no topology connections for external imports, got %v", conns)
	}
	if len(gt.Warnings) != 0 {
		t.Fatalf("expected no warnings for external imports, got %v", gt.Warnings)
	}
}

func TestAnalyzeFunctionBody_builtinsDoNotWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{PkgPath: pkgPath, ModulePath: modulePath}
	gt := buildTestTopology(t, modulePath, string(pkgPath))
	body := parseGoExpr(t, `
		_ = make([]int, 0, 4)
		_ = len(x)
		_ = cap(x)
		_ = new(int)
		x = append(x, 1)
		copy(a, b)
		delete(m, "k")
		panic("boom")
		println("hello")
		close(ch)
	`).(*ast.BlockStmt)

	analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) != 0 {
		for _, w := range gt.Warnings {
			t.Logf("unexpected warning: [%s] %s", w.Kind, w.Message)
		}
		t.Fatalf("expected no warnings for builtins, got %d", len(gt.Warnings))
	}
}

func TestAnalyzeFunctionBody_knownLocalFunctionDoesNotWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		Functions: []FunctionParse{
			{
				Function: golang.GolangFunction{
					ID:   golang.FunctionID("example.com/test.helperFunc"),
					Name: "helperFunc",
				},
			},
		},
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))
	body := parseGoExpr(t, `helperFunc()`).(*ast.BlockStmt)

	analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) != 0 {
		for _, w := range gt.Warnings {
			t.Logf("unexpected warning: [%s] %s", w.Kind, w.Message)
		}
		t.Fatalf("expected no warnings for known local function, got %d", len(gt.Warnings))
	}
}

func TestAnalyzeFunctionBody_localVarCallDoesNotWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{PkgPath: pkgPath, ModulePath: modulePath}
	gt := buildTestTopology(t, modulePath, string(pkgPath))
	body := parseGoExpr(t, `
		f := someFunc
		f(42)
	`).(*ast.BlockStmt)

	analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) != 0 {
		for _, w := range gt.Warnings {
			t.Logf("unexpected warning: [%s] %s", w.Kind, w.Message)
		}
		t.Fatalf("expected no warnings for local var call, got %d", len(gt.Warnings))
	}
}

func TestAnalyzeFunctionBody_paramFunctionCallDoesNotWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{PkgPath: pkgPath, ModulePath: modulePath}
	gt := buildTestTopology(t, modulePath, string(pkgPath))
	body := parseGoExpr(t, `callback(42)`).(*ast.BlockStmt)

	funcInput := []golang.VariableDefinition{
		{Name: "callback", Typing: "func(int) error"},
	}

	analyzeFunctionBody(body, pr, gt, funcInput, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) != 0 {
		for _, w := range gt.Warnings {
			t.Logf("unexpected warning: [%s] %s", w.Kind, w.Message)
		}
		t.Fatalf("expected no warnings for param function call, got %d", len(gt.Warnings))
	}
}

func TestAnalyzeFunctionBody_paramStructMethodCallDoesNotWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{PkgPath: pkgPath, ModulePath: modulePath}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	structID := golang.StructID("example.com/test.MyStruct")
	methodID := golang.FunctionID("example.com/test.(MyStruct).DoSomething")
	gt.Structs[structID] = golang.GolangStruct{
		ID:          structID,
		Name:        "MyStruct",
		Connections: map[golang.ConnectionKind][]string{golang.ConnHasMethod: {string(methodID)}},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "DoSomething",
	}

	body := parseGoExpr(t, `s.DoSomething()`).(*ast.BlockStmt)
	funcInput := []golang.VariableDefinition{
		{Name: "s", Typing: "MyStruct"},
	}

	analyzeFunctionBody(body, pr, gt, funcInput, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) != 0 {
		for _, w := range gt.Warnings {
			t.Logf("unexpected warning: [%s] %s", w.Kind, w.Message)
		}
		t.Fatalf("expected no warnings for param struct method call, got %d", len(gt.Warnings))
	}
}

func TestAnalyzeFunctionBody_internalImportKnownFunctionDoesNotWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  map[string]string{"other": "example.com/test/other"},
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))
	gt.Functions["example.com/test/other.HelperFunc"] = golang.GolangFunction{
		ID:   "example.com/test/other.HelperFunc",
		Name: "HelperFunc",
	}
	body := parseGoExpr(t, `other.HelperFunc()`).(*ast.BlockStmt)

	analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) != 0 {
		for _, w := range gt.Warnings {
			t.Logf("unexpected warning: [%s] %s", w.Kind, w.Message)
		}
		t.Fatalf("expected no warnings for known internal import function, got %d", len(gt.Warnings))
	}
}

func TestAnalyzeFunctionBody_nonExistentFunctionDoesWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{PkgPath: pkgPath, ModulePath: modulePath}
	gt := buildTestTopology(t, modulePath, string(pkgPath))
	body := parseGoExpr(t, `NonExistent()`).(*ast.BlockStmt)

	analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) == 0 {
		t.Fatal("expected a warning for non-existent function")
	}
}

func TestAnalyzeFunctionBody_internalImportMissingFunctionDoesWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  map[string]string{"other": "example.com/test/other"},
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))
	body := parseGoExpr(t, `other.MissingFunc()`).(*ast.BlockStmt)

	analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) == 0 {
		t.Fatal("expected a warning for missing internal import function")
	}
}

func TestAnalyzeFunctionBody_endToEndWithFullScan(t *testing.T) {
	dir := t.TempDir()
	goModPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module example.com/test\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}

	code := `package test

type MyStruct struct {}
func (m MyStruct) Do() int { return 0 }

func caller() int {
	x := MyStruct{}
	return x.Do()
}
`
	filePath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")

	pr, err := ParseFile(filePath, pkgPath, modulePath, dir)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	// Build topology from parse results
	gt := buildTestTopology(t, modulePath, string(pkgPath))
	for _, s := range pr.Structs {
		gt.Structs[s.ID] = s
	}
	for _, fi := range pr.Functions {
		gt.Functions[fi.Function.ID] = fi.Function
	}

	populateStructMethods(gt)

	// Find caller function
	var fi *FunctionParse
	for i := range pr.Functions {
		if pr.Functions[i].Function.Name == "caller" {
			fi = &pr.Functions[i]
			break
		}
	}
	if fi == nil {
		t.Fatal("caller function not found in parse results")
	}

	conns := analyzeFunctionBody(fi.Body, pr, gt, fi.Function.Input, fi.ReceiverName, fi.Function.MethodFrom, fi.Function.ID, fi.TypeParamNames)

	structID := string(pkgPath) + ".MyStruct"
	methodID := string(pkgPath) + ".(MyStruct).Do"

	calls := conns[golang.ConnCalls]
	usesStruct := conns[golang.ConnUsesStruct]

	if len(calls) != 1 || calls[0] != methodID {
		t.Errorf("expected ConnCalls to %q, got %v", methodID, calls)
	}
	if len(usesStruct) != 1 || usesStruct[0] != structID {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, usesStruct)
	}
}

// TestAnalyzeFunctionBody_interfaceConstructorAndMethodCall
// Verifies: x := NewMyInterface(); x.Method() where NewMyInterface returns SomeInterface
// produces ConnUsesIface + ConnCalls (to the implementing struct's method)
func TestAnalyzeFunctionBody_interfaceConstructorAndMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		x := NewMyInterface()
		x.Method()
	`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	ifaceID := golang.InterfaceID(string(pkgPath) + ".SomeInterface")
	structID := golang.StructID(string(pkgPath) + ".MyStruct")
	ctorID := golang.FunctionID(string(pkgPath) + ".NewMyInterface")
	methodID := golang.FunctionID(string(pkgPath) + ".(MyStruct).Method")

	gt.Interfaces[ifaceID] = golang.GolangInterface{
		ID:   ifaceID,
		Name: "SomeInterface",
		Methods: []golang.FunctionDefinition{
			{Name: "Method", Input: nil, Output: nil},
		},
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnImplBy: {string(structID)},
		},
	}
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[ctorID] = golang.GolangFunction{
		ID:   ctorID,
		Name: "NewMyInterface",
		Output: []golang.VariableDefinition{
			{Typing: "SomeInterface"},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	usesIface := conns[golang.ConnUsesIface]
	calls := conns[golang.ConnCalls]

	if len(usesIface) != 1 || usesIface[0] != string(ifaceID) {
		t.Errorf("expected ConnUsesIface to %q, got %v", ifaceID, usesIface)
	}
	// x is interface-typed (the constructor returns SomeInterface), so the method
	// call records only interface usage; the constructor is the sole call edge
	// (no implementer fan-out — cold scan has no implemented_by here).
	if len(calls) != 1 || calls[0] != string(ctorID) {
		t.Errorf("expected only the constructor call %q, got %v", ctorID, calls)
	}
}

// TestAnalyzeFunctionBody_qualifiedInterfaceConstructorAndMethodCall
// Verifies: x := other.NewMyInterface(); x.Method() across package boundaries
// produces ConnUsesIface + ConnCalls to the implementing struct's method
func TestAnalyzeFunctionBody_qualifiedInterfaceConstructorAndMethodCall(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		x := other.NewMyInterface()
		x.Method()
	`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  map[string]string{"other": "example.com/other"},
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	otherPkg := golang.PackagePath("example.com/other")
	ifaceID := golang.InterfaceID(string(otherPkg) + ".SomeInterface")
	structID := golang.StructID(string(otherPkg) + ".MyStruct")
	ctorID := golang.FunctionID(string(otherPkg) + ".NewMyInterface")
	methodID := golang.FunctionID(string(otherPkg) + ".(MyStruct).Method")

	gt.Interfaces[ifaceID] = golang.GolangInterface{
		ID:   ifaceID,
		Name: "SomeInterface",
		Methods: []golang.FunctionDefinition{
			{Name: "Method", Input: nil, Output: nil},
		},
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnImplBy: {string(structID)},
		},
	}
	gt.Structs[structID] = golang.GolangStruct{
		ID:   structID,
		Name: "MyStruct",
		Connections: map[golang.ConnectionKind][]string{
			golang.ConnHasMethod: {string(methodID)},
		},
	}
	gt.Functions[ctorID] = golang.GolangFunction{
		ID:   ctorID,
		Name: "NewMyInterface",
		Output: []golang.VariableDefinition{
			{Typing: "*other.SomeInterface"},
		},
	}
	gt.Functions[methodID] = golang.GolangFunction{
		ID:   methodID,
		Name: "Method",
	}
	gt.Packages[otherPkg] = golang.GolangPackage{Path: otherPkg}

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	usesIface := conns[golang.ConnUsesIface]
	calls := conns[golang.ConnCalls]

	if len(usesIface) != 1 || usesIface[0] != string(ifaceID) {
		t.Errorf("expected ConnUsesIface to %q, got %v", ifaceID, usesIface)
	}
	// x is interface-typed across packages; the cross-package method call records
	// only interface usage, leaving the constructor as the sole call edge.
	if len(calls) != 1 || calls[0] != string(ctorID) {
		t.Errorf("expected only the constructor call %q, got %v", ctorID, calls)
	}
}

// TestAnalyzeFunctionBody_ignoresUnderscoreAssign
// Verifies: _ = MyStruct{} does not populate varTypeMap for _
func TestAnalyzeFunctionBody_ignoresUnderscoreAssign(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath("example.com/test")
	src := `
		_ = MyStruct{}
		_.Method()
	`

	body := parseGoExpr(t, src).(*ast.BlockStmt)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  make(map[string]string),
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	// _ should not be tracked, _.Method() should not produce any valid connections
	if _, ok := conns[golang.ConnCalls]; ok {
		t.Errorf("expected no ConnCalls for underscore var, got %v", conns[golang.ConnCalls])
	}
}

// TestParseFile_returnTypeTypingID verifies the parser records the canonical
// TypingID for a cross-package return type, resolved against the DEFINING file's
// import map (Phase 2: parse-time resolved type ids).
func TestParseFile_returnTypeTypingID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module mod\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pkg2"), 0755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "pkg2", "ext.go")
	if err := os.WriteFile(src, []byte(`package pkg2

import "mod/pkg1"

func ExtFunc() *pkg1.Stu { return nil }
`), 0644); err != nil {
		t.Fatal(err)
	}

	pr, err := ParseFile(src, golang.PackagePath("mod/pkg2"), "mod", dir)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	var ext *FunctionParse
	for i := range pr.Functions {
		if pr.Functions[i].Function.Name == "ExtFunc" {
			ext = &pr.Functions[i]
			break
		}
	}
	if ext == nil {
		t.Fatal("ExtFunc not parsed")
	}
	if len(ext.Function.Output) != 1 {
		t.Fatalf("expected 1 output, got %v", ext.Function.Output)
	}
	if got := ext.Function.Output[0].TypingID; got != "mod/pkg1.Stu" {
		t.Errorf("expected Output[0].TypingID = %q, got %q (Typing=%q)",
			"mod/pkg1.Stu", got, ext.Function.Output[0].Typing)
	}
}

// TestAnalyzeFunctionBody_transitiveCrossPackageReturnType is the user's
// scenario: pkg3 calls pkg2.ExtFunc() which returns *pkg1.Stu, then x.Method().
// pkg3 does NOT import pkg1, so resolving the return type in the caller's context
// fails — only the parse-time TypingID (recorded in pkg2's context) makes the
// transitive x.Method() -> pkg1.(Stu).Method edge resolvable. Exercises BOTH the
// parser (TypingID population) and resolver (TypingID preference) halves.
func TestAnalyzeFunctionBody_transitiveCrossPackageReturnType(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module mod\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("pkg1/stu.go", `package pkg1

type Stu struct{}

func (s *Stu) Method() {}
`)
	write("pkg2/ext.go", `package pkg2

import "mod/pkg1"

func ExtFunc() *pkg1.Stu { return nil }
`)
	write("pkg3/edit.go", `package pkg3

import "mod/pkg2"

func IWasEdited() {
	x := pkg2.ExtFunc()
	x.Method()
}
`)

	pr1, err := ParseFile(filepath.Join(dir, "pkg1", "stu.go"), golang.PackagePath("mod/pkg1"), "mod", dir)
	if err != nil {
		t.Fatalf("ParseFile pkg1: %v", err)
	}
	pr2, err := ParseFile(filepath.Join(dir, "pkg2", "ext.go"), golang.PackagePath("mod/pkg2"), "mod", dir)
	if err != nil {
		t.Fatalf("ParseFile pkg2: %v", err)
	}
	pr3, err := ParseFile(filepath.Join(dir, "pkg3", "edit.go"), golang.PackagePath("mod/pkg3"), "mod", dir)
	if err != nil {
		t.Fatalf("ParseFile pkg3: %v", err)
	}

	gt := buildTestTopology(t, "mod", "mod/pkg3")
	for _, pr := range []*ParseResult{pr1, pr2, pr3} {
		for _, s := range pr.Structs {
			gt.Structs[s.ID] = s
		}
		for _, fi := range pr.Functions {
			gt.Functions[fi.Function.ID] = fi.Function
		}
	}
	populateStructMethods(gt)

	var caller *FunctionParse
	for i := range pr3.Functions {
		if pr3.Functions[i].Function.Name == "IWasEdited" {
			caller = &pr3.Functions[i]
			break
		}
	}
	if caller == nil {
		t.Fatal("IWasEdited not found")
	}

	conns := analyzeFunctionBody(caller.Body, pr3, gt, caller.Function.Input, caller.ReceiverName, caller.Function.MethodFrom, caller.Function.ID, caller.TypeParamNames)

	methodID := "mod/pkg1.(Stu).Method"
	found := false
	for _, c := range conns[golang.ConnCalls] {
		if c == methodID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ConnCalls to include %q (transitive cross-package method), got %v",
			methodID, conns[golang.ConnCalls])
	}
}

// --- package-level func-typed vars are call targets, not missing nodes -------
//
// Regression cover for the defect that produced 83 false `use_missing_node`
// warnings on the cli/cli fixture: every one of them was a call through a
// package-level variable of function type (var Yellow = makeColorFunc(...),
// var PrepareCmd = func(*exec.Cmd) Runnable). The body analyzer checked
// Functions/NamedTypes/Structs but never ExternalVars, so a node that was
// sitting in the database was reported as nonexistent.

// TestAnalyzeFunctionBody_packageFuncVarCallDoesNotWarn
// Verifies: Format("x") where Format is a same-package func-typed var resolves
// to ConnUsesExtVar and produces no warning.
func TestAnalyzeFunctionBody_packageFuncVarCallDoesNotWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{PkgPath: pkgPath, ModulePath: modulePath}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	varID := golang.ExternalVarID(string(pkgPath) + ".Format")
	gt.ExternalVars[varID] = golang.GolangExternalVar{
		ID: varID, Name: "Format", Typing: "func(string) string",
	}

	body := parseGoExpr(t, `Format("x")`).(*ast.BlockStmt)
	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) != 0 {
		for _, w := range gt.Warnings {
			t.Logf("unexpected warning: [%s] %s", w.Kind, w.Message)
		}
		t.Fatalf("expected no warnings for a package-level func var call, got %d", len(gt.Warnings))
	}
	if got := conns[golang.ConnUsesExtVar]; len(got) != 1 || got[0] != string(varID) {
		t.Errorf("expected ConnUsesExtVar to %q, got %v", varID, got)
	}
}

// TestAnalyzeFunctionBody_qualifiedFuncVarCallDoesNotWarn
// Verifies the cli/cli shape: run.PrepareCmd(cmd) where PrepareCmd is a
// func-typed var in an internal package. Also pins that NO uses_package edge is
// emitted — resolveUseMissingWarning's extvar case deliberately skips
// addCrossPkg(), and the cold and incremental paths must agree byte for byte or
// the at-scale three-mode equality suite fails.
func TestAnalyzeFunctionBody_qualifiedFuncVarCallDoesNotWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  map[string]string{"run": "example.com/test/internal/run"},
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	varID := golang.ExternalVarID("example.com/test/internal/run.PrepareCmd")
	gt.ExternalVars[varID] = golang.GolangExternalVar{
		ID: varID, Name: "PrepareCmd", Typing: "func(*exec.Cmd) Runnable",
	}

	body := parseGoExpr(t, `run.PrepareCmd(cmd)`).(*ast.BlockStmt)
	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) != 0 {
		for _, w := range gt.Warnings {
			t.Logf("unexpected warning: [%s] %s", w.Kind, w.Message)
		}
		t.Fatalf("expected no warnings for a qualified func var call, got %d", len(gt.Warnings))
	}
	if got := conns[golang.ConnUsesExtVar]; len(got) != 1 || got[0] != string(varID) {
		t.Errorf("expected ConnUsesExtVar to %q, got %v", varID, got)
	}
	if got := conns[golang.ConnUsesPkg]; len(got) != 0 {
		t.Errorf("extvar resolution must not emit uses_package (mirrors resolveUseMissingWarning), got %v", got)
	}
}

// TestAnalyzeFunctionBody_qualifiedStructConversionDoesNotWarn
// Verifies: pkg.T(x) is a conversion, not a missing function. resolveCompositeLit
// already resolves the pkg.T{...} form; the call form must match it.
func TestAnalyzeFunctionBody_qualifiedStructConversionDoesNotWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  map[string]string{"other": "example.com/test/other"},
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	structID := golang.StructID("example.com/test/other.Point")
	gt.Structs[structID] = golang.GolangStruct{ID: structID, Name: "Point"}

	body := parseGoExpr(t, `other.Point(v)`).(*ast.BlockStmt)
	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) != 0 {
		for _, w := range gt.Warnings {
			t.Logf("unexpected warning: [%s] %s", w.Kind, w.Message)
		}
		t.Fatalf("expected no warnings for a qualified struct conversion, got %d", len(gt.Warnings))
	}
	if got := conns[golang.ConnUsesStruct]; len(got) != 1 || got[0] != string(structID) {
		t.Errorf("expected ConnUsesStruct to %q, got %v", structID, got)
	}
	if got := conns[golang.ConnUsesPkg]; len(got) != 1 || got[0] != "example.com/test/other" {
		t.Errorf("expected uses_package to the target package, got %v", got)
	}
}

// TestAnalyzeFunctionBody_qualifiedInterfaceConversionDoesNotWarn
// Verifies the interface arm of the same conversion form, mirroring
// resolveUseMissingWarning's existsInInterfaces case.
func TestAnalyzeFunctionBody_qualifiedInterfaceConversionDoesNotWarn(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{
		PkgPath:    pkgPath,
		ModulePath: modulePath,
		ImportMap:  map[string]string{"other": "example.com/test/other"},
	}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	ifaceID := golang.InterfaceID("example.com/test/other.Shape")
	gt.Interfaces[ifaceID] = golang.GolangInterface{ID: ifaceID, Name: "Shape"}

	body := parseGoExpr(t, `other.Shape(v)`).(*ast.BlockStmt)
	conns := analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) != 0 {
		t.Fatalf("expected no warnings for a qualified interface conversion, got %d", len(gt.Warnings))
	}
	if got := conns[golang.ConnUsesIface]; len(got) != 1 || got[0] != string(ifaceID) {
		t.Errorf("expected ConnUsesIface to %q, got %v", ifaceID, got)
	}
}

// TestUseMissingWarningUsesQualifiedTargetInMessage
// The same-package branch put the fully-qualified id in TargetID but the bare
// name in the message, unlike the qualified branch. An agent reading
// `warnings list` saw "calls GitCommand" and could not tell which package.
func TestUseMissingWarningUsesQualifiedTargetInMessage(t *testing.T) {
	modulePath := "example.com/test"
	pkgPath := golang.PackagePath(modulePath)
	pr := &ParseResult{PkgPath: pkgPath, ModulePath: modulePath}
	gt := buildTestTopology(t, modulePath, string(pkgPath))

	body := parseGoExpr(t, `NonExistent()`).(*ast.BlockStmt)
	analyzeFunctionBody(body, pr, gt, nil, "", nil, "example.com/test.caller", nil)

	if len(gt.Warnings) != 1 {
		t.Fatalf("expected exactly one warning, got %d", len(gt.Warnings))
	}
	want := "example.com/test.NonExistent"
	for _, w := range gt.Warnings {
		if w.TargetID != want {
			t.Errorf("TargetID = %q, want %q", w.TargetID, want)
		}
		if !strings.Contains(w.Message, want) {
			t.Errorf("message should name the qualified target %q, got %q", want, w.Message)
		}
	}
}

// TestFullScan_CrossPackageFuncVarProducesNoUseMissingWarning
// The cold-path counterpart of the unit tests above, and the test that would
// have caught this in production: a real two-package module scanned end to end
// must produce a uses_extvar edge and zero use_missing_node warnings.
func TestFullScan_CrossPackageFuncVarProducesNoUseMissingWarning(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("go.mod", "module example.com/test\n\ngo 1.22\n")
	write("hooks/hooks.go", `package hooks

// Format is a package-level func-typed var: the cli/cli "var Yellow =
// makeColorFunc(...)" shape.
var Format = func(s string) string { return s }
`)
	write("consumer/consumer.go", `package consumer

import "example.com/test/hooks"

func Report() string {
	return hooks.Format("x")
}
`)

	topo, err := NewGoScanner().Scan(dir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	for _, w := range topo.Warnings {
		if w.Kind == domain.WarnUseMissingNode {
			t.Errorf("unexpected use_missing_node warning: %s", w.Message)
		}
	}

	report, ok := topo.Resources["example.com/test/consumer.Report"]
	if !ok {
		t.Fatalf("consumer.Report not in topology; have %d resources", len(topo.Resources))
	}
	want := "example.com/test/hooks.Format"
	found := false
	for _, target := range report.Connections[string(golang.ConnUsesExtVar)] {
		if target == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected consumer.Report to have uses_extvar -> %q, got %v", want, report.Connections)
	}
	if got := report.Connections[string(golang.ConnUsesPkg)]; len(got) != 0 {
		t.Errorf("extvar resolution must not emit uses_package, got %v", got)
	}
}
