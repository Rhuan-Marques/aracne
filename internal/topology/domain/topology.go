package domain

// Represents the entire project topology: the root path, language, map of all resources keyed by ID, and per-file error messages. This is the top-level domain model for scanned projects.
type Topology struct {
	Root      string
	Language  string
	Resources map[string]Resource
	Errors    map[string]string
}

// Generic representation of any resource in the topology graph. Stores ID, kind, name, description, source location, arbitrary properties, and typed connections to other resources.
type Resource struct {
	ID          string
	Kind        ResourceKind
	Name        string
	Description string
	Location    Location
	Properties  map[string]any
	Connections map[string][]string
}

// Represents a warning emitted during topology file updates, containing the affected resource kind, a list of affected resource IDs, and a human-readable message describing the change.
type TopologyWarning struct {
	Resource          ResourceKind
	AffectedResources []string
	Message           string
}
