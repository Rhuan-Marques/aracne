package helper

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"aracne/internal/topology/domain"
)

// ReferenceConnTypes are the body-resolution edge kinds that mean "this
// resource references that one". Losing one of these is a real "go verify this
// caller" signal, so it earns a node_removed warning.
//
// Deliberately excluded: ownership edges (has_function/has_struct/...), which
// only say a container held the symbol; structural reverse edges
// (implements/implemented_by/inherits/inherited_by/methods/constructor), which
// every scanner recomputes globally on each pass; and package/dependency edges,
// which do not point at a symbol at all.
var ReferenceConnTypes = map[string]bool{
	"calls":           true,
	"uses_struct":     true,
	"uses_class":      true,
	"uses_named_type": true,
	"uses_interface":  true,
	"uses_extvar":     true,
}

// ReferrerScan parameterizes ScanReferrers.
type ReferrerScan struct {
	// Removed is the set of resource ids that existed before this update and
	// are gone from topo.Resources now.
	Removed map[string]bool
	// Origin names the file (or file id) the symbols disappeared from; it only
	// appears in the warning message.
	Origin string
	// ConnTypes limits which edge kinds count as a reference. nil means every
	// edge kind, which is what whole-file removal wants.
	ConnTypes map[string]bool
	// SkipSource suppresses warnings for referrers the caller has already
	// handled — normally the resources of the file that was just re-parsed,
	// since those were re-resolved from source in this same update. nil means
	// warn for every surviving referrer.
	SkipSource func(id string) bool
	// Strip also removes the dangling edge from the referrer.
	//
	// Only set this where ResolveReferrerWarnings runs afterwards. Stripping an
	// edge with no way to put it back is worse than leaving it: the referrer's
	// own file is not re-parsed when the target returns, and that edge is the
	// only record that would tell reverseCallerFiles to re-resolve it, so it
	// would stay lost until a cold `scan --hard`.
	Strip bool
}

// ScanReferrers emits one caller-attributed node_removed warning per surviving
// resource that still points at a removed id.
//
// Attribution is the whole point. A warning whose SourceID is the symbol that
// just disappeared is deleted by CleanupOrphanedWarnings before it can reach
// the database, and tells an agent nothing it can act on; a warning attributed
// to the surviving *referrer* both persists and names the code that now needs
// checking. goscanner has always done this for Go via its reverse-caller index;
// this is the same thing computed from the graph, so it works for every
// language. The id scheme (src@kind@tgt) is goscanner's, so when both emit for
// the same pair they collapse onto one map key.
func ScanReferrers(topo *domain.Topology, opt ReferrerScan) []domain.TopologyWarning {
	if len(opt.Removed) == 0 {
		return nil
	}
	var warnings []domain.TopologyWarning
	for _, res := range topo.Resources {
		if opt.Removed[res.ID] {
			continue
		}
		if opt.SkipSource != nil && opt.SkipSource(res.ID) {
			continue
		}
		stripped := false
		for connType, targets := range res.Connections {
			if opt.ConnTypes != nil && !opt.ConnTypes[connType] {
				continue
			}
			var kept []string
			for _, target := range targets {
				if !opt.Removed[target] {
					if opt.Strip {
						kept = append(kept, target)
					}
					continue
				}
				msg := fmt.Sprintf("%s was removed, verify %s which references it via %s",
					target, res.ID, connType)
				if opt.Origin != "" {
					msg = fmt.Sprintf("%s was removed from %s, verify %s which references it via %s",
						target, opt.Origin, res.ID, connType)
				}
				warnings = append(warnings, domain.TopologyWarning{
					ID:       res.ID + "@" + string(domain.WarnNodeRemoved) + "@" + target,
					SourceID: res.ID,
					Kind:     domain.WarnNodeRemoved,
					TargetID: target,
					Message:  msg,
				})
			}
			if opt.Strip && len(kept) != len(targets) {
				stripped = true
				if len(kept) > 0 {
					res.Connections[connType] = kept
				} else {
					delete(res.Connections, connType)
				}
			}
		}
		if stripped {
			topo.Resources[res.ID] = res
		}
	}
	return warnings
}

