package javascript

type ConnectionKind string

// ConnImportsModule is a module->module import edge: its targets are module
// (file) IDs, resolved from an import specifier to the specific file it pulls
// in. This is the real file-to-file import relationship surfaced in the
// "Packages & Modules" viz.
var (
	ConnCalls         ConnectionKind = "calls"
	ConnUsesClass     ConnectionKind = "uses_class"
	ConnUsesExtVar    ConnectionKind = "uses_extvar"
	ConnUsesPkg       ConnectionKind = "uses_package"
	ConnUsesDep       ConnectionKind = "uses_dependency"
	ConnHasMethod     ConnectionKind = "methods"
	ConnInherits      ConnectionKind = "inherits"
	ConnInheritedBy   ConnectionKind = "inherited_by"
	ConnConstructor   ConnectionKind = "constructor"
	ConnHasFunc       ConnectionKind = "has_function"
	ConnHasClass      ConnectionKind = "has_class"
	ConnHasVar        ConnectionKind = "has_extvar"
	ConnHasFile       ConnectionKind = "has_file"
	ConnImportsPkg    ConnectionKind = "imports_package"
	ConnImportsDep    ConnectionKind = "imports_dependency"
	ConnImplements    ConnectionKind = "implements"
	ConnImplementedBy ConnectionKind = "implemented_by"
	ConnUsesNamedType ConnectionKind = "uses_named_type"
	ConnUsesInterface ConnectionKind = "uses_interface"
	ConnHasInterface  ConnectionKind = "has_interface"
	ConnHasNamedType  ConnectionKind = "has_named_type"
	// ConnImportsModule is a module->module import edge: its targets are module
	// (file) IDs, resolved from an import specifier to the specific file it pulls
	// in. This is the real file-to-file import relationship surfaced in the
	// "Packages & Modules" viz.
	ConnImportsModule ConnectionKind = "imports_module"
)

// Returns functions called by a JavaScript function.
func (f *JavaScriptFunction) Calls() []FunctionID {
	return castSlice[FunctionID](f.Connections[ConnCalls])
}

// Returns classes used by a JavaScript function.
func (f *JavaScriptFunction) UsesClass() []ClassID {
	return castSlice[ClassID](f.Connections[ConnUsesClass])
}

// Returns external variables referenced by this JavaScript function.
func (f *JavaScriptFunction) UsesExtVar() []ExternalVarID {
	return castSlice[ExternalVarID](f.Connections[ConnUsesExtVar])
}

// Returns packages imported or used by this JavaScript function.
func (f *JavaScriptFunction) UsesPkg() []PackagePath {
	return castSlice[PackagePath](f.Connections[ConnUsesPkg])
}

// Returns dependencies used by a JavaScript function.
func (f *JavaScriptFunction) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](f.Connections[ConnUsesDep])
}

// Returns named types used by this JavaScript function.
func (f *JavaScriptFunction) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](f.Connections[ConnUsesNamedType])
}

// Returns interfaces used by this JavaScript function.
func (f *JavaScriptFunction) UsesInterface() []InterfaceID {
	return castSlice[InterfaceID](f.Connections[ConnUsesInterface])
}

// Returns the list of methods defined in this class.
func (c *JavaScriptClass) Methods() []FunctionID {
	return castSlice[FunctionID](c.Connections[ConnHasMethod])
}

// Returns the list of classes this class inherits from.
func (c *JavaScriptClass) Inherits() []ClassID {
	return castSlice[ClassID](c.Connections[ConnInherits])
}

// Returns the list of classes that inherit from a JavaScriptClass.
func (c *JavaScriptClass) InheritedBy() []ClassID {
	return castSlice[ClassID](c.Connections[ConnInheritedBy])
}

// Returns packages used by a JavaScript class.
func (c *JavaScriptClass) UsesPkg() []PackagePath {
	return castSlice[PackagePath](c.Connections[ConnUsesPkg])
}

// Returns the list of external dependencies this class uses.
func (c *JavaScriptClass) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](c.Connections[ConnUsesDep])
}

// Returns the list of classes this class uses or depends on.
func (c *JavaScriptClass) UsesClass() []ClassID {
	return castSlice[ClassID](c.Connections[ConnUsesClass])
}

// Returns the list of interfaces implemented by a JavaScriptClass.
func (c *JavaScriptClass) Implements() []InterfaceID {
	return castSlice[InterfaceID](c.Connections[ConnImplements])
}

// Returns named types used by a JavaScript class.
func (c *JavaScriptClass) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](c.Connections[ConnUsesNamedType])
}

// Returns the list of interfaces this class implements or uses.
func (c *JavaScriptClass) UsesInterface() []InterfaceID {
	return castSlice[InterfaceID](c.Connections[ConnUsesInterface])
}

// Returns classes implementing this JavaScript interface.
func (i *JavaScriptInterface) ImplementedBy() []ClassID {
	return castSlice[ClassID](i.Connections[ConnImplementedBy])
}

// Returns the list of interfaces that this interface inherits from.
func (i *JavaScriptInterface) Inherits() []InterfaceID {
	return castSlice[InterfaceID](i.Connections[ConnInherits])
}

// Returns the list of interfaces that inherit from this interface.
func (i *JavaScriptInterface) InheritedBy() []InterfaceID {
	return castSlice[InterfaceID](i.Connections[ConnInheritedBy])
}

// Returns the slice of functions defined in this JavaScript module.
func (m *JavaScriptModule) Functions() []FunctionID {
	return castSlice[FunctionID](m.Connections[ConnHasFunc])
}

// Returns the list of class IDs defined in this module
func (m *JavaScriptModule) Classes() []ClassID {
	return castSlice[ClassID](m.Connections[ConnHasClass])
}

// Returns the slice of external variables defined in this JavaScript module.
func (m *JavaScriptModule) ExternalVars() []ExternalVarID {
	return castSlice[ExternalVarID](m.Connections[ConnHasVar])
}

// Returns the slice of interfaces defined in this JavaScript module.
func (m *JavaScriptModule) Interfaces() []InterfaceID {
	return castSlice[InterfaceID](m.Connections[ConnHasInterface])
}

// Returns the slice of named types defined in this JavaScript module.
func (m *JavaScriptModule) NamedTypes() []NamedTypeID {
	return castSlice[NamedTypeID](m.Connections[ConnHasNamedType])
}

// Returns the list of packages imported by this JavaScript module.
func (m *JavaScriptModule) PackagesImported() []PackagePath {
	return castSlice[PackagePath](m.Connections[ConnImportsPkg])
}

// Returns the slice of modules imported by this JavaScript module.
func (m *JavaScriptModule) ModulesImported() []ModuleID {
	return castSlice[ModuleID](m.Connections[ConnImportsModule])
}

// Returns the list of external dependencies imported by this module
func (m *JavaScriptModule) DependenciesImported() []JavaScriptDependancy {
	var result []JavaScriptDependancy
	for _, d := range m.Connections[ConnImportsDep] {
		result = append(result, JavaScriptDependancy{PackagePath: DependancyPath(d)})
	}
	return result
}

// Generic helper that casts a slice of strings to a slice of a compatible string-based type.
func castSlice[T ~string](ids []string) []T {
	result := make([]T, len(ids))
	for i, id := range ids {
		result[i] = T(id)
	}
	return result
}
