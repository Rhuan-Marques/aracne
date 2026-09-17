package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"sort"
	"strings"

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
func ReconcileSignatureWarnings(topo *domain.Topology) (int, []domain.TopologyWarning) {
	if topo == nil || len(topo.Warnings) == 0 {
		return 0, nil
	}
	env := contract.Env{Lookup: func(id string) (domain.Resource, bool) {
		res, ok := topo.Resources[id]
		return res, ok
	}}
	withdrawn := 0
	var transients []domain.TopologyWarning
	for id, w := range topo.Warnings {
		// callerEdited is false: an edited caller has already been answered, and reported, by
		// ClearReferrerWarningsForFile. What reaches this loop is either a caller nobody edited
		// or one beside a callee that moved in this update, which that clear leaves here.
		ok, why := signatureWarningSatisfied(topo, env, w, false)
		if !ok {
			continue
		}
		if why != "" {
			transients = append(transients, TransientFrom(w, topo.Resources))
		}
		delete(topo.Warnings, id)
		withdrawn++
	}
	domain.SortWarnings(transients)
	return withdrawn, transients
}

// signatureWarningSatisfied reports whether every recorded call from this warning's caller
// to its callee currently fits. False whenever the answer is not a confident yes.
//
// callerEdited says the caller was just rewritten while the callee stood still. That is the
// one event that may retire a warning about a changed return type, which no recorded call can
// judge: it is the same permissive rule ClearReferrerWarningsForFile applies to an edited
// caller on the full path, and without it a caller fixed through the partial path kept its
// warning until the callee was reverted.
func signatureWarningSatisfied(topo *domain.Topology, env contract.Env, w domain.TopologyWarning, callerEdited bool) (satisfied bool, unverifiedWhy string) {
	if w.Kind != domain.WarnSignatureChanged || w.TargetID == "" {
		return false, ""
	}
	callee, ok := topo.Resources[w.SourceID]
	if !ok {
		return false, "" // CleanupOrphanedWarnings owns the vanished-callee case
	}
	// A call site records what the caller PASSES and nothing about what it does with the
	// result, so arguments that still fit are no evidence about a changed return type:
	// `x, _ := lib.Get(1)` fits `Get(a int) int` exactly as well as `Get(a int) (int, error)`.
	// The baseline is the only thing that sees that change, so while it stands the warning
	// does too. Putting the return type back discharges it through the baseline rule.
	if !callerEdited && outputChangedSinceBaseline(w, callee) {
		return false, ""
	}
	caller, ok := topo.Resources[w.TargetID]
	if !ok {
		return false, ""
	}
	matcher := contract.For(callee.Language)
	if matcher == nil {
		return false, "" // a language with no rules must not start withdrawing warnings
	}
	sites := contract.CallSitesOf(caller, w.SourceID)
	if len(sites) == 0 {
		return false, ""
	}
	// The baseline names the shape the caller was written against, so the matcher can skip
	// the positions that did not move. Judging those would manufacture doubt about arguments
	// this edit never touched.
	env.OldParams = BaselineInput(w.Baseline)
	why := ""
	for _, site := range sites {
		switch v, w := matcher.Match(env, callee, site); v {
		case contract.Match:
			// keep looking; every site has to fit
		case contract.Unverified:
			// A changed position whose argument the scanner never typed. Not a break and
			// not a clean bill of health -- the stored warning goes and a transient takes
			// its place, so the agent hears about it exactly once, at the edit.
			if why == "" {
				why = w
			}
		default:
			return false, ""
		}
	}
	return true, why
}

// calleeName is the short name a reader recognises, falling back to the id when the callee
// is not in the set this path loaded.
func calleeName(resources map[string]domain.Resource, id string) string {
	if res, ok := resources[id]; ok && res.Name != "" {
		return res.Name
	}
	return id
}

// TransientFrom turns a withdrawn warning into the unverified report that replaces it.
//
// Same endpoints and same id as the warning it stands in for, so a surface that dedupes by id
// still does -- but Transient, so nothing writes it down, and carrying the other sentence.
//
// The matcher's own explanation ("s is now []byte and this call's argument could not be read")
// is deliberately NOT appended. It restates what the reader is already looking at -- the line
// is annotated in place and the new signature is in the same report -- and this report is
// emitted on the agent's critical path after every edit, where a second clause costs more
// than it tells.
//
// resources is whatever set the calling path loaded; it names the callee for the message and
// fingerprints both endpoints into State (see TransientState).
func TransientFrom(w domain.TopologyWarning, resources map[string]domain.Resource) domain.TopologyWarning {
	w.Message = domain.SignatureUnverifiedMessage(calleeName(resources, w.SourceID), w.TargetID)
	w.Transient = true
	w.State = TransientState(w, resources)
	return w
}