// ReferenceConnType returns the body-reference edge kind a referrer written in
// language uses to point at a target of the given kind, or "" when that kind is
// not a reference target. It is the inverse of ReferenceConnTypes and exists so
// a stripped edge can be put back when the target reappears.
//
// The edge vocabulary is aligned across languages by design; the one real
// asymmetry is classes. JS, TS and Python map a class to ResourceStruct but
// reference it with uses_class, while Go, Rust and Java use uses_struct.
func ReferenceConnType(language string, kind domain.ResourceKind) string {
	switch kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		return "calls"
	case domain.ResourceStruct:
		switch language {
		case "javascript", "typescript", "python":
			return "uses_class"
		default:
			return "uses_struct"
		}
	case domain.ResourceInterface:
		return "uses_interface"
	case domain.ResourceNamedType:
		return "uses_named_type"
	case domain.ResourceVariable:
		return "uses_extvar"
	default:
		return ""
	}
}

// ClearReferrerWarningsForFile drops the warnings that send an agent to a
// resource living in path. That file was just re-parsed from source, so
// whatever it references now is authoritative: a stale reference that survived
// the edit is re-emitted by the referrer pass in this same update, and one that
// did not survive should stop being reported.
//
// The two kinds that qualify point opposite ways round. node_removed is
// attributed to the referrer through SourceID, while signature_changed keeps
// the changed symbol as SourceID and names the caller to verify in TargetID
// (goscanner's shape; see ExpandSignatureWarnings).
//
// This is goscanner's clearReanalyzedFunctionWarnings generalized from one
// function to one file, and from Go to every language.
func ClearReferrerWarningsForFile(topo *domain.Topology, path string) {
	if path == "" {
		return
	}
	inFile := func(id string) bool {
		res, ok := topo.Resources[id]
		return ok && res.Location.Path == path
	}
	for id, w := range topo.Warnings {
		switch w.Kind {
		case domain.WarnNodeRemoved:
			if inFile(w.SourceID) {
				delete(topo.Warnings, id)
			}
		case domain.WarnSignatureChanged:
			if inFile(w.TargetID) {
				delete(topo.Warnings, id)
			}
		}
	}
}

// ExpandSignatureWarnings rewrites every self-attributed signature_changed
// warning -- SourceID naming the symbol whose signature moved, TargetID empty --
// into one caller-attributed warning per surviving referrer.
//
// The self-attributed shape can never be cleared. The symbol it names still
// exists, so CleanupOrphanedWarnings keeps it; and with no caller in TargetID
// neither ClearReferrerWarningsForFile nor goscanner's
// clearReanalyzedFunctionWarnings has anything to key on. Fixing every caller
// left the row in the database forever. Naming the caller is also the only form
// an agent can act on, which is the same reason node_removed was moved off this
// shape and onto the referrer pass.
//
// Warnings that already name a caller -- goscanner builds the final shape
// itself -- and every other kind pass through untouched. skipPaths are the files
// re-parsed in this same update: a referrer living in one of them was just
// re-resolved from source, so warning about it would duplicate what the scanner
// already reported. A changed symbol whose referrers have all gone yields no
// warning at all; there is nothing left to verify, which is what goscanner does
// by only emitting inside its caller loop.
func ExpandSignatureWarnings(topo *domain.Topology, ws []domain.TopologyWarning, skipPaths map[string]bool) []domain.TopologyWarning {
	changed := make(map[string]bool)
	for _, w := range ws {
		if w.Kind == domain.WarnSignatureChanged && w.TargetID == "" {
			changed[w.SourceID] = true
		}
	}
	// The overwhelmingly common update changes no signature at all, and the walk
	// below is over every resource in the repo. Do not pay for it needlessly.
	if len(changed) == 0 {
		return ws
	}

	referrers := make(map[string][]string, len(changed))
	for _, res := range topo.Resources {
		if skipPaths[res.Location.Path] {
			continue
		}
		// A referrer reaching the same symbol through two edge kinds (calls and
		// uses_struct, say) is still one thing to verify, so record it once per
		// changed symbol rather than once per edge.
		var recorded map[string]bool
		for connType, targets := range res.Connections {
			if !ReferenceConnTypes[connType] {
				continue
			}
			for _, target := range targets {
				if !changed[target] || recorded[target] {
					continue
				}
				if recorded == nil {
					recorded = make(map[string]bool, len(changed))
				}
				recorded[target] = true
				referrers[target] = append(referrers[target], res.ID)
			}
		}
	}
	for _, ids := range referrers {
		sort.Strings(ids)
	}

	out := make([]domain.TopologyWarning, 0, len(ws))
	for _, w := range ws {
		if w.Kind != domain.WarnSignatureChanged || w.TargetID != "" {
			out = append(out, w)
			continue
		}
		name := w.SourceID
		if res, ok := topo.Resources[w.SourceID]; ok && res.Name != "" {
			name = res.Name
		}
		for _, callerID := range referrers[w.SourceID] {
			out = append(out, domain.TopologyWarning{
				// goscanner's id, so the two producers collapse onto one key.
				ID:       callerID + "@" + string(domain.WarnSignatureChanged) + "@" + w.SourceID,
				SourceID: w.SourceID,
				Kind:     domain.WarnSignatureChanged,
				TargetID: callerID,
				Message:  fmt.Sprintf("%s changed signature, verify caller %s", name, callerID),
			})
		}
	}
	return out
}

