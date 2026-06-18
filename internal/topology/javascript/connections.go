package javascript

type ConnectionKind string

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

func (f *JavaScriptFunction) Calls() []FunctionID {
	return castSlice[FunctionID](f.Connections[ConnCalls])
}

func (f *JavaScriptFunction) UsesClass() []ClassID {
	return castSlice[ClassID](f.Connections[ConnUsesClass])
}

func (f *JavaScriptFunction) UsesExtVar() []ExternalVarID {
	return castSlice[ExternalVarID](f.Connections[ConnUsesExtVar])
}

func (f *JavaScriptFunction) UsesPkg() []PackagePath {
	return castSlice[PackagePath](f.Connections[ConnUsesPkg])
}

func (f *JavaScriptFunction) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](f.Connections[ConnUsesDep])
}

func (f *JavaScriptFunction) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](f.Connections[ConnUsesNamedType])
}

func (f *JavaScriptFunction) UsesInterface() []InterfaceID {
	return castSlice[InterfaceID](f.Connections[ConnUsesInterface])
}

func (c *JavaScriptClass) Methods() []FunctionID {
	return castSlice[FunctionID](c.Connections[ConnHasMethod])
}

func (c *JavaScriptClass) Inherits() []ClassID {
	return castSlice[ClassID](c.Connections[ConnInherits])
}

func (c *JavaScriptClass) InheritedBy() []ClassID {
	return castSlice[ClassID](c.Connections[ConnInheritedBy])
}

func (c *JavaScriptClass) UsesPkg() []PackagePath {
	return castSlice[PackagePath](c.Connections[ConnUsesPkg])
}

func (c *JavaScriptClass) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](c.Connections[ConnUsesDep])
}

func (c *JavaScriptClass) UsesClass() []ClassID {
	return castSlice[ClassID](c.Connections[ConnUsesClass])
}

func (c *JavaScriptClass) Implements() []InterfaceID {
	return castSlice[InterfaceID](c.Connections[ConnImplements])
}

func (c *JavaScriptClass) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](c.Connections[ConnUsesNamedType])
}

func (c *JavaScriptClass) UsesInterface() []InterfaceID {
	return castSlice[InterfaceID](c.Connections[ConnUsesInterface])
}

func (i *JavaScriptInterface) ImplementedBy() []ClassID {
	return castSlice[ClassID](i.Connections[ConnImplementedBy])
}

func (i *JavaScriptInterface) Inherits() []InterfaceID {
	return castSlice[InterfaceID](i.Connections[ConnInherits])
}

func (i *JavaScriptInterface) InheritedBy() []InterfaceID {
	return castSlice[InterfaceID](i.Connections[ConnInheritedBy])
}

func (m *JavaScriptModule) Functions() []FunctionID {
	return castSlice[FunctionID](m.Connections[ConnHasFunc])
}

func (m *JavaScriptModule) Classes() []ClassID {
	return castSlice[ClassID](m.Connections[ConnHasClass])
}

func (m *JavaScriptModule) ExternalVars() []ExternalVarID {
	return castSlice[ExternalVarID](m.Connections[ConnHasVar])
}

func (m *JavaScriptModule) Interfaces() []InterfaceID {
	return castSlice[InterfaceID](m.Connections[ConnHasInterface])
}

func (m *JavaScriptModule) NamedTypes() []NamedTypeID {
	return castSlice[NamedTypeID](m.Connections[ConnHasNamedType])
}

func (m *JavaScriptModule) PackagesImported() []PackagePath {
	return castSlice[PackagePath](m.Connections[ConnImportsPkg])
}

func (m *JavaScriptModule) ModulesImported() []ModuleID {
	return castSlice[ModuleID](m.Connections[ConnImportsModule])
}

func (m *JavaScriptModule) DependenciesImported() []JavaScriptDependancy {
	var result []JavaScriptDependancy
	for _, d := range m.Connections[ConnImportsDep] {
		result = append(result, JavaScriptDependancy{PackagePath: DependancyPath(d)})
	}
	return result
}

func castSlice[T ~string](ids []string) []T {
	result := make([]T, len(ids))
	for i, id := range ids {
		result[i] = T(id)
	}
	return result
}
