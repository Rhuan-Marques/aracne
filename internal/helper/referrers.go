package helper

import (
	"fmt"

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

// ClearReferrerWarningsForFile drops node_removed warnings attributed to a
// resource that lives in path. That file was just re-parsed from source, so
// whatever it references now is authoritative: a stale reference that survived
// the edit is re-emitted by the referrer pass in this same update, and one that
// did not survive should stop being reported.
//
// This is goscanner's clearReanalyzedFunctionWarnings generalized from one
// function to one file, and from Go to every language.
func ClearReferrerWarningsForFile(topo *domain.Topology, path string) {
	if path == "" {
		return
	}
	for id, w := range topo.Warnings {
		if w.Kind != domain.WarnNodeRemoved {
			continue
		}
		res, ok := topo.Resources[w.SourceID]
		if ok && res.Location.Path == path {
			delete(topo.Warnings, id)
		}
	}
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
