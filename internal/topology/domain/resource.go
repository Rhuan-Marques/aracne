package domain

type ResourceKind string

// ResourceKind constant representing a function resource in the topology domain model.
// ResourceKind constant representing a Go type (struct or type alias) in the topology model.
// ResourceKind constant representing a file resource in the topology domain model.
// ResourceKind constant for external/package-level variable resources in the topology graph.
// Resource kind constant representing an external dependency (third-party imported package).
// ResourceKind constant for interface type resources in the topology graph.
// Resource kind constant representing a Go method (a function with a receiver).
// ResourcePackage is the ResourceKind constant ("package") identifying resources that represent Go packages.
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
