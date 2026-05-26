package domain

type ResourceKind string

var (
	ResourcePackage    ResourceKind = "package"
	ResourceFile       ResourceKind = "file"
	ResourceFunction   ResourceKind = "function"
	ResourceMethod     ResourceKind = "method"
	ResourceType       ResourceKind = "type"
	ResourceInterface  ResourceKind = "interface"
	ResourceVariable   ResourceKind = "variable"
	ResourceDependency ResourceKind = "dependency"
)