// TransientState fingerprints the situation a transient describes, in two halves joined by
// "." -- the callee's signature now, and what the caller does now: its recorded calls to that
// callee and its normalised body.
//
// WHY A TRANSIENT NEEDS ONE. A transient is reported once, and "once" has to survive the same
// finding being raised again. It is raised whenever a scan re-judges the pair, and a scan re-
// judges the pair whenever the files move -- including when they move and come straight back:
// `git stash && go test ...; git stash pop` reverted every edited file and restored it, the
// next scan saw every signature change again, and the agent was re-sent the whole batch of
// reports it had already read. The id alone cannot tell that apart from a genuinely new
// question, because the id is only the two endpoints. The state can.
//
// The halves are separate so the ledger can follow the agent's own edits to a caller; see
// RefreshTransientCallers. The body hash is the normalised one, so a comment or a blank line in
// the caller is not a new question.
func TransientState(w domain.TopologyWarning, resources map[string]domain.Resource) string {
	return transientCalleeState(w, resources) + "." + transientCallerState(w, resources)
}

// transientCalleeState is the callee half of TransientState: the callee's signature now.
//
// NOT the baseline. The baseline is the signature the warning was raised against, and that
// depends on the path the code took to get here, not on where it is: `int` reached from
// `[]byte` and `int` reached from a revert to `string` are one callee and one question, and
// keying on the baseline reported the second as new.
func transientCalleeState(w domain.TopologyWarning, resources map[string]domain.Resource) string {
	h := sha256.New()
	if callee, ok := resources[w.SourceID]; ok {
		writeStatePart(h, SignatureBaseline(callee))
	} else {
		writeStatePart(h, "\x00missing")
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// transientCallerState is the caller half: its normalised body and its recorded calls to the
// callee.
func transientCallerState(w domain.TopologyWarning, resources map[string]domain.Resource) string {
	h := sha256.New()
	caller, ok := resources[w.TargetID]
	if !ok {
		writeStatePart(h, "\x00missing")
		return hex.EncodeToString(h.Sum(nil)[:12])
	}
	writeStatePart(h, caller.NormHash)
	var calls []string
	for _, rec := range caller.Connections[contract.CallSitesConn] {
		if site, ok := contract.DecodeCallSite(rec); ok && site.CalleeID == w.SourceID {
			calls = append(calls, rec)
		}
	}
	sort.Strings(calls)
	for _, rec := range calls {
		writeStatePart(h, rec)
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

func writeStatePart(h hash.Hash, s string) {
	h.Write([]byte(s))
	h.Write([]byte{0})
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
func ReconcileSignatureWarningsScoped(dbPath string, warnings map[string]domain.TopologyWarning, working []domain.Resource) (int, []domain.TopologyWarning) {
	if len(warnings) == 0 {
		return 0, nil
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
			return 0, nil // cannot answer; leave every warning standing
		}
		for id, r := range fetched {
			if _, have := resources[id]; !have {
				resources[id] = r
			}
		}
	}

	// The working set is what this update re-parsed, and the database still holds each row's
	// previous version. A caller in the working set was edited -- unless its callee's signature
	// moved in the same update, which is what raised the warning in the first place.
	reparsed := make(map[string]bool, len(working))
	for _, r := range working {
		reparsed[r.ID] = true
	}
	// The previous version of every re-parsed endpoint: callees, to tell whether their signature
	// moved in this update, and callers, to tell whether they existed before it and what their
	// body was. Anything outside the working set is unchanged by this update, so its row is
	// both versions at once.
	var prevCandidates []string
	for _, w := range warnings {
		if w.Kind != domain.WarnSignatureChanged {
			continue
		}
		for _, id := range []string{w.SourceID, w.TargetID} {
			if id != "" && reparsed[id] {
				prevCandidates = append(prevCandidates, id)
			}
		}
	}
	previous := map[string]domain.Resource{}
	previousKnown := true
	if len(prevCandidates) > 0 {
		if fetched, err := ReadResourcesByIDs(dbPath, prevCandidates); err == nil {
			previous = fetched
		} else {
			previousKnown = false
		}
	}
	calleeMoved := func(id string) bool {
		if !reparsed[id] {
			return false
		}
		prev, ok := previous[id]
		return !ok || SignatureBaseline(prev) != SignatureBaseline(resources[id])
	}
	priorCaller := func(id string) (domain.Resource, bool) {
		if !reparsed[id] {
			return resources[id], true
		}
		prev, ok := previous[id]
		return prev, ok
	}

	topo := &domain.Topology{Resources: resources, Warnings: warnings}
	env := contract.Env{Lookup: func(id string) (domain.Resource, bool) {
		res, ok := resources[id]
		return res, ok
	}}
	withdrawn := 0
	var transients []domain.TopologyWarning
	for id, w := range warnings {
		if previousKnown && w.Kind == domain.WarnSignatureChanged && w.TargetID != "" {
			prev, existed := priorCaller(w.TargetID)
			if staleSignatureCaller(topo, w, prev, existed, calleeMoved(w.SourceID)) {
				delete(warnings, id)
				withdrawn++
				continue
			}
		}
		callerEdited := reparsed[w.TargetID] && !calleeMoved(w.SourceID)
		ok, why := signatureWarningSatisfied(topo, env, w, callerEdited)
		if !ok {
			continue
		}
		if why != "" {
			transients = append(transients, TransientFrom(w, resources))
		}
		delete(warnings, id)
		withdrawn++
	}
	domain.SortWarnings(transients)
	return withdrawn, transients
}

// RetireStaleSignatureCallers is the full-graph form of the caller rules in
// staleSignatureCaller, for the paths that hold the whole pre-update graph. before is that
// graph; it reports how many warnings it withdrew.
func RetireStaleSignatureCallers(topo *domain.Topology, before map[string]domain.Resource) int {
	if topo == nil || len(topo.Warnings) == 0 || before == nil {
		return 0
	}
	withdrawn := 0
	for id, w := range topo.Warnings {
		if w.Kind != domain.WarnSignatureChanged || w.TargetID == "" {
			continue
		}
		calleeMoved := false
		if prev, ok := before[w.SourceID]; ok {
			if now, ok := topo.Resources[w.SourceID]; ok {
				calleeMoved = SignatureBaseline(prev) != SignatureBaseline(now)
			}
		}
		prev, existed := before[w.TargetID]
		if staleSignatureCaller(topo, w, prev, existed, calleeMoved) {
			delete(topo.Warnings, id)
			withdrawn++
		}
	}
	return withdrawn
}

// staleSignatureCaller reports whether a signature_changed warning names a caller it cannot be
// about any more -- decided from the caller as the update left it (topo) and as it was before
// (prev, existed).
//
// The warning is raised against whoever called the callee BEFORE the update, or whoever
// references it after, and the update that changed the callee can also have changed the
// callers. Three cases, each a question a cold scan would never ask:
//
//   - The caller no longer calls the callee: the call moved (into a helper written in the same
//     edit) or went away. There is nothing left in it to verify.
//   - The caller did not exist before the update: it was written against the new signature.
//   - The caller's body was rewritten in the same update that changed the callee. The Baseline
//     rule assumes callers were written against the old signature, which is exactly what this
//     one no longer is; so only the recorded calls decide, and a warning stands only where one
//     demonstrably does not fit. That is the rule ClearReferrerWarningsForFile applies to a
//     caller fixed after the fact, applied to one fixed in the same breath. A comment-only
//     edit is not a rewrite -- the body hash ignores comments -- so it keeps the warning.
//
// An unchanged caller of a changed callee matches none of these and keeps its warning, even
// when only the return type moved.
func staleSignatureCaller(topo *domain.Topology, w domain.TopologyWarning, prev domain.Resource, existed, calleeMoved bool) bool {
	caller, ok := topo.Resources[w.TargetID]
	if !ok {
		return false // CleanupOrphanedWarnings owns the vanished-caller case
	}
	calls := false
	for _, target := range caller.Connections["calls"] {
		if target == w.SourceID {
			calls = true
			break
		}
	}
	if !calls || !existed {
		return true
	}
	if !calleeMoved || prev.NormHash == "" || caller.NormHash == "" || prev.NormHash == caller.NormHash {
		return false
	}
	fits, known := CallerStillFits(topo, w.TargetID, w.SourceID)
	return !known || fits
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
		// Unverified joins Unknown deliberately: this reports to the CALLER-SIDE clear,
		// whose rule is "hold the warning only on a demonstrable mismatch". An unreadable
		// argument is not one. The clear raises the transient for it itself, through
		// signatureWarningSatisfied, so the report carries the baseline this answer lacks.
		case contract.Unknown, contract.Unverified:
			return false, false
		}
	}
	return true, true
}

// callSiteNamesAny reports whether an edge is a `__call_sites` record whose callee is in ids.
// The callee id is the head of the edge's value (see contract.EncodeCallSite), so an edge
// sweep comparing whole targets against removed ids never matches one.
func callSiteNamesAny(connType, target string, ids map[string]bool) bool {
	if connType != contract.CallSitesConn {
		return false
	}
	i := strings.Index(target, contract.RecordSep)
	return i > 0 && ids[target[:i]]
}
