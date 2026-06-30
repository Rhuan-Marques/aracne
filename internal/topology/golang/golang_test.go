package golang

import (
	"testing"

	"aracne/internal/topology/domain"
)

func TestConnectionKindValues(t *testing.T) {
	tests := []struct {
		kind ConnectionKind
		want string
	}{
		{ConnCalls, "calls"},
		{ConnUsesStruct, "uses_struct"},
		{ConnUsesNamedType, "uses_named_type"},
		{ConnUsesIface, "uses_interface"},
		{ConnUsesExtVar, "uses_extvar"},
		{ConnUsesPkg, "uses_package"},
		{ConnUsesDep, "uses_dependency"},
		{ConnHasMethod, "methods"},
		{ConnImplements, "implements"},
		{ConnImplBy, "implemented_by"},
		{ConnConstructor, "constructor"},
		{ConnHasFunc, "has_function"},
		{ConnHasStruct, "has_struct"},
		{ConnHasNamedType, "has_named_type"},
		{ConnHasIface, "has_interface"},
		{ConnHasVar, "has_extvar"},
		{ConnHasFile, "has_file"},
		{ConnImportsPkg, "imports_package"},
		{ConnImportsDep, "imports_dependency"},
	}
	for _, tt := range tests {
		if string(tt.kind) != tt.want {
			t.Errorf("ConnectionKind(%s) = %q, want %q", tt.want, string(tt.kind), tt.want)
		}
	}
}

func TestGolangFunctionAccessors(t *testing.T) {
	fn := GolangFunction{
		ID:   "pkg.Func",
		Name: "Func",
		Connections: map[ConnectionKind][]string{
			ConnCalls:         {"pkg.Bar", "pkg.Baz"},
			ConnUsesStruct:    {"pkg.MyStruct"},
			ConnUsesNamedType: {"pkg.MyType"},
			ConnUsesIface:     {"pkg.MyInterface"},
			ConnUsesExtVar:    {"pkg.GlobalVar"},
			ConnUsesPkg:       {"fmt"},
			ConnUsesDep:       {"github.com/foo/bar"},
		},
	}

	if calls := fn.Calls(); len(calls) != 2 || calls[0] != "pkg.Bar" {
		t.Errorf("Calls() = %v, want [pkg.Bar pkg.Baz]", calls)
	}
	if uses := fn.UsesStruct(); len(uses) != 1 || uses[0] != "pkg.MyStruct" {
		t.Errorf("UsesStruct() = %v", uses)
	}
	if nt := fn.UsesNamedType(); len(nt) != 1 || nt[0] != "pkg.MyType" {
		t.Errorf("UsesNamedType() = %v", nt)
	}
	if ifaces := fn.UsesInterface(); len(ifaces) != 1 || ifaces[0] != "pkg.MyInterface" {
		t.Errorf("UsesInterface() = %v", ifaces)
	}
	if vars := fn.UsesExtVar(); len(vars) != 1 || vars[0] != "pkg.GlobalVar" {
		t.Errorf("UsesExtVar() = %v", vars)
	}
	if pkgs := fn.UsesPkg(); len(pkgs) != 1 || pkgs[0] != "fmt" {
		t.Errorf("UsesPkg() = %v", pkgs)
	}
	if deps := fn.UsesDep(); len(deps) != 1 || deps[0] != "github.com/foo/bar" {
		t.Errorf("UsesDep() = %v", deps)
	}
}

func TestGolangFunctionAccessorsEmpty(t *testing.T) {
	fn := GolangFunction{Connections: make(map[ConnectionKind][]string)}
	if calls := fn.Calls(); len(calls) != 0 {
		t.Errorf("expected empty Calls, got %v", calls)
	}
	if uses := fn.UsesStruct(); len(uses) != 0 {
		t.Errorf("expected empty UsesStruct")
	}
}

func TestGolangFunctionAccessorsNil(t *testing.T) {
	var fn GolangFunction
	calls := fn.Calls()
	if len(calls) != 0 {
		t.Errorf("expected empty Calls, got %v", calls)
	}
}

