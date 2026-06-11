package domain

type WarningKind string

const (
	WarnUseMissingNode   WarningKind = "use_missing_node"
	WarnNodeRemoved      WarningKind = "node_removed"
	WarnSignatureChanged WarningKind = "signature_changed"
)

type TopologyWarning struct {
	ID       string
	SourceID string
	Kind     WarningKind
	TargetID string
	Message  string
}

type Topology struct {
	Root      string
	Language  string
	Languages []string
	Resources map[string]Resource
	Warnings  map[string]TopologyWarning
	Errors    map[string]string
}

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