// ResolveReferrerWarnings drops node_removed warnings whose target is back in
// the graph. Restoring a symbol someone deleted has to clear the warning about
// deleting it, or the channel fills with permanently stale entries and agents
// learn to ignore it.
// It also puts back the reference edge that ScanReferrers stripped, mirroring
// goscanner's resolveUseMissingWarning. The referrer's own file is not re-parsed
// when the target comes back, so nothing else would ever restore it.
func ResolveReferrerWarnings(topo *domain.Topology) {
	for id, w := range topo.Warnings {
		if w.Kind != domain.WarnNodeRemoved || w.TargetID == "" {
			continue
		}
		target, exists := topo.Resources[w.TargetID]
		if !exists {
			continue
		}
		delete(topo.Warnings, id)

		referrer, ok := topo.Resources[w.SourceID]
		if !ok {
			continue
		}
		connType := ReferenceConnType(referrer.Language, target.Kind)
		if connType == "" {
			continue
		}
		if referrer.Connections == nil {
			referrer.Connections = map[string][]string{}
		}
		for _, existing := range referrer.Connections[connType] {
			if existing == w.TargetID {
				connType = ""
				break
			}
		}
		if connType == "" {
			continue
		}
		referrer.Connections[connType] = append(referrer.Connections[connType], w.TargetID)
		topo.Resources[w.SourceID] = referrer
	}
}

// SignatureBaseline fingerprints the part of a resource that a CALLER is written
// against: its name and its declared input/output types.
//
// Language-agnostic on purpose. Every scanner already lands its typed parameter
// and result lists in Properties["input"]/["output"], so the shape a caller
// depends on is readable from the domain resource without asking the scanner
// that produced it. That is what lets one rule discharge Go, JavaScript,
// TypeScript, Python and Rust warnings instead of five near-copies.
//
// The value is CANONICAL, not just deterministic, and that is the whole
// difficulty. The same function reaches this code in two different Go shapes: a
// freshly parsed resource carries typed structs, while one loaded back from
// SQLite carries the JSON round-trip of those structs -- []any of
// map[string]any. json.Marshal writes a struct in field order and a map in
// sorted-key order, so marshalling the raw value would give the same function
// two different fingerprints depending on where it came from, and a baseline
// stored from one shape could never match a signature computed from the other.
// Re-marshalling through `any` puts both through the map path, so the
// comparison is between signatures rather than between provenances.
func SignatureBaseline(res domain.Resource) string {
	var b strings.Builder
	b.WriteString(res.Name)
	b.WriteByte('|')
	b.WriteString(canonicalJSON(res.Properties["input"]))
	b.WriteByte('|')
	b.WriteString(canonicalJSON(res.Properties["output"]))
	return b.String()
}

