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

	// Transient marks a warning REPORTED to the agent and never stored.
	//
	// It is the answer to a call the rules could not judge: a parameter whose type moved,
	// against an argument the scanner never recorded a type for. Saying nothing there is
	// what let the most common breaking edit in a typed language report nothing at all;
	// saying it in the warnings table would be worse. A stored warning asserts a break,
	// and this one is a maybe -- and it has no discharge event, because the argument that
	// could not be read this scan cannot be read the next one either, so it would stand
	// forever and be cleared by hand.
	//
	// Never written to SQLite. It reaches the model through the post-edit report only, so
	// `arac warnings list` keeps meaning "these are actually broken".
	Transient bool

	// State fingerprints both endpoints of a transient as they stood when it was raised --
	// see helper.TransientState. A transient is reported once PER STATE: the same id raised
	// again over the same caller and callee is a finding the agent already has, and one
	// raised over a changed caller or callee is a new question. Empty on stored warnings.
	State string
}

// SignatureChangedMessage and SignatureUnverifiedMessage are the two sentences a changed
// signature produces, and the ONLY two places either is written.
//
// They were built in two producers with different wording -- goscanner emitted one shape and
// helper.ExpandSignatureWarnings another for every other language -- so the same event read
// differently depending on which scanner saw it. They are here, in the package both import,
// because the difference between them is the entire point of the transient channel: one says
// go fix this, the other says go look at this. A reader has to be able to tell them apart at
// a glance, which they cannot do if the wording drifts.
func SignatureChangedMessage(calleeName, callerID string) string {
	return "function " + calleeName + " changed, fix caller " + callerID
}

// SignatureUnverifiedMessage is deliberately a different SENTENCE, not the one above with a
// caveat bolted on. "Fix this" and "check whether this still works" are different instructions,
// and a suffix on a sentence that already said "fix" reads as the first one.
func SignatureUnverifiedMessage(calleeName, callerID string) string {
	return "function " + calleeName + " changed, check if caller " + callerID + " still supports it"
}

// WarningLabel is the bracketed tag a warning prints under, on every surface. A transient is
// marked in the tag rather than in the prose so it survives truncation and reads the same in
// a summary line and on an annotated line of source.
func WarningLabel(w TopologyWarning) string {
	if w.Transient {
		return string(w.Kind) + " | UNVERIFIED"
	}
	return string(w.Kind)
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
// loop picks up. See warnread.Section.
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
