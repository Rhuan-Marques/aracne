package domain

import "sort"

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

// SortWarnings puts a warning list in the one order every surface prints it in: kind, then
// source, then target, then id.
//
// A TOTAL order, and that is the point. The warnings table is a map, so a listing built by
// ranging over it arrives shuffled, and the two sorts that existed (`arac warnings list` and
// the warnings_list tool, both keyed on kind+source only) left every tie to that shuffle. That
// was merely untidy while the whole list was printed. It stops being untidy the moment
// something takes the FIRST N of it -- the warning-read expansion does, and "the first five"
// has to be the same five each time, and the same five the surface that continues the fixing
// loop picks up. See cli.warningReadSection.
func SortWarnings(ws []TopologyWarning) {
	sort.SliceStable(ws, func(i, j int) bool {
		a, b := ws[i], ws[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		if a.TargetID != b.TargetID {
			return a.TargetID < b.TargetID
		}
		return a.ID < b.ID
	})
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
	// ExactHash and NormHash fingerprint this resource's own source span, so that code which
	// MOVED can be recognised as the same code after its id changes -- which it does in every
	// language whose module path is minted from the filename (Python, JavaScript, TypeScript,
	// Rust). See helper.BodyHashes for what each one covers and why there are two.
	//
	// Stamped on the scan path while the file is still on disk, and persisted, because the
	// point is to answer a question asked AFTER the old file is gone. Empty for a resource
	// whose body is too small to fingerprint safely, and for one read out of a database
	// written before these columns existed.
	ExactHash string
	NormHash  string
	// NormLines is how many lines the body came to once comments and blanks were removed. It
	// is what the weaker match tiers check before trusting a hash; see helper.WeakTierMinLines.
	NormLines int
}