// canonicalJSON renders v the same way whichever Go shape it arrives in.
//
// "No parameters" has four spellings on the way through here -- the key absent,
// a Go nil, a nil slice (which marshals to `null`) and an empty slice (`[]`) --
// and which one a resource carries says only where it was built, not what the
// function looks like. A scanner emits a nil slice while the same resource read
// back from SQLite has the key missing entirely, so leaving them distinct made
// a parameterless function fail to match its own baseline.
func canonicalJSON(v any) string {
	if v == nil {
		return ""
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return string(raw)
	}
	switch g := generic.(type) {
	case nil:
		return ""
	case []any:
		if len(g) == 0 {
			return ""
		}
	case map[string]any:
		if len(g) == 0 {
			return ""
		}
	}
	out, err := json.Marshal(generic)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// StampSignatureBaselines records, on every signature_changed warning that does
// not have one, the signature its subject had BEFORE this update -- which is the
// signature its callers were written against.
//
// `before` is the pre-update resource set. A warning whose subject is not in it
// is left unstamped rather than stamped with the current signature: an empty
// baseline never discharges, which is the old behaviour, whereas a wrong one
// would discharge a warning that is still true.
//
// Warnings that ALREADY carry a baseline keep it. See PreserveSignatureBaselines
// for why that matters.
func StampSignatureBaselines(topo *domain.Topology, before map[string]domain.Resource) {
	for id, w := range topo.Warnings {
		if w.Kind != domain.WarnSignatureChanged || w.Baseline != "" {
			continue
		}
		prev, ok := before[w.SourceID]
		if !ok {
			continue
		}
		base := SignatureBaseline(prev)
		if base == "" {
			continue
		}
		w.Baseline = base
		topo.Warnings[id] = w
	}
}

// RestoreSignatureBaselines puts back a baseline that this update overwrote,
// by re-raising a warning that was already in the table under the same id.
//
// A definition edited twice raises the same src@kind@tgt id twice, and the
// second raise knows only the signature left by the first edit. Letting it win
// would move the target: the callers are still written against the ORIGINAL
// signature, so a definition taken A->B->C and then put back to A has to
// discharge, and it only can if the recorded baseline is still A rather than B.
//
// Keyed on the topology rather than on a warning slice because the producers
// disagree about how they deliver: goscanner assigns into the warnings map
// itself while every other scanner returns a slice the manager merges. Reading
// the finished map covers both, and cannot be bypassed by a scanner that starts
// writing directly tomorrow.
func RestoreSignatureBaselines(topo *domain.Topology, before map[string]domain.TopologyWarning) {
	if len(before) == 0 {
		return
	}
	for id, w := range topo.Warnings {
		if w.Kind != domain.WarnSignatureChanged || w.Baseline != "" {
			continue
		}
		prev, ok := before[id]
		if !ok || prev.Kind != domain.WarnSignatureChanged || prev.Baseline == "" {
			continue
		}
		w.Baseline = prev.Baseline
		topo.Warnings[id] = w
	}
}

// DischargeSignatureWarnings drops every signature_changed warning whose subject
// is back to the signature the warning was raised against, and reports how many
// it dropped.
//
// This is the definition-side counterpart to ClearReferrerWarningsForFile, and
// until it existed there was none: a signature_changed warning could only be
// answered by re-parsing the CALLER or by the caller disappearing. Undoing the
// change answered nothing, so a definition edited and then reverted left one
// warning per caller in the database permanently, and only a full `arac scan
// --all` -- which rebuilds the table from nothing -- cleared them. On a
// widely-called symbol that is hundreds of rows telling an agent to go verify
// callers that were never broken.
func DischargeSignatureWarnings(topo *domain.Topology) int {
	dropped := 0
	for id, w := range topo.Warnings {
		if w.Kind != domain.WarnSignatureChanged || w.Baseline == "" {
			continue
		}
		res, ok := topo.Resources[w.SourceID]
		if !ok {
			continue // CleanupOrphanedWarnings owns the vanished-subject case
		}
		if SignatureBaseline(res) == w.Baseline {
			delete(topo.Warnings, id)
			dropped++
		}
	}
	return dropped
}
