package python

type ConnectionKind string

// ConnImportsModule is a module->module import edge: its targets are module
// (file) IDs, resolved from a Python import to the specific file it pulls in.
// Unlike ConnImportsPkg (a directory-level grouping), this is the real
// file-to-file import relationship surfaced in the "Packages & Modules" viz.
var (
	ConnCalls       ConnectionKind = "calls"
	ConnUsesClass   ConnectionKind = "uses_class"
	ConnUsesExtVar  ConnectionKind = "uses_extvar"
	ConnUsesPkg     ConnectionKind = "uses_package"
	ConnUsesDep     ConnectionKind = "uses_dependency"
	ConnHasMethod   ConnectionKind = "methods"
	ConnInherits    ConnectionKind = "inherits"
	ConnInheritedBy ConnectionKind = "inherited_by"
	// ConnImplements / ConnImplementedBy capture structural Protocol
	// conformance: a class that defines every member of a typing.Protocol it
	// does not nominally inherit. They mirror the implements/implemented_by edge
	// kinds the Go and JS/TS scanners emit for interfaces.
	ConnImplements    ConnectionKind = "implements"
	ConnImplementedBy ConnectionKind = "implemented_by"
	ConnConstructor   ConnectionKind = "constructor"
	ConnHasFunc       ConnectionKind = "has_function"
	ConnHasClass      ConnectionKind = "has_class"
	ConnHasVar        ConnectionKind = "has_extvar"
	ConnHasFile       ConnectionKind = "has_file"
	ConnImportsPkg    ConnectionKind = "imports_package"
	ConnImportsDep    ConnectionKind = "imports_dependency"
	// ConnImportsModule is a module->module import edge: its targets are module
	// (file) IDs, resolved from a Python import to the specific file it pulls in.
	// Unlike ConnImportsPkg (a directory-level grouping), this is the real
	// file-to-file import relationship surfaced in the "Packages & Modules" viz.
	ConnImportsModule ConnectionKind = "imports_module"
)

// Returns the list of functions called by this Python function.
func (f *PythonFunction) Calls() []FunctionID {
	return castSlice[FunctionID](f.Connections[ConnCalls])
}

// Returns the list of classes used by this Python function.
func (f *PythonFunction) UsesClass() []ClassID {
	return castSlice[ClassID](f.Connections[ConnUsesClass])
}

// Returns the list of external variables used by this Python function.
func (f *PythonFunction) UsesExtVar() []ExternalVarID {
	return castSlice[ExternalVarID](f.Connections[ConnUsesExtVar])
}

// Returns the list of package paths used by this Python function.
func (f *PythonFunction) UsesPkg() []PackagePath {
	return castSlice[PackagePath](f.Connections[ConnUsesPkg])
}

// Returns the list of dependency paths used by this Python function.
func (f *PythonFunction) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](f.Connections[ConnUsesDep])
}

// Returns the list of methods defined in this Python class
func (c *PythonClass) Methods() []FunctionID {
	return castSlice[FunctionID](c.Connections[ConnHasMethod])
}

// Returns the list of classes this Python class inherits from
func (c *PythonClass) Inherits() []ClassID {
	return castSlice[ClassID](c.Connections[ConnInherits])
}

// Returns the list of class IDs that inherit from this PythonClass.
func (c *PythonClass) InheritedBy() []ClassID {
	return castSlice[ClassID](c.Connections[ConnInheritedBy])
}

// Returns the list of Protocol class IDs this class structurally implements.
func (c *PythonClass) Implements() []ClassID {
	return castSlice[ClassID](c.Connections[ConnImplements])
}

// Returns the list of class IDs that structurally implement this Protocol.
func (c *PythonClass) ImplementedBy() []ClassID {
	return castSlice[ClassID](c.Connections[ConnImplementedBy])
}

// Returns the list of package paths used by this Python class
func (c *PythonClass) UsesPkg() []PackagePath {
	return castSlice[PackagePath](c.Connections[ConnUsesPkg])
}

// Returns the list of dependency paths used by this Python class
func (c *PythonClass) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](c.Connections[ConnUsesDep])
}

// Returns the list of classes used by this Python class
func (c *PythonClass) UsesClass() []ClassID {
	return castSlice[ClassID](c.Connections[ConnUsesClass])
}

// Returns functions defined in this Python module.
func (m *PythonModule) Functions() []FunctionID {
	return castSlice[FunctionID](m.Connections[ConnHasFunc])
}

// Returns the list of class IDs defined in this module.
func (m *PythonModule) Classes() []ClassID {
	return castSlice[ClassID](m.Connections[ConnHasClass])
}

// Returns external variables defined in this Python module.
func (m *PythonModule) ExternalVars() []ExternalVarID {
	return castSlice[ExternalVarID](m.Connections[ConnHasVar])
}

// Returns external packages imported by this Python module.
func (m *PythonModule) PackagesImported() []PackagePath {
	return castSlice[PackagePath](m.Connections[ConnImportsPkg])
}

// Returns modules imported by this Python module.
func (m *PythonModule) ModulesImported() []ModuleID {
	return castSlice[ModuleID](m.Connections[ConnImportsModule])
}

// Returns the external dependencies this module imports, as PythonDependancy objects.
func (m *PythonModule) DependenciesImported() []PythonDependancy {
	var result []PythonDependancy
	for _, d := range m.Connections[ConnImportsDep] {
		result = append(result, PythonDependancy{PackagePath: DependancyPath(d)})
	}
	return result
}

// Generic helper that converts a string slice to a typed slice by casting each element.
func castSlice[T ~string](ids []string) []T {
	result := make([]T, len(ids))
	for i, id := range ids {
		result[i] = T(id)
	}
	return result
}
