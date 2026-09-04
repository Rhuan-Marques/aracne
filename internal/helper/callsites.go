package helper

import (
	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// ReconcileSignatureWarnings withdraws every signature_changed warning whose caller
// demonstrably still fits the callee, and reports how many it withdrew.
//
// This is the authoritative half of the signature-warning lifecycle; everything else in
// this file's neighbourhood is a fallback for when it cannot answer. The warning names a
// callee (SourceID) and a caller to go verify (TargetID), and until now nothing could check
// that pair -- so the question was approximated twice over, and both approximations are
// wrong in ordinary situations:
//
//   - "the callee went back to its old signature" (Baseline) is a proxy for what the caller
//     expects. It goes stale the moment the caller moves on its own: take fun1 through
//     (x) -> (x,y), fix fun2 to pass three arguments, then widen fun1 to (x,y,z), and the
//     code compiles while the proxy still says the caller was written against (x).
//   - "the caller's file was re-parsed" is a proxy for the caller being fixed. Adding a
//     comment to the caller satisfies it exactly as well as correcting the call.
//
// Comparing the recorded call against the current signature answers the real question, so a
// revert, a fix and an unrelated edit all fall out of one comparison.
//
// Silence is deliberate in three cases, and each leaves the warning standing rather than
// removing it on a guess: no recorded call (an edge added outside body resolution, or a
// caller last parsed by an older build), a callee or caller missing from the graph, and an
// Unknown verdict (JavaScript, or an argument whose type could not be read). Those are
// exactly the cases the Baseline rule still covers.
func ReconcileSignatureWarnings(topo *domain.Topology) int {
	if topo == nil || len(topo.Warnings) == 0 {
		return 0
	}
	env := contract.Env{Lookup: func(id string) (domain.Resource, bool) {
		res, ok := topo.Resources[id]
		return res, ok
	}}
	withdrawn := 0
	for id, w := range topo.Warnings {
		if !signatureWarningSatisfied(topo, env, w) {
			continue
		}
		delete(topo.Warnings, id)
		withdrawn++
	}
	return withdrawn
}

// signatureWarningSatisfied reports whether every recorded call from this warning's caller
// to its callee currently fits. False whenever the answer is not a confident yes.
func signatureWarningSatisfied(topo *domain.Topology, env contract.Env, w domain.TopologyWarning) bool {
	if w.Kind != domain.WarnSignatureChanged || w.TargetID == "" {
		return false
	}
	callee, ok := topo.Resources[w.SourceID]
	if !ok {
		return false // CleanupOrphanedWarnings owns the vanished-callee case
	}
	caller, ok := topo.Resources[w.TargetID]
	if !ok {
		return false
	}
	matcher := contract.For(callee.Language)
	if matcher == nil {
		return false // a language with no rules must not start withdrawing warnings
	}
	sites := contract.CallSitesOf(caller, w.SourceID)
	if len(sites) == 0 {
		return false
	}
	for _, site := range sites {
		if v, _ := matcher.Match(env, callee, site); v != contract.Match {
			return false
		}
	}
	return true
}

// ReconcileSignatureWarningsScoped is the partial fast path's counterpart to
// ReconcileSignatureWarnings, which cannot run there: that path holds only the edited
// file's resources, so the callee a warning names is usually absent and every warning would
// look unresolvable.
//
// It reads exactly the endpoints its warnings name and nothing else. This matters more than
// it looks: the fast path is the ordinary single-file edit, so it is where an agent's fix
// actually lands, and a rule that only runs on the slow path is a rule the agent almost
// never sees.
func ReconcileSignatureWarningsScoped(dbPath string, warnings map[string]domain.TopologyWarning, working []domain.Resource) int {
	if len(warnings) == 0 {
		return 0
	}
	resources := make(map[string]domain.Resource, len(working))
	for _, r := range working {
		resources[r.ID] = r
	}
	var missing []string
	for _, w := range warnings {
		if w.Kind != domain.WarnSignatureChanged || w.TargetID == "" {
			continue
		}
		for _, id := range []string{w.SourceID, w.TargetID} {
			if _, have := resources[id]; !have {
				missing = append(missing, id)
			}
		}
	}
	if len(missing) > 0 {
		fetched, err := ReadResourcesByIDs(dbPath, missing)
		if err != nil {
			return 0 // cannot answer; leave every warning standing
		}
		for id, r := range fetched {
			if _, have := resources[id]; !have {
				resources[id] = r
			}
		}
	}

	topo := &domain.Topology{Resources: resources, Warnings: warnings}
	env := contract.Env{Lookup: func(id string) (domain.Resource, bool) {
		res, ok := resources[id]
		return res, ok
	}}
	withdrawn := 0
	for id, w := range warnings {
		if !signatureWarningSatisfied(topo, env, w) {
			continue
		}
		delete(warnings, id)
		withdrawn++
	}
	return withdrawn
}

// CallerStillFits reports whether a re-parsed caller's calls to calleeID all fit.
//
// Used to tighten the caller-side clear, which discharges a warning when the caller's FILE
// is re-parsed -- a rule that cannot tell a fix from a comment. The third return value says
// whether the question could be answered at all; when it could not, the caller keeps the
// old permissive behaviour rather than holding a warning it has no evidence for.
func CallerStillFits(topo *domain.Topology, callerID, calleeID string) (fits bool, known bool) {
	callee, ok := topo.Resources[calleeID]
	if !ok {
		return false, false
	}
	caller, ok := topo.Resources[callerID]
	if !ok {
		return false, false
	}
	matcher := contract.For(callee.Language)
	if matcher == nil {
		return false, false
	}
	sites := contract.CallSitesOf(caller, calleeID)
	if len(sites) == 0 {
		return false, false
	}
	env := contract.Env{Lookup: func(id string) (domain.Resource, bool) {
		res, ok := topo.Resources[id]
		return res, ok
	}}
	for _, site := range sites {
		switch v, _ := matcher.Match(env, callee, site); v {
		case contract.Mismatch:
			return false, true
		case contract.Unknown:
			return false, false
		}
	}
	return true, true
}