func TestGolangStructAccessors(t *testing.T) {
	s := GolangStruct{
		ID:   "pkg.MyStruct",
		Name: "MyStruct",
		Connections: map[ConnectionKind][]string{
			ConnHasMethod:  {"pkg.Method1", "pkg.Method2"},
			ConnImplements: {"pkg.DoSomething"},
			ConnUsesPkg:    {"fmt"},
			ConnUsesDep:    {"github.com/foo/bar"},
		},
	}

	if methods := s.Methods(); len(methods) != 2 {
		t.Errorf("Methods() = %v, want 2 methods", methods)
	}
	if impls := s.Implements(); len(impls) != 1 || impls[0] != "pkg.DoSomething" {
		t.Errorf("Implements() = %v", impls)
	}
	if pkgs := s.UsesPkg(); len(pkgs) != 1 || pkgs[0] != "fmt" {
		t.Errorf("UsesPkg() = %v", pkgs)
	}
	if deps := s.UsesDep(); len(deps) != 1 || deps[0] != "github.com/foo/bar" {
		t.Errorf("UsesDep() = %v", deps)
	}
}

func TestGolangInterfaceAccessors(t *testing.T) {
	iface := GolangInterface{
		ID:   "pkg.DoSomething",
		Name: "DoSomething",
		Connections: map[ConnectionKind][]string{
			ConnImplBy: {"pkg.Struct1", "pkg.Struct2"},
		},
	}

	if impls := iface.ImplementedBy(); len(impls) != 2 {
		t.Errorf("ImplementedBy() = %v, want 2", impls)
	}
}

func TestGolangFileAccessors(t *testing.T) {
	f := GolangFile{
		ID:   "main.go",
		Name: "main.go",
		Connections: map[ConnectionKind][]string{
			ConnHasFunc:      {"pkg.Func1", "pkg.Func2"},
			ConnHasStruct:    {"pkg.MyStruct"},
			ConnHasNamedType: {"pkg.MyType"},
			ConnHasIface:     {"pkg.MyIface"},
			ConnHasVar:       {"pkg.Global"},
			ConnImportsPkg:   {"fmt", "os"},
			ConnImportsDep:   {"github.com/foo/bar"},
		},
	}

	if funcs := f.Functions(); len(funcs) != 2 {
		t.Errorf("Functions() = %v, want 2", funcs)
	}
	if structs := f.Structs(); len(structs) != 1 {
		t.Errorf("Structs() = %v", structs)
	}
	if nts := f.NamedTypes(); len(nts) != 1 {
		t.Errorf("NamedTypes() = %v", nts)
	}
	if ifaces := f.Interfaces(); len(ifaces) != 1 {
		t.Errorf("Interfaces() = %v", ifaces)
	}
	if vars := f.ExternalVars(); len(vars) != 1 {
		t.Errorf("ExternalVars() = %v", vars)
	}
	if pkgs := f.PackagesImported(); len(pkgs) != 2 {
		t.Errorf("PackagesImported() = %v", pkgs)
	}
	if deps := f.DependenciesImported(); len(deps) != 1 {
		t.Errorf("DependenciesImported() = %v", deps)
	}
}

func TestGolangPackageAccessors(t *testing.T) {
	p := GolangPackage{
		Path: "mypackage",
		Connections: map[ConnectionKind][]string{
			ConnHasFile:      {"file1.go", "file2.go"},
			ConnHasFunc:      {"pkg.Func1"},
			ConnHasStruct:    {"pkg.MyStruct"},
			ConnHasNamedType: {"pkg.MyType"},
			ConnHasIface:     {"pkg.MyIface"},
			ConnHasVar:       {"pkg.Global"},
		},
	}

	if files := p.Files(); len(files) != 2 {
		t.Errorf("Files() = %v", files)
	}
	if funcs := p.HasFunctions(); len(funcs) != 1 {
		t.Errorf("HasFunctions() = %v", funcs)
	}
	if structs := p.HasStructs(); len(structs) != 1 {
		t.Errorf("HasStructs() = %v", structs)
	}
	if nts := p.HasNamedTypes(); len(nts) != 1 {
		t.Errorf("HasNamedTypes() = %v", nts)
	}
	if ifaces := p.HasInterfaces(); len(ifaces) != 1 {
		t.Errorf("HasInterfaces() = %v", ifaces)
	}
	if vars := p.HasExternalVars(); len(vars) != 1 {
		t.Errorf("HasExternalVars() = %v", vars)
	}
}

