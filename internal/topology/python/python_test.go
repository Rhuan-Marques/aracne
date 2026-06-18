package python

import (
	"testing"

	"aracne/internal/topology/domain"
)

func TestPythonConnectionKindValues(t *testing.T) {
	tests := []struct {
		kind ConnectionKind
		want string
	}{
		{ConnCalls, "calls"},
		{ConnUsesClass, "uses_class"},
		{ConnUsesExtVar, "uses_extvar"},
		{ConnUsesPkg, "uses_package"},
		{ConnUsesDep, "uses_dependency"},
		{ConnHasMethod, "methods"},
		{ConnInherits, "inherits"},
		{ConnInheritedBy, "inherited_by"},
		{ConnConstructor, "constructor"},
		{ConnHasFunc, "has_function"},
		{ConnHasClass, "has_class"},
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

func TestPythonFunctionAccessors(t *testing.T) {
	fn := PythonFunction{
		ID:   "mod.func",
		Name: "func",
		Connections: map[ConnectionKind][]string{
			ConnCalls:      {"mod.bar", "mod.baz"},
			ConnUsesClass:  {"mod.MyClass"},
			ConnUsesExtVar: {"mod.GlobalVar"},
			ConnUsesPkg:    {"os"},
			ConnUsesDep:    {"requests"},
		},
	}

	if calls := fn.Calls(); len(calls) != 2 || calls[0] != "mod.bar" {
		t.Errorf("Calls() = %v, want [mod.bar mod.baz]", calls)
	}
	if classes := fn.UsesClass(); len(classes) != 1 || classes[0] != "mod.MyClass" {
		t.Errorf("UsesClass() = %v", classes)
	}
	if vars := fn.UsesExtVar(); len(vars) != 1 || vars[0] != "mod.GlobalVar" {
		t.Errorf("UsesExtVar() = %v", vars)
	}
	if pkgs := fn.UsesPkg(); len(pkgs) != 1 || pkgs[0] != "os" {
		t.Errorf("UsesPkg() = %v", pkgs)
	}
	if deps := fn.UsesDep(); len(deps) != 1 || deps[0] != "requests" {
		t.Errorf("UsesDep() = %v", deps)
	}
}

func TestPythonFunctionAccessorsEmpty(t *testing.T) {
	fn := PythonFunction{Connections: make(map[ConnectionKind][]string)}
	if calls := fn.Calls(); len(calls) != 0 {
		t.Errorf("expected empty Calls, got %v", calls)
	}
}

func TestPythonFunctionAccessorsNil(t *testing.T) {
	var fn PythonFunction
	calls := fn.Calls()
	if len(calls) != 0 {
		t.Errorf("expected empty Calls, got %v", calls)
	}
}

func TestPythonClassAccessors(t *testing.T) {
	c := PythonClass{
		ID:   "mod.MyClass",
		Name: "MyClass",
		Connections: map[ConnectionKind][]string{
			ConnHasMethod:   {"mod.Method1", "mod.Method2"},
			ConnInherits:    {"mod.BaseClass"},
			ConnInheritedBy: {"mod.SubClass"},
			ConnUsesPkg:     {"os"},
			ConnUsesDep:     {"requests"},
		},
	}

	if methods := c.Methods(); len(methods) != 2 {
		t.Errorf("Methods() = %v, want 2 methods", methods)
	}
	if inherits := c.Inherits(); len(inherits) != 1 || inherits[0] != "mod.BaseClass" {
		t.Errorf("Inherits() = %v", inherits)
	}
	if inheritedBy := c.InheritedBy(); len(inheritedBy) != 1 || inheritedBy[0] != "mod.SubClass" {
		t.Errorf("InheritedBy() = %v", inheritedBy)
	}
}

func TestPythonModuleAccessors(t *testing.T) {
	m := PythonModule{
		ID:   "module.py",
		Name: "module.py",
		Connections: map[ConnectionKind][]string{
			ConnHasFunc:    {"mod.Func1"},
			ConnHasClass:   {"mod.MyClass"},
			ConnHasVar:     {"mod.Global"},
			ConnImportsPkg: {"os", "sys"},
			ConnImportsDep: {"requests"},
		},
	}

	if funcs := m.Functions(); len(funcs) != 1 {
		t.Errorf("Functions() = %v", funcs)
	}
	if classes := m.Classes(); len(classes) != 1 {
		t.Errorf("Classes() = %v", classes)
	}
	if vars := m.ExternalVars(); len(vars) != 1 {
		t.Errorf("ExternalVars() = %v", vars)
	}
	if pkgs := m.PackagesImported(); len(pkgs) != 2 {
		t.Errorf("PackagesImported() = %v", pkgs)
	}
	if deps := m.DependenciesImported(); len(deps) != 1 {
		t.Errorf("DependenciesImported() = %v", deps)
	}
}

func TestPythonFromGenericNil(t *testing.T) {
	result := FromGeneric(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestPythonToGenericNil(t *testing.T) {
	result := ToGeneric(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestPythonManagerNilCheck(t *testing.T) {
	mgr := NewPythonManager(nil)
	if mgr == nil {
		t.Fatal("NewPythonManager returned nil")
	}
	if mgr.Generic() != nil {
		t.Errorf("expected nil generic manager")
	}
}

func TestPythonFunctionProperties(t *testing.T) {
	fn := PythonFunction{
		ID:         "mod.func",
		Name:       "func",
		Decorators: []string{"staticmethod"},
		IsAsync:    true,
	}
	if !fn.IsAsync {
		t.Error("expected IsAsync=true")
	}
	if len(fn.Decorators) != 1 || fn.Decorators[0] != "staticmethod" {
		t.Errorf("unexpected decorators: %v", fn.Decorators)
	}
}

func TestPythonClassABCProperties(t *testing.T) {
	c := PythonClass{
		ID:                 "mod.ABCClass",
		Name:               "ABCClass",
		IsABC:              true,
		IsProtocol:         false,
		HasAbstractMethods: true,
		Bases:              []string{"ABC"},
	}
	if !c.IsABC {
		t.Error("expected IsABC=true")
	}
	if c.IsProtocol {
		t.Error("expected IsProtocol=false")
	}
	if !c.HasAbstractMethods {
		t.Error("expected HasAbstractMethods=true")
	}
	if len(c.Bases) != 1 || c.Bases[0] != "ABC" {
		t.Errorf("unexpected bases: %v", c.Bases)
	}
}

func TestPythonClassUsesClass(t *testing.T) {
	c := PythonClass{
		ID:   "mod.MyClass",
		Name: "MyClass",
		Connections: map[ConnectionKind][]string{
			ConnUsesClass: {"mod.OtherClass"},
		},
	}
	if classes := c.UsesClass(); len(classes) != 1 || classes[0] != "mod.OtherClass" {
		t.Errorf("UsesClass() = %v", classes)
	}
}

func TestPythonFromGeneric(t *testing.T) {
	domainTopo := &domain.Topology{
		Root:     "/test",
		Language: "python",
		Resources: map[string]domain.Resource{
			"mod.func": {
				ID:       "mod.func",
				Kind:     domain.ResourceFunction,
				Name:     "func",
				Location: domain.Location{StartsAt: 1, EndsAt: 3, Path: "mod.py"},
				Properties: map[string]any{
					"input":      []any{map[string]any{"Name": "x", "Typing": "int"}},
					"output":     []any{},
					"decorators": []any{"staticmethod"},
					"is_async":   true,
				},
				Connections: map[string][]string{"calls": {"mod.bar"}},
			},
			"mod.MyClass": {
				ID:       "mod.MyClass",
				Kind:     domain.ResourceType,
				Name:     "MyClass",
				Location: domain.Location{StartsAt: 10, EndsAt: 20, Path: "mod.py"},
				Properties: map[string]any{
					"params":               []any{map[string]any{"Name": "x", "Typing": "int"}},
					"bases":                []any{"Base"},
					"is_abc":               true,
					"is_protocol":          false,
					"has_abstract_methods": true,
				},
			},
			"mod.GlobalVar": {
				ID:         "mod.GlobalVar",
				Kind:       domain.ResourceVariable,
				Name:       "GlobalVar",
				Location:   domain.Location{StartsAt: 30, EndsAt: 30, Path: "mod.py"},
				Properties: map[string]any{"typing": "str", "value": "\"hello\""},
			},
			"mod.py": {
				ID:          "mod.py",
				Kind:        domain.ResourceFile,
				Name:        "mod.py",
				Properties:  map[string]any{"from_package": "mypackage"},
				Connections: map[string][]string{"has_function": {"mod.func"}, "has_class": {"mod.MyClass"}},
			},
			"mypackage": {
				ID:   "mypackage",
				Kind: domain.ResourcePackage,
				Name: "mypackage",
			},
			"requests": {
				ID:   "requests",
				Kind: domain.ResourceDependency,
				Name: "requests",
			},
		},
		Errors: map[string]string{"mod.py": "warning"},
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
	if len(gt.Classes) != 1 {
		t.Errorf("expected 1 class, got %d", len(gt.Classes))
	}
	if len(gt.ExternalVars) != 1 {
		t.Errorf("expected 1 external var, got %d", len(gt.ExternalVars))
	}
	if len(gt.Modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(gt.Modules))
	}
	if len(gt.Dependencies) != 1 {
		t.Errorf("expected 1 dependency, got %d", len(gt.Dependencies))
	}

	fn := gt.Functions["mod.func"]
	if !fn.IsAsync {
		t.Error("expected IsAsync=true")
	}
	if len(fn.Decorators) != 1 || fn.Decorators[0] != "staticmethod" {
		t.Errorf("decorators: %v", fn.Decorators)
	}

	cls := gt.Classes["mod.MyClass"]
	if !cls.IsABC {
		t.Error("expected IsABC=true")
	}
	if !cls.HasAbstractMethods {
		t.Error("expected HasAbstractMethods=true")
	}
	if len(cls.Bases) != 1 || cls.Bases[0] != "Base" {
		t.Errorf("bases: %v", cls.Bases)
	}

	roundtrip := ToGeneric(gt)
	if roundtrip == nil {
		t.Fatal("ToGeneric returned nil")
	}
	if roundtrip.Language != "python" {
		t.Errorf("expected language 'python', got %q", roundtrip.Language)
	}
	if roundtrip.Root != "/test" {
		t.Errorf("expected root /test, got %q", roundtrip.Root)
	}
	if len(roundtrip.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(roundtrip.Errors))
	}
}

func TestPythonContextDefaults(t *testing.T) {
	fnCtx := &PythonFunctionContext{}
	if fnCtx.Function != nil {
		t.Error("expected nil Function")
	}
	if len(fnCtx.CalledFunctions) != 0 {
		t.Error("expected empty CalledFunctions")
	}

	clsCtx := &PythonClassContext{}
	if clsCtx.Class != nil {
		t.Error("expected nil Class")
	}
	if len(clsCtx.Methods) != 0 {
		t.Error("expected empty Methods")
	}
}

func TestPythonSimplifiedClass(t *testing.T) {
	sc := SimplifiedClass{
		ID:                     "mod.Base",
		Name:                   "Base",
		NeedToImplement:        true,
		NeedToImplementMethods: []string{"abstract_method"},
	}
	if !sc.NeedToImplement {
		t.Error("expected NeedToImplement=true")
	}
	if len(sc.NeedToImplementMethods) != 1 || sc.NeedToImplementMethods[0] != "abstract_method" {
		t.Errorf("unexpected NeedToImplementMethods: %v", sc.NeedToImplementMethods)
	}
}

func TestPythonVariableDefinition(t *testing.T) {
	v := VariableDefinition{Name: "x", Typing: "int"}
	if v.Name != "x" {
		t.Errorf("expected name x, got %q", v.Name)
	}
}

func TestPythonFunctionDefinition(t *testing.T) {
	fd := FunctionDefinition{
		Name: "func",
		Input: []VariableDefinition{
			{Name: "self"},
		},
	}
	if fd.Name != "func" || len(fd.Input) != 1 {
		t.Errorf("unexpected FunctionDefinition: %+v", fd)
	}
}
