package domain

type WarningKind string

const (
	WarnUseMissingNode   WarningKind = "use_missing_node"
	WarnNodeRemoved      WarningKind = "node_removed"
	WarnSignatureChanged WarningKind = "signature_changed"
)

// Warning about a topology issue linking a source resource to a target with a kind and message.
type TopologyWarning struct {
	ID       string
	SourceID string
	Kind     WarningKind
	TargetID string
	Message  string
}

// Graph of code resources indexed by ID, with language tracking, warnings, and errors for a codebase root.
type Topology struct {
	Root      string
	Language  string
	Languages []string
	Resources map[string]Resource
	Warnings  map[string]TopologyWarning
	Errors    map[string]string
}

// Represents a code resource (function, struct, variable, etc.) with metadata, location, and connections to other resources.
type Resource struct {
	ID          string
	Kind        ResourceKind
	Name        string
	Language    string
	Description string
	Location    Location
	Properties  map[string]any
	Connections map[string][]string
}
