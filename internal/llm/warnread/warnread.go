// Package warnread expands a topology warning into the source of the code it names.
//
// IT LIVES BELOW BOTH ITS CALLERS on purpose. The expansion is one behaviour with three
// surfaces -- the report an edit comes back with, `arac warnings list --read`, and the
// warnings_list tool's `read` -- and two of those are in packages that cannot see each other
// (internal/cli and internal/llm/tools). Keeping it here is what lets all three run the same
// code instead of one of them being handed a closure by whoever wired it.
package warnread

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// HookBudget bounds the expansion on the HOOK path, for the same reason every other
// stage of a hook is bounded: it runs on the agent's critical path, after an edit, and a report
// that arrives late is a report the harness has already killed. Spent inside
// GuardHookTimeoutSeconds alongside driftCheckBudget -- 20 + 10 plus the small checks around
// them still leaves margin under 45.
const HookBudget = 10 * time.Second

// NoBudget is the deadline for a surface the model ASKED for the reads on --
// `arac warnings list --read` and the warnings_list tool's `read`. There is none.
//
// The hook's deadline exists because a report that arrives late is discarded whole, which
// makes returning nothing the better of two bad outcomes. Neither of those holds here: the
// caller is waiting for exactly this answer, and handing it an empty section because a large
// repository took eleven seconds would be a silent wrong answer to a direct question.
const NoBudget time.Duration = 0

// Section renders the batched read behind features.warning_reads, plus the
// "N warnings left" note when the cap bit. It is the whole feature, shared by the three
// surfaces that offer it: the report an edit comes back with (cli.driftWarningReport),
// `arac warnings list --read`, and the warnings_list tool's `read`.
//
// ONE read call for the whole batch, not one per warning, and that is the requirement rather
// than an optimization. universaltools.Read shares a single renderstate ledger across
// everything in a call: an enclosing type, an import block, a neighbour in the CONTEXT section
// is emitted once and back-referenced afterwards. Eleven callers of one function are eleven
// warnings, one callee and (usually) a handful of files -- read one at a time that is the
// callee's declaration eleven times over. Batched, it is once.
//
// THE CAP IS NOT A TRUNCATION, it is a page. Whatever the cap left out is named in the note,
// and the surface the note points at reads the next page from the table as it stands -- so
// fixing the first five and asking again gets the next five, because a fixed warning has
// retired itself by then. That is why no "already expanded" ledger exists and must not: the
// warnings are derived from current state (see domain.TopologyWarning), and a remembered
// page would hand the model warnings 6-10 while 1-5 were still broken.
//
// budget bounds the whole thing; NoBudget (0) waits for the answer. See both
// constants for which surface gets which and why.
func Section(dbPath string, reg *scanner.Registry, warnings []domain.TopologyWarning, budget time.Duration) string {
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

	// SORTED BEFORE IT IS CAPPED, through the ordering every warning surface prints in. The
	// list arrives from unreportedWarnings or from ListWarnings, both of which walk the
	// warnings table as a map -- so "the first five" would be a different five on every run,
	// and the note below would promise a continuation that starts somewhere else. See
	// domain.SortWarnings.
	ordered := append([]domain.TopologyWarning(nil), warnings...)
	domain.SortWarnings(ordered)
	limit := cfg.EffectiveWarningReadLimit()
	expanded := ordered
	if limit > 0 && len(expanded) > limit {
		expanded = expanded[:limit]
	}

	// Panic-proof always, deadlined only where the caller set one: a read that hangs or a
	// scanner that panics must not take the agent's tool call down with it. See runGuardScan,
	// which does the same for the scan half.
	done := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- ""
			}
		}()
		done <- renderReads(dbPath, reg, cfg, expanded)
	}()
	var expired <-chan time.Time
	if budget > 0 {
		expired = time.After(budget)
	}
	select {
	case out := <-done:
		if out == "" {
			return ""
		}
		// Attached to the READ rather than to the caller's summary, so the one surface that
		// knows how many warnings it left unexpanded is the one that says so.
		if note := LeftNote(cfg, len(ordered)-len(expanded)); note != "" {
			out += "\n\n" + note
		}
		return out
	case <-expired:
		return ""
	}
}

