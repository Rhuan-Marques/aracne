package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// warningReadBudget bounds the whole expansion, for the same reason every other stage of a
// hook is bounded: this runs on the agent's critical path, after an edit, and a report that
// arrives late is a report the harness has already killed. Spent inside GuardHookTimeoutSeconds
// alongside driftCheckBudget -- 20 + 10 plus the small checks around them still leaves margin
// under 45.
const warningReadBudget = 10 * time.Second

// driftWarningReport is the text a warning report comes back as: the summary formatDriftWarnings
// has always rendered, plus -- when features.warning_reads is on -- the full read of the code
// those warnings name.
//
// One funnel, because there are four entrances to the same report (`arac edit`, `arac write`,
// `arac update-file`, and the guard's post-tool drift check) and a warning has to read
// identically whichever produced the change. The expansion is best-effort: everything below
// returns "" rather than failing, so a project that turns the feature on never gets a WORSE
// report than one that leaves it off.
func driftWarningReport(dbPath string, warnings []domain.TopologyWarning) string {
	summary := formatDriftWarnings(warnings)
	if summary == "" {
		return ""
	}
	reads := warningReads(dbPath, warnings)
	if reads == "" {
		return summary
	}
	return summary + "\n\n" + reads
}

// warningReads renders the batched read behind features.warning_reads.
//
// ONE read call for the whole batch, not one per warning, and that is the requirement rather
// than an optimization. universaltools.Read shares a single renderstate ledger across
// everything in a call: an enclosing type, an import block, a neighbour in the CONTEXT section
// is emitted once and back-referenced afterwards. Eleven callers of one function are eleven
// warnings, one callee and (usually) a handful of files -- read one at a time that is the
// callee's declaration eleven times over. Batched, it is once.
func warningReads(dbPath string, warnings []domain.TopologyWarning) string {
	if dbPath == "" || len(warnings) == 0 {
		return ""
	}
	cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
	if !cfg.WarningReadsEnabled() {
		return ""
	}
	if _, err := os.Stat(dbPath); err != nil {
		return ""
	}

	// SORTED BEFORE IT IS CAPPED. The list arrives from unreportedWarnings, which walks the
	// warnings table as a map -- so "the first five" is a different five on every run, and a
	// capped expansion would have shown the model an arbitrary subset and called it the top
	// of the list. Ordered by kind then by the ids, which is the order `arac warnings list`
	// prints, so the expansion and that command agree on what the first five are.
	ordered := append([]domain.TopologyWarning(nil), warnings...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
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
	limit := cfg.EffectiveWarningReadLimit()
	expanded := ordered
	if limit > 0 && len(expanded) > limit {
		expanded = expanded[:limit]
	}

	// Bounded and panic-proof, like every other piece of work a hook runs: a read that hangs
	// or a scanner that panics must not take the agent's tool call down with it. See
	// runGuardScan, which does the same for the scan half.
	done := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- ""
			}
		}()
		done <- renderWarningReads(dbPath, cfg, expanded, len(ordered))
	}()
	select {
	case out := <-done:
		return out
	case <-time.After(warningReadBudget):
		return ""
	}
}

// renderWarningReads resolves the ids the expanded warnings name and reads them in one call.
func renderWarningReads(
	dbPath string, cfg *helper.Config, expanded []domain.TopologyWarning, total int,
) string {
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		return ""
	}
	topo, err := mgr.ReadAll()
	if err != nil {
		return ""
	}
	// NO LAZY FILL ON THIS PATH, for the reason proxyRead gives: NewRead attaches a
	// descriptions filler that is awaited inline for up to descriptions.lazy.timeout_seconds
	// (45s by default), which is far longer than warningReadBudget -- so on the repository
	// where the filler has the most to do, the expansion would reliably time out and the
	// model would get nothing. The next ordinary read still fills them.
	//
	// The registry is passed so a file changed since it was indexed is re-parsed before the
	// answer is cut from it, rather than served from spans that no longer fit. That matters
	// more here than anywhere: this runs immediately after something wrote to the tree.
	read := universaltools.NewRead(mgr, cfg, false, NewScannerRegistry()).WithFiller(nil)
	ids := warningReadIDs(topo, read.ReadKinds(), expanded)
	if len(ids) == 0 {
		return ""
	}
	out, err := read.ReadIDs(ids, universaltools.ReadIDsOptions{})
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	return warningReadsHeader(len(expanded), total) + "\n" + strings.TrimRight(out, "\n")
}

// warningReadIDs is the ordered, de-duplicated list of resource ids the expansion reads.
//
// FILTERED AGAINST THE GRAPH, not handed over as the warning spelled them. Two of the four
// kinds name a target that is GONE by construction -- node_removed and use_missing_node are
// precisely "this still references something that no longer exists" -- so passing every id
// through would put a "# UNRESOLVED:" block under every such report, telling the model its
// own warning was a typo. Only what the graph still holds, and only kinds read.kinds allows,
// is asked for; a warning both of whose halves are unreadable simply keeps its summary line.
func warningReadIDs(
	topo *domain.Topology, kinds map[domain.ResourceKind]bool, warnings []domain.TopologyWarning,
) []string {
	seen := map[string]bool{}
	var ids []string
	for _, w := range warnings {
		for _, id := range warningReadTargets(w) {
			if id == "" || seen[id] {
				continue
			}
			res, ok := topo.Resources[id]
			if !ok || !kinds[res.Kind] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// warningReadTargets is which halves of one warning are worth reading, FIX SITE FIRST.
//
// The two ids a warning carries are not interchangeable, and which one holds the code to
// change depends on the kind:
//
//   - signature_changed -- SourceID is the callee whose signature moved, TargetID the caller
//     that has to be verified. The caller is the edit; the callee is what it must now match,
//     and reading both is what lets the model write the fix without looking anything up.
//   - interface_conflict -- the implementer (TargetID) is the thing to go fix, and the
//     interface (SourceID) is the promise it has to meet. Same shape, same reason.
//   - node_removed / use_missing_node -- SourceID is the referrer that still points at
//     something gone. TargetID is that gone thing; it is listed anyway because
//     warningReadIDs drops what the graph does not hold, so naming it costs nothing and a
//     target that came back is worth showing.
//
// Order matters beyond taste: the batch shares one render ledger, so whatever is named first
// is rendered in full and the rest back-reference it. The fix site is what should be in full.
func warningReadTargets(w domain.TopologyWarning) []string {
	switch w.Kind {
	case domain.WarnSignatureChanged, domain.WarnInterfaceConflict:
		return []string{w.TargetID, w.SourceID}
	default:
		return []string{w.SourceID, w.TargetID}
	}
}

// warningReadsHeader says what the block below it is, and -- when the cap bit -- that it is not
// all of them. A truncation the reader cannot see is one it will assume did not happen.
func warningReadsHeader(expanded, total int) string {
	if expanded < total {
		return fmt.Sprintf(
			"Warned code, read in full (%d of %d warnings; features.warning_read_limit caps this, "+
				"`arac warnings list` has the rest). Fix from this instead of reading it again:",
			expanded, total)
	}
	return "Warned code, read in full. Fix from this instead of reading it again:"
}
