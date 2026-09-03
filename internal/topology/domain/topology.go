package domain

type WarningKind string

const (
	WarnUseMissingNode   WarningKind = "use_missing_node"
	WarnNodeRemoved      WarningKind = "node_removed"
	WarnSignatureChanged WarningKind = "signature_changed"
	// WarnInterfaceConflict fires when a type DECLARES that it implements an interface and
	// no longer does -- a missing method, or one whose signature the interface does not
	// accept. Distinct from signature_changed because the thing to go fix is the
	// implementer's own declaration, not a call.
	WarnInterfaceConflict WarningKind = "interface_conflict"
)

// Warning about a topology issue linking a source resource to a target with a kind and message.
type TopologyWarning struct {
	ID       string
	SourceID string
	Kind     WarningKind
	TargetID string
	Message  string
	// Baseline is the signature SourceID had at the moment this warning was
	// raised -- the shape TargetID was written against, in the language-agnostic
	// form SignatureBaseline produces. Only signature_changed sets it.
	//
	// WHY IT IS STORED AND NOT RECOMPUTED. A signature_changed warning is
	// discharged by two different events, and only one of them was ever
	// detectable. Re-parsing the CALLER answers it (that is
	// ClearReferrerWarningsForFile, keyed on TargetID). Putting the DEFINITION
	// back the way it was answers it too, and nothing could see that: the
	// database holds the current signature and the previous one is overwritten
	// on every scan, so after a revert there is nothing left to compare against
	// and the warning outlived the change that caused it. Recording the shape
	// the callers were written against makes the revert a string comparison.
	//
	// It survives repeated edits deliberately: a definition changed A->B->C
	// keeps Baseline A, because A is still what the callers were written
	// against. See PreserveSignatureBaselines.
	Baseline string
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