// LeftNote is what the model reads when the cap bit: how many warnings it has not been
// shown the code for, and the one command that shows the next page of them.
//
// It names the surface THIS project actually has. In ModeMCP the model is served warnings_list
// as a tool and has no reason to reach for a shell command; in every other mode there is no MCP
// tool at all and the CLI verb is the only spelling that exists. (A ModeMCP project that has
// hand-narrowed mcp_tools to exclude warnings_list gets a pointer to a tool it disabled --
// `arac warnings list --read` still works there, and narrowing that list is the deliberate act
// of someone who knows what they removed.)
func LeftNote(cfg *helper.Config, left int) string {
	if left <= 0 {
		return ""
	}
	noun := "warnings"
	if left == 1 {
		noun = "warning"
	}
	surface := "`arac warnings list --read`"
	if cfg.MCPEnabled() {
		surface = "`warnings_list` with `read: true`"
	}
	return fmt.Sprintf("... %d %s left. Use %s to continue fixing.", left, noun, surface)
}

// renderReads resolves the ids the expanded warnings name and reads them in one call.
func renderReads(dbPath string, reg *scanner.Registry, cfg *helper.Config, expanded []domain.TopologyWarning) string {
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
	// (45s by default), which is far longer than HookBudget -- so on the repository
	// where the filler has the most to do, the expansion would reliably time out and the
	// model would get nothing. The next ordinary read still fills them.
	//
	// The registry is passed so a file changed since it was indexed is re-parsed before the
	// answer is cut from it, rather than served from spans that no longer fit. That matters
	// more here than anywhere: this runs immediately after something wrote to the tree.
	read := universaltools.NewRead(mgr, cfg, false, reg).WithFiller(nil)
	ids := readIDs(topo, read.ReadKinds(), expanded)
	if len(ids) == 0 {
		return ""
	}
	out, err := read.ReadIDs(ids, universaltools.ReadIDsOptions{})
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	return readsHeader + "\n" + strings.TrimRight(out, "\n")
}

// readIDs is the ordered, de-duplicated list of resource ids the expansion reads.
//
// FILTERED AGAINST THE GRAPH, not handed over as the warning spelled them. Two of the four
// kinds name a target that is GONE by construction -- node_removed and use_missing_node are
// precisely "this still references something that no longer exists" -- so passing every id
// through would put a "# UNRESOLVED:" block under every such report, telling the model its
// own warning was a typo. Only what the graph still holds, and only kinds read.kinds allows,
// is asked for; a warning both of whose halves are unreadable simply keeps its summary line.
func readIDs(
	topo *domain.Topology, kinds map[domain.ResourceKind]bool, warnings []domain.TopologyWarning,
) []string {
	seen := map[string]bool{}
	var ids []string
	for _, w := range warnings {
		for _, id := range readTargets(w) {
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

// readTargets is which halves of one warning are worth reading, FIX SITE FIRST.
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
//     readIDs drops what the graph does not hold, so naming it costs nothing and a
//     target that came back is worth showing.
//
// Order matters beyond taste: the batch shares one render ledger, so whatever is named first
// is rendered in full and the rest back-reference it. The fix site is what should be in full.
func readTargets(w domain.TopologyWarning) []string {
	switch w.Kind {
	case domain.WarnSignatureChanged, domain.WarnInterfaceConflict:
		return []string{w.TargetID, w.SourceID}
	default:
		return []string{w.SourceID, w.TargetID}
	}
}

// readsHeader says what the block below it is. It says nothing about the cap: the note
// under the read carries that, together with the remedy, and stating it twice is one statement
// to keep in sync with the arithmetic for no reader who is better off.
const readsHeader = "Warned code, read in full. Fix from this instead of reading it again:"
