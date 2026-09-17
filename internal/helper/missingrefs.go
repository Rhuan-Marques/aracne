package helper

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// MissingRefsConn is the private connection kind a scanner records on a caller for every reference
// it could BIND to project code but could not RESOLVE: `from a import fun2` where a.py is in the
// project and declares no fun2, `import { fun2 } from './a'`, `use crate::a::fun2`, `new A().fun2()`
// on a project class A. The target is the id the reference expects.
//
// WHY A RECORD AND NOT A WARNING. A scanner resolves one file against the graph as it stands at
// that moment, and a scan changes several files: the callee may be added by a file parsed later in
// the same batch. Only the finished graph can say whether the reference is broken, so scanners
// record what they expected and SyncMissingReferenceWarnings judges it once, at the end.
//
// A target ending in "(" names a method by owner and name without its parameter list (Java ids
// carry one, and a call site does not know it): it exists if any resource id starts with it.
//
// References that bind to nothing in the project -- a builtin, a third-party package, a dynamic
// attribute -- are never recorded: there is no expectation to break. Go keeps its own resolver-side
// use_missing_node and records none of these.
const MissingRefsConn = domain.MissingRefsConn

// SyncMissingReferenceWarnings makes the use_missing_node warnings of every recording language match
// the graph: one warning per recorded target that does not exist, none for a target that does, and
// none for a reference the caller no longer makes. It returns the warnings it raised that were not
// already standing, for the scan to report.
//
// Warnings whose caller is gone are left to CleanupOrphanedWarnings, and Go's own are left alone:
// a Go resource never carries MissingRefsConn, so a Go warning is never judged here.
func SyncMissingReferenceWarnings(topo *domain.Topology) []domain.TopologyWarning {
	if topo == nil {
		return nil
	}
	if topo.Warnings == nil {
		topo.Warnings = make(map[string]domain.TopologyWarning)
	}
	var prefixes []string
	exists := func(target string) bool {
		if !strings.HasSuffix(target, "(") {
			_, ok := topo.Resources[target]
			return ok
		}
		if prefixes == nil {
			prefixes = make([]string, 0, len(topo.Resources))
			for id := range topo.Resources {
				prefixes = append(prefixes, id)
			}
			sort.Strings(prefixes)
		}
		i := sort.SearchStrings(prefixes, target)
		return i < len(prefixes) && strings.HasPrefix(prefixes[i], target)
	}

	wanted := make(map[string]domain.TopologyWarning)
	recorders := make(map[string]bool)
	for id, res := range topo.Resources {
		targets := res.Connections[MissingRefsConn]
		if len(targets) == 0 {
			continue
		}
		recorders[id] = true
		for _, target := range targets {
			if target == "" || exists(target) {
				continue
			}
			w := domain.TopologyWarning{
				ID:       id + "@" + string(domain.WarnUseMissingNode) + "@" + target,
				SourceID: id,
				Kind:     domain.WarnUseMissingNode,
				TargetID: target,
				Message:  fmt.Sprintf("%s references %s, which does not exist", id, strings.TrimSuffix(target, "(")),
			}
			wanted[w.ID] = w
		}
	}

	// Retire what no longer holds. Only warnings of callers that record references are judged:
	// anything else is Go's, or belongs to a caller whose file was not part of any recording scan.
	for wid, w := range topo.Warnings {
		if w.Kind != domain.WarnUseMissingNode {
			continue
		}
		res, alive := topo.Resources[w.SourceID]
		if !alive || res.Language == "go" {
			continue
		}
		if _, still := wanted[wid]; still {
			continue
		}
		if recorders[w.SourceID] || (w.TargetID != "" && exists(w.TargetID)) {
			delete(topo.Warnings, wid)
			continue
		}
		// A caller that records nothing any more stopped making the reference.
		if _, generic := res.Connections[MissingRefsConn]; !generic && strings.HasSuffix(w.Message, "which does not exist") {
			delete(topo.Warnings, wid)
		}
	}

	var raised []domain.TopologyWarning
	for wid, w := range wanted {
		if _, standing := topo.Warnings[wid]; standing {
			continue
		}
		topo.Warnings[wid] = w
		raised = append(raised, w)
	}
	domain.SortWarnings(raised)
	return raised
}
