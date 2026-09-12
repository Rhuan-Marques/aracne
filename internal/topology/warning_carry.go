package topology

import (
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// carryForwardWarnings moves the warnings a rebuilt graph must not forget onto the new
// topology, and then makes the NEW graph re-judge every one of them.
//
// A full rescan builds its topology from the source tree alone, so the warnings table it
// wrote was whatever this scan happened to raise -- which, for a scan that re-parses
// everything at once, is nothing. Every outstanding signature_changed, node_removed and
// use_missing_node therefore vanished while the call that caused it sat untouched on disk.
// That is not a corner: `arac scan --all` reaches it, so does a relocated project, so does any
// edit to go.mod / Cargo.toml / pom.xml / build.gradle (which sends an ordinary incremental
// scan down this path), and under `scan.pre_tool: "full"` it runs before EVERY tool call.
// docs/architecture.md promises the opposite -- these warnings stand while the cause stands,
// and surface through warnings_list.
//
// interface_conflict is deliberately NOT carried: it is derived from current state by
// syncInterfaceConflicts on this same path, so copying it would risk a stale duplicate of a
// row the rebuild recomputes anyway.
//
// Carrying is only half the contract. A caller fixed since the last scan, a symbol put back,
// a signature reverted -- each must retire its warning, or a rescan would preserve rows
// forever. So the carried rows are put through the same four re-judgements the incremental
// path uses, against the new graph: orphans dropped, restored symbols resolved, recorded call
// sites re-matched, reverted signatures discharged.
func carryForwardWarnings(oldTopo, newTopo *domain.Topology, aliases map[string]string) {
	if oldTopo == nil || newTopo == nil || len(oldTopo.Warnings) == 0 {
		return
	}
	if newTopo.Warnings == nil {
		newTopo.Warnings = map[string]domain.TopologyWarning{}
	}
	remap := func(id string) string {
		if to, ok := aliases[id]; ok && to != "" {
			return to
		}
		return id
	}

	for _, w := range oldTopo.Warnings {
		switch w.Kind {
		case domain.WarnSignatureChanged, domain.WarnNodeRemoved, domain.WarnUseMissingNode:
		default:
			continue
		}
		oldSource, oldTarget := w.SourceID, w.TargetID
		w.SourceID, w.TargetID = remap(oldSource), remap(oldTarget)
		// The id is built from the endpoints (see helper/referrers.go), so a remapped
		// endpoint has to move the key with it, or the next scan raises the same warning
		// under the new id and the old row lingers beside it.
		if oldSource != "" && w.SourceID != oldSource {
			w.ID = strings.ReplaceAll(w.ID, oldSource, w.SourceID)
		}
		if oldTarget != "" && w.TargetID != oldTarget {
			w.ID = strings.ReplaceAll(w.ID, oldTarget, w.TargetID)
		}
		if w.ID == "" {
			continue
		}
		// The subject has to still exist for the warning to mean anything; the re-judgement
		// below owns the rest of the retirement rules.
		if _, ok := newTopo.Resources[w.SourceID]; !ok {
			continue
		}
		// A use_missing_node whose target is now in the graph is answered: the symbol the
		// referrer wanted exists. (node_removed gets the same treatment, plus its edge back,
		// from ResolveReferrerWarnings.)
		if w.Kind == domain.WarnUseMissingNode && w.TargetID != "" {
			if _, ok := newTopo.Resources[w.TargetID]; ok {
				continue
			}
		}
		if _, taken := newTopo.Warnings[w.ID]; taken {
			continue
		}
		newTopo.Warnings[w.ID] = w
	}

	helper.CleanupOrphanedWarnings(newTopo)
	helper.ResolveReferrerWarnings(newTopo)
	helper.ReconcileSignatureWarnings(newTopo)
	helper.DischargeSignatureWarnings(newTopo)
}
