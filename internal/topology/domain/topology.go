package domain

type Topology struct {
	Root      string
	Language  string
	Resources map[string]Resource
	Errors    map[string]string
}

type Resource struct {
	ID          string
	Kind        ResourceKind
	Name        string
	Description string
	Location    Location
	Properties  map[string]any
	Connections map[string][]string
}

type TopologyWarning struct {
	Resource          ResourceKind
	AffectedResources []string
	Message           string
}