func TestFromGenericNil(t *testing.T) {
	result := FromGeneric(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestToGenericNil(t *testing.T) {
	result := ToGeneric(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestGoManagerNilCheck(t *testing.T) {
	mgr := NewGoManager(nil)
	if mgr == nil {
		t.Fatal("NewGoManager returned nil")
	}
	if mgr.Generic() != nil {
		t.Errorf("expected nil generic manager")
	}
}

func TestVariableDefinition(t *testing.T) {
	v := VariableDefinition{Name: "x", Typing: "int"}
	if v.Name != "x" || v.Typing != "int" {
		t.Errorf("unexpected VariableDefinition values")
	}
}

func TestDependency(t *testing.T) {
	d := Dependancy{PackagePath: "github.com/foo/bar"}
	if string(d.PackagePath) != "github.com/foo/bar" {
		t.Errorf("unexpected PackagePath")
	}
}

func TestGoStructContextDefaults(t *testing.T) {
	ctx := &GoStructContext{}
	if ctx.Struct != nil {
		t.Error("expected nil Struct")
	}
	if len(ctx.Methods) != 0 {
		t.Error("expected empty Methods")
	}
	if len(ctx.Blocks) != 0 {
		t.Error("expected empty Blocks")
	}
}

func TestGoFunctionContextDefaults(t *testing.T) {
	ctx := &GoFunctionContext{}
	if ctx.Function != nil {
		t.Error("expected nil Function")
	}
	if len(ctx.CalledFunctions) != 0 {
		t.Error("expected empty CalledFunctions")
	}
}

func TestContextBlock(t *testing.T) {
	b := ContextBlock{
		Kind:   "function",
		FileID: "main.go",
		Line:   10,
		Title:  "func Foo",
		Cut:    "func Foo() {}",
	}
	if b.Kind != "function" {
		t.Errorf("unexpected Kind: %q", b.Kind)
	}
	if b.Line != 10 {
		t.Errorf("unexpected Line: %d", b.Line)
	}
}

func TestFromGenericRoundtrip(t *testing.T) {
	domainTopo := &domain.Topology{
		Root:     "/test",
		Language: "go",
		Resources: map[string]domain.Resource{
			"pkg.Func1": {
				ID:       "pkg.Func1",
				Kind:     domain.ResourceFunction,
				Name:     "Func1",
				Location: domain.Location{StartsAt: 1, EndsAt: 5, Path: "main.go"},
				Properties: map[string]any{
					"input":  []any{map[string]any{"Name": "x", "Typing": "int"}},
					"output": []any{map[string]any{"Name": "", "Typing": "error"}},
				},
				Connections: map[string][]string{"calls": {"pkg.Func2"}},
			},
			"pkg.MyStruct": {
				ID:       "pkg.MyStruct",
				Kind:     domain.ResourceStruct,
				Name:     "MyStruct",
				Location: domain.Location{StartsAt: 10, EndsAt: 15, Path: "main.go"},
				Properties: map[string]any{
					"params": []any{map[string]any{"Name": "X", "Typing": "int"}},
				},
			},
			"pkg.MyInterface": {
				ID:       "pkg.MyInterface",
				Kind:     domain.ResourceInterface,
				Name:     "MyInterface",
				Location: domain.Location{StartsAt: 20, EndsAt: 25, Path: "main.go"},
				Properties: map[string]any{
					"methods": []any{map[string]any{"Name": "Do", "Input": []any{}, "Output": []any{}}},
				},
			},
			"pkg.GlobalVar": {
				ID:         "pkg.GlobalVar",
				Kind:       domain.ResourceVariable,
				Name:       "GlobalVar",
				Location:   domain.Location{StartsAt: 30, EndsAt: 30, Path: "main.go"},
				Properties: map[string]any{"typing": "string", "value": "\"hello\""},
			},
			"main.go": {
				ID:          "main.go",
				Kind:        domain.ResourceFile,
				Name:        "main.go",
				Properties:  map[string]any{"from_package": "mypackage"},
				Connections: map[string][]string{"has_function": {"pkg.Func1"}, "has_struct": {"pkg.MyStruct"}},
			},
			"mypackage": {
				ID:   "mypackage",
				Kind: domain.ResourcePackage,
				Name: "mypackage",
			},
			"github.com/foo/bar": {
				ID:   "github.com/foo/bar",
				Kind: domain.ResourceDependency,
				Name: "github.com/foo/bar",
			},
		},
		Errors: map[string]string{"file.go": "parse error"},
	}

	gt := FromGeneric(domainTopo)
	if gt == nil {
		t.Fatal("FromGeneric returned nil")
	}
	if gt.Root != "/test" {
		t.Errorf("expected root /test, got %q", gt.Root)
	}
	if len(gt.Functions) != 1 {
		t.Errorf("expected 1 function, got %d", len(gt.Functions))
	}
	if len(gt.Structs) != 1 {
		t.Errorf("expected 1 struct, got %d", len(gt.Structs))
	}
	if len(gt.Interfaces) != 1 {
		t.Errorf("expected 1 interface, got %d", len(gt.Interfaces))
	}
	if len(gt.ExternalVars) != 1 {
		t.Errorf("expected 1 external var, got %d", len(gt.ExternalVars))
	}
	if len(gt.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(gt.Files))
	}
	if len(gt.Packages) != 1 {
		t.Errorf("expected 1 package, got %d", len(gt.Packages))
	}
	if len(gt.Dependencies) != 1 {
		t.Errorf("expected 1 dependency, got %d", len(gt.Dependencies))
	}

	roundtrip := ToGeneric(gt)
	if roundtrip == nil {
		t.Fatal("ToGeneric returned nil")
	}
	if roundtrip.Language != "go" {
		t.Errorf("expected language 'go', got %q", roundtrip.Language)
	}
	if roundtrip.Root != "/test" {
		t.Errorf("expected root /test, got %q", roundtrip.Root)
	}
	if len(roundtrip.Resources) < 5 {
		t.Errorf("expected at least 5 resources, got %d", len(roundtrip.Resources))
	}
	if len(roundtrip.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(roundtrip.Errors))
	}
}

func TestFromGenericMethod(t *testing.T) {
	domainTopo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"pkg.Method1": {
				ID:       "pkg.Method1",
				Kind:     domain.ResourceMethod,
				Name:     "Method1",
				Location: domain.Location{StartsAt: 1, EndsAt: 5, Path: "main.go"},
				Properties: map[string]any{
					"input":       []any{},
					"output":      []any{},
					"method_from": "pkg.MyStruct",
				},
			},
		},
	}
	gt := FromGeneric(domainTopo)
	if len(gt.Functions) != 1 {
		t.Fatalf("expected 1 function, got %d", len(gt.Functions))
	}
	fn := gt.Functions["pkg.Method1"]
	if fn.MethodFrom == nil || *fn.MethodFrom != "pkg.MyStruct" {
		t.Errorf("expected method_from pkg.MyStruct")
	}
}

func TestFromGenericNamedType(t *testing.T) {
	domainTopo := &domain.Topology{
		Resources: map[string]domain.Resource{
			"pkg.MyType": {
				ID:         "pkg.MyType",
				Kind:       domain.ResourceNamedType,
				Name:       "MyType",
				Properties: map[string]any{"underlying": "string"},
				Location:   domain.Location{StartsAt: 1, EndsAt: 1, Path: "main.go"},
			},
		},
	}
	gt := FromGeneric(domainTopo)
	if len(gt.NamedTypes) != 1 {
		t.Fatalf("expected 1 named type, got %d", len(gt.NamedTypes))
	}
	nt := gt.NamedTypes["pkg.MyType"]
	if nt.Underlying != "string" {
		t.Errorf("expected underlying 'string', got %q", nt.Underlying)
	}
}
