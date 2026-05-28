package python

type ConnectionKind string

var (
	ConnCalls        ConnectionKind = "calls"
	ConnUsesClass    ConnectionKind = "uses_class"
	ConnUsesExtVar   ConnectionKind = "uses_extvar"
	ConnUsesPkg      ConnectionKind = "uses_package"
	ConnUsesDep      ConnectionKind = "uses_dependency"
	ConnHasMethod    ConnectionKind = "methods"
	ConnInherits     ConnectionKind = "inherits"
	ConnInheritedBy  ConnectionKind = "inherited_by"
	ConnConstructor  ConnectionKind = "constructor"
	ConnHasFunc      ConnectionKind = "has_function"
	ConnHasClass     ConnectionKind = "has_class"
	ConnHasVar       ConnectionKind = "has_extvar"
	ConnHasFile      ConnectionKind = "has_file"
	ConnImportsPkg   ConnectionKind = "imports_package"
	ConnImportsDep   ConnectionKind = "imports_dependency"
)

func (f *PythonFunction) Calls() []FunctionID {
	return castSlice[FunctionID](f.Connections[ConnCalls])
}

func (f *PythonFunction) UsesClass() []ClassID {
	return castSlice[ClassID](f.Connections[ConnUsesClass])
}

func (f *PythonFunction) UsesExtVar() []ExternalVarID {
	return castSlice[ExternalVarID](f.Connections[ConnUsesExtVar])
}

func (f *PythonFunction) UsesPkg() []PackagePath {
	return castSlice[PackagePath](f.Connections[ConnUsesPkg])
}

func (f *PythonFunction) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](f.Connections[ConnUsesDep])
}

func (c *PythonClass) Methods() []FunctionID {
	return castSlice[FunctionID](c.Connections[ConnHasMethod])
}

func (c *PythonClass) Inherits() []ClassID {
	return castSlice[ClassID](c.Connections[ConnInherits])
}

func (c *PythonClass) InheritedBy() []ClassID {
	return castSlice[ClassID](c.Connections[ConnInheritedBy])
}

func (c *PythonClass) UsesPkg() []PackagePath {
	return castSlice[PackagePath](c.Connections[ConnUsesPkg])
}

func (c *PythonClass) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](c.Connections[ConnUsesDep])
}

func (c *PythonClass) UsesClass() []ClassID {
	return castSlice[ClassID](c.Connections[ConnUsesClass])
}

func (m *PythonModule) Functions() []FunctionID {
	return castSlice[FunctionID](m.Connections[ConnHasFunc])
}

func (m *PythonModule) Classes() []ClassID {
	return castSlice[ClassID](m.Connections[ConnHasClass])
}

func (m *PythonModule) ExternalVars() []ExternalVarID {
	return castSlice[ExternalVarID](m.Connections[ConnHasVar])
}

func (m *PythonModule) PackagesImported() []PackagePath {
	return castSlice[PackagePath](m.Connections[ConnImportsPkg])
}

func (m *PythonModule) DependenciesImported() []PythonDependancy {
	var result []PythonDependancy
	for _, d := range m.Connections[ConnImportsDep] {
		result = append(result, PythonDependancy{PackagePath: DependancyPath(d)})
	}
	return result
}

func (p *PythonPackage) Files() []ModuleID {
	return castSlice[ModuleID](p.Connections[ConnHasFile])
}

func (p *PythonPackage) HasFunctions() []FunctionID {
	return castSlice[FunctionID](p.Connections[ConnHasFunc])
}

func (p *PythonPackage) HasClasses() []ClassID {
	return castSlice[ClassID](p.Connections[ConnHasClass])
}

func (p *PythonPackage) HasExternalVars() []ExternalVarID {
	return castSlice[ExternalVarID](p.Connections[ConnHasVar])
}

func castSlice[T ~string](ids []string) []T {
	result := make([]T, len(ids))
	for i, id := range ids {
		result[i] = T(id)
	}
	return result
}
