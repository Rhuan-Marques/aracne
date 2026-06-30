package domain

type ResourceKind string

var (
	ResourcePackage    ResourceKind = "package"
	ResourceFile       ResourceKind = "file"
	ResourceFunction   ResourceKind = "function"
	ResourceMethod     ResourceKind = "method"
	ResourceStruct     ResourceKind = "struct"
	ResourceNamedType  ResourceKind = "named_type"
	ResourceInterface  ResourceKind = "interface"
	ResourceVariable   ResourceKind = "variable"
	ResourceDependency ResourceKind = "dependency"
)
