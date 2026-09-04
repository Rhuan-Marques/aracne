package contract

import (
	"encoding/json"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// CallSitesConn is the connection kind under which a caller stores what it passes to each
// callee.
//
// A private "__"-prefixed kind holding composite data in the target id, copying
// rustscanner's __impl_records and javascanner's __extends_records verbatim. That shape was
// chosen there for the same reason it is chosen here: it round-trips through
// FromGeneric/ToGeneric, through all three database writers and through hydration with no
// code aware of it, because target_id has no foreign key and the mappers never inspect a
// connection's value.
//
// Two consequences are load-bearing rather than incidental:
//
//   - resourceSignature folds every connection into the fingerprint DiffResources compares,
//     so a call whose SHAPE changed marks its caller dirty and gets rewritten. A record kept
//     anywhere else -- a side table, a Properties key -- would go stale silently, because
//     nothing else about the caller changed.
//   - the per-source DELETE-then-reinsert that every upsert already performs rebuilds a
//     caller's records wholesale, so there is no separate invalidation to get wrong.
//
// It cannot reach the model: domain.outgoingKeys is an allow-list, and OutgoingNeighbors
// drops targets that are not resources, which an encoded record never is.
const CallSitesConn = "__call_sites"

// RecordSep separates the callee id from its payload. Resource ids cannot contain it in any
// language's grammar -- Go's "pkg/path.Name", Rust's "crate::mod::T::m", Java's
// "com.x.C.m(int,int)", Python and JS module paths -- which is the same argument the two
// existing record kinds rely on.
const RecordSep = "=>>"

// payload is the part of a record that is not the callee id. Marshalled from a struct, so
// field order is fixed and the same call always encodes to the same bytes -- which matters
// because the at-scale suite compares connection sets byte-for-byte across scan modes.
type payload struct {
	N        int       `json:"n"`
	Types    []*string `json:"t,omitempty"`
	Variadic bool      `json:"v,omitempty"`
	Kwargs   []string  `json:"k,omitempty"`
	Dyn      bool      `json:"d,omitempty"`
}

// EncodeCallSite renders one recorded call. An empty callee id yields "", which callers
// treat as "nothing to record".
func EncodeCallSite(s CallSite) string {
	if s.CalleeID == "" {
		return ""
	}
	raw, err := json.Marshal(payload{N: s.N, Types: s.Types, Variadic: s.Variadic, Kwargs: s.Kwargs, Dyn: s.Dyn})
	if err != nil {
		return ""
	}
	return s.CalleeID + RecordSep + string(raw)
}

// DecodeCallSite parses a record back. It reports false for anything it does not recognise,
// so a record written by a newer build, or an unrelated "__"-prefixed edge, is skipped
// rather than misread.
func DecodeCallSite(rec string) (CallSite, bool) {
	i := strings.Index(rec, RecordSep)
	if i <= 0 {
		return CallSite{}, false
	}
	var p payload
	if err := json.Unmarshal([]byte(rec[i+len(RecordSep):]), &p); err != nil {
		return CallSite{}, false
	}
	return CallSite{
		CalleeID: rec[:i],
		N:        p.N,
		Types:    p.Types,
		Variadic: p.Variadic,
		Kwargs:   p.Kwargs,
		Dyn:      p.Dyn,
	}, true
}

// CallSitesOf returns everything a caller recorded, optionally narrowed to one callee.
// Pass "" for calleeID to get them all.
func CallSitesOf(caller domain.Resource, calleeID string) []CallSite {
	recs := caller.Connections[CallSitesConn]
	if len(recs) == 0 {
		return nil
	}
	out := make([]CallSite, 0, len(recs))
	for _, rec := range recs {
		s, ok := DecodeCallSite(rec)
		if !ok || (calleeID != "" && s.CalleeID != calleeID) {
			continue
		}
		out = append(out, s)
	}
	return out
}
