// Package warnread expands a topology warning into the source of the code it names, with the
// warning written on the line that caused it.
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
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
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

// The two headlines a warning report can open with.
//
// SEPARATE BECAUSE THE CLAIM IS DIFFERENT. The report an edit comes back with is handed to
// whoever just wrote to the tree, so it can say their change did this. `arac warnings list
// --read` and the warnings_list tool are PULL surfaces: they answer "what is outstanding",
// against a table that may have been broken days ago by someone else, and telling that caller
// "your changes caused" this would be a claim the renderer is in no position to make. The
// same mistake formatDriftWarnings documents at its own header.
//
// Both say the notes are not in the file. `arac edit` matches old_string against the bytes on
// disk, so a model that builds one out of an annotated line gets a confusing "not found" --
// the same hazard renderstate.ElisionMarker exists to avoid.
const (
	HeadlinePush = "Your changes caused conflicts. Fix these files as soon as possible " +
		"(the `<- [...]` notes are not in the file):"
	HeadlinePull = "Warned code, annotated in place. Fix these files " +
		"(the `<- [...]` notes are not in the file):"
)

// Options is what a surface tells the expansion about itself.
type Options struct {
	// Budget bounds the whole expansion. HookBudget on the hook path, NoBudget on the two
	// surfaces that were asked for exactly this and are waiting for it; see both constants.
	Budget time.Duration
	// Headline opens the report. Empty means HeadlinePull, the claim that is safe anywhere.
	Headline string
	// Reserve is how many bytes the caller puts in front of the section in the same message --
	// `arac edit`'s own result, a nudge the hook joins it with, and the separators between
	// them. features.warning_read_max_bytes bounds the MESSAGE the model receives, not this
	// function's return value, so whatever else travels with it is paid for out of the same
	// budget. See Budgeted.
	Reserve int
}

// Section renders the batched, annotated read behind features.warning_reads, plus the
// "N warnings left" note when the byte budget bit. It is the whole feature, shared by the three
// surfaces that offer it: the report an edit comes back with (cli.driftWarningReport),
// `arac warnings list --read`, and the warnings_list tool's `read`.
//
// IT REPLACES THE SUMMARY THOSE SURFACES USED TO PRINT, rather than following it. A summary
// line is "this kind of warning, on these two ids" -- which is exactly what an annotation
// writes on the offending line, where the reader is already looking. Printing both said
// everything twice in the one report emitted on the agent's critical path. Each caller keeps
// its own listing as the FALLBACK for an empty return, so the report is never worse than the
// one it replaced; see uncoveredTail for the warnings an expansion cannot speak for.
//
// ONE read call for the whole batch, not one per warning, and that is the requirement rather
// than an optimization. universaltools.Read shares a single renderstate ledger across
// everything in a call: an enclosing type, an import block, a neighbour in the CONTEXT section
// is emitted once and back-referenced afterwards. Eleven callers of one function are eleven
// warnings, one callee and (usually) a handful of files -- read one at a time that is the
// callee's declaration eleven times over. Batched, it is once.
//
// THE CAP IS BYTES, NOT WARNINGS. It used to be a count, and a count does not bound what it
// was standing in for: one warning in a 300-line method and one in a one-liner are the same
// "1". Measured on a benchmark run, a 14-warning report came to 13.7 KB, over the ~10 KB at
// which Claude Code stops injecting hook context and substitutes a 2 KB preview -- the model saw
// 2 of the 14 annotations and never opened the saved file. So the page is the LONGEST prefix of
// the ordered warnings whose whole message -- headline, reads, CONTEXT, the uncovered tail, the
// note, and the caller's Reserve -- fits features.warning_read_max_bytes. See fitPrefix.
//
// THE CAP IS NOT A TRUNCATION, it is a page. Whatever the cap left out is named in the note,
// and the surface the note points at reads the next page from the table as it stands -- so
// fixing the first page and asking again gets the next one, because a fixed warning has
// retired itself by then. That is why no "already expanded" ledger exists and must not: the
// warnings are derived from current state (see domain.TopologyWarning), and a remembered
// page would hand the model warnings 6-10 while 1-5 were still broken.
//
// budget bounds the whole thing; NoBudget (0) waits for the answer. See both
// constants for which surface gets which and why.
func Section(dbPath string, reg *scanner.Registry, warnings []domain.TopologyWarning, opt Options) string {
	out, _ := SectionPage(dbPath, reg, warnings, opt)
	return out
}

// SectionPage is Section plus WHICH warnings the page showed -- the prefix of the sorted list
// that fit, or nil when nothing was expanded. A caller holding warnings that exist nowhere else
// needs it: a transient is drained from its queue to be reported, and one the budget left off
// the page has to go back, or the note's "N left" names warnings no surface can produce.
func SectionPage(dbPath string, reg *scanner.Registry, warnings []domain.TopologyWarning,
	opt Options) (string, []domain.TopologyWarning) {
	if opt.Headline == "" {
		opt.Headline = HeadlinePull
	}
	if dbPath == "" || len(warnings) == 0 {
		return "", nil
	}
	cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
	if !cfg.WarningReadsEnabled() {
		return "", nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		return "", nil
	}

	// SORTED BEFORE IT IS CAPPED, through the ordering every warning surface prints in. The
	// list arrives from unreportedWarnings or from ListWarnings, both of which walk the
	// warnings table as a map -- so "the first page" would be a different page on every run,
	// and the note below would promise a continuation that starts somewhere else. See
	// domain.SortWarnings.
	ordered := append([]domain.TopologyWarning(nil), warnings...)
	domain.SortWarnings(ordered)
	limit := Budgeted(cfg, opt.Reserve)

	// Panic-proof always, deadlined only where the caller set one: a read that hangs or a
	// scanner that panics must not take the agent's tool call down with it. See runGuardScan,
	// which does the same for the scan half.
	type result struct {
		out   string
		shown int
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- result{}
			}
		}()
		exp := prepare(dbPath, reg, cfg, ordered)
		if exp == nil {
			done <- result{}
			return
		}
		// The page's own length is not the count: the tail and note are text, not warnings.
		pages := map[string]int{}
		out := fitPrefix(len(ordered), limit, exp.readable, func(k int) string {
			out := exp.render(ordered[:k], opt.Headline)
			if out == "" {
				return ""
			}
			// Attached to the READ rather than to the caller's summary, so the one surface
			// that knows how many warnings it left unexpanded is the one that says so -- and
			// measured with it, because the note is bytes the model receives too.
			if note := LeftNote(cfg, len(ordered)-k); note != "" {
				out += "\n\n" + note
			}
			pages[out] = k
			return out
		})
		done <- result{out, pages[out]}
	}()
	var expired <-chan time.Time
	if opt.Budget > 0 {
		expired = time.After(opt.Budget)
	}
	select {
	case r := <-done:
		if r.out == "" {
			return "", nil
		}
		return r.out, ordered[:r.shown]
	case <-expired:
		return "", nil
	}
}

// Budgeted is how many bytes a section may occupy once the caller's Reserve is paid for: 0 means
// unlimited (features.warning_read_max_bytes at 0 or below), and a Reserve that leaves nothing
// is reported as -1, which no non-empty page fits.
func Budgeted(cfg *helper.Config, reserve int) int {
	maxBytes := cfg.EffectiveWarningReadMaxBytes()
	if maxBytes <= 0 {
		return 0
	}
	if left := maxBytes - reserve; left > 0 {
		return left
	}
	return -1
}

// fitPrefix returns the page for the LARGEST k in [1, n] whose page(k) is non-empty and at most
// limit bytes, or "" when none is. limit 0 means unlimited, so the page is all n.
//
// BINARY SEARCH, because every probe is a full batched read -- resolve, freshen, cut, render
// the CONTEXT section. The common case costs one: all n are tried first, and a report that fits
// is returned untouched. Only a report that does not fit pays log2(n) more.
//
// It relies on a page never SHRINKING as warnings are added, which holds for everything that
// scales -- each warning adds a read, an annotation or a tail line, and the shared ledger only
// ever removes repeats. The one term that moves the other way is the note's count ("12 left" is
// one byte longer than "9 left"), which can make the search settle one warning short of the
// true maximum; it can never make it return a page over the limit, because every candidate is
// measured before it is kept.
//
// minK is the smallest prefix that has anything to read. Below it every page is empty, which
// would read to the search as "too big" and send it the wrong way; starting from it keeps the
// predicate monotone. A prefix with nothing readable at all (minK == 0) has no page.
func fitPrefix(n, limit int, minK func() int, page func(k int) string) string {
	if n == 0 {
		return ""
	}
	all := page(n)
	if limit == 0 || (all != "" && len(all) <= limit) {
		return all
	}
	lo := minK()
	if lo == 0 {
		return ""
	}
	hi, best := n-1, ""
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if out := page(mid); out != "" && len(out) <= limit {
			best, lo = out, mid+1
		} else {
			hi = mid - 1
		}
	}
	return best
}

// LeftNote is what the model reads when the budget bit: how many warnings it has not been
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
	return fmt.Sprintf("... %d %s left. Use %s to continue fixing.", left, warningNoun(left), listSurface(cfg, true))
}

// MoreNote closes a plain LISTING cut short by the same budget -- the fallback summary a report
// prints when nothing could be expanded. It points at the unexpanded listing, because the
// expansion is exactly what just came back empty for these warnings.
func MoreNote(cfg *helper.Config, left int) string {
	if left <= 0 {
		return ""
	}
	return fmt.Sprintf("... %d more %s. Use %s to see them all.", left, warningNoun(left), listSurface(cfg, false))
}

func warningNoun(n int) string {
	if n == 1 {
		return "warning"
	}
	return "warnings"
}

func listSurface(cfg *helper.Config, read bool) string {
	switch {
	case cfg.MCPEnabled() && read:
		return "`warnings_list` with `read: true`"
	case cfg.MCPEnabled():
		return "`warnings_list`"
	case read:
		return "`arac warnings list --read`"
	default:
		return "`arac warnings list`"
	}
}

// expansion is everything a page needs that does not depend on WHICH page: the loaded graph,
// the read tool, and each warning's anchor. Built once per Section, so the binary search in
// fitPrefix re-renders without re-loading the database or re-cutting a single source.
type expansion struct {
	read    *universaltools.Read
	topo    *domain.Topology
	kinds   map[domain.ResourceKind]bool
	anchors map[string]anchored // by warning ID; absent means no line to mark
	ordered []domain.TopologyWarning
}

type anchored struct {
	id  string
	ann readunit.Annotation
}

// prepare resolves what every page of this report shares. nil means nothing can be read at all.
func prepare(dbPath string, reg *scanner.Registry, cfg *helper.Config,
	ordered []domain.TopologyWarning) *expansion {
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		return nil
	}
	topo, err := mgr.ReadAll()
	if err != nil {
		return nil
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

	// FRESHEN BEFORE CUTTING. anchorFor cuts each warning's source itself, to find the line
	// the warning points at, and it does that before ReadIDs runs its own freshness pass.
	// This runs immediately after something wrote to the tree, so the spans it would cut from
	// are exactly the stale ones -- and the anchor would be looked for in whatever lines now
	// sit at the old offsets. One stat per file on the common, fresh path.
	if read.Freshen(anchorPaths(topo, ordered)) {
		if refreshed, err := mgr.ReadAll(); err == nil {
			topo = refreshed
		}
	}

	exp := &expansion{read: read, topo: topo, kinds: read.ReadKinds(),
		anchors: map[string]anchored{}, ordered: ordered}
	for _, w := range ordered {
		if id, a, ok := anchorFor(mgr, topo, w); ok {
			exp.anchors[w.ID] = anchored{id: id, ann: a}
		}
	}
	return exp
}

// readable is the smallest k whose prefix names anything the read can show, or 0 for none.
func (e *expansion) readable() int {
	for k := 1; k <= len(e.ordered); k++ {
		if len(readIDs(e.topo, e.kinds, e.ordered[k-1:k])) > 0 {
			return k
		}
	}
	return 0
}

// render is one page: the given warnings read in one annotated call, under headline.
func (e *expansion) render(page []domain.TopologyWarning, headline string) string {
	ids := readIDs(e.topo, e.kinds, page)
	if len(ids) == 0 {
		return ""
	}
	anns := map[string][]readunit.Annotation{}
	covered := map[string]bool{}
	for _, w := range page {
		a, ok := e.anchors[w.ID]
		// An anchor on a resource the read is not going to show is not an anchor. readIDs
		// drops ids the graph no longer holds and kinds read.kinds forbids, and a note keyed
		// to one of those would simply never be placed -- so it belongs in the tail instead.
		if !ok || !contains(ids, a.id) {
			continue
		}
		anns[a.id] = append(anns[a.id], a.ann)
		covered[w.ID] = true
	}

	out, err := e.read.ReadIDs(ids, universaltools.ReadIDsOptions{
		Annotations:   anns,
		SignatureOnly: signatureOnly(page, anns),
	})
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	return headline + "\n" + strings.TrimRight(out, "\n") + uncoveredTail(page, covered)
}

// signatureOnly is the callees on this page that are read for their signature alone.
//
// A signature_changed warning reads its callee so the caller can be checked against it -- and
// that check needs the signature, not the body. The callee is also, nearly always, what the
// agent just edited, so its body is already in the agent's context: shown in full, a report of
// five callers carried five function bodies the reader had written a moment earlier, and pushed
// warnings that mattered onto the next page.
//
// A callee that is ALSO a fix site on the page -- a caller in another of its warnings, or
// anything carrying an annotation -- is shown in full, because there the body is the point:
// changing GetServer's parameters makes GetServer a callee, and the newHandler call inside it,
// whose own signature moved, makes it a caller.
func signatureOnly(page []domain.TopologyWarning, anns map[string][]readunit.Annotation) map[string]bool {
	fixSites := map[string]bool{}
	for _, w := range page {
		if targets := readTargets(w); len(targets) > 0 && targets[0] != "" {
			fixSites[targets[0]] = true
		}
	}
	out := map[string]bool{}
	for _, w := range page {
		if w.Kind != domain.WarnSignatureChanged || w.SourceID == "" || fixSites[w.SourceID] || len(anns[w.SourceID]) > 0 {
			continue
		}
		out[w.SourceID] = true
	}
	return out
}

// anchorPaths is the files anchorFor will cut from, for the freshness check above.
func anchorPaths(topo *domain.Topology, warnings []domain.TopologyWarning) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range warnings {
		for _, id := range readTargets(w) {
			res, ok := topo.Resources[id]
			if !ok || res.Location.Path == "" || seen[res.Location.Path] {
				continue
			}
			seen[res.Location.Path] = true
			out = append(out, res.Location.Path)
		}
	}
	return out
}

// uncoveredTail names the warnings this report showed no code for.
//
// The summary listing that used to sit above the read is gone -- it restated what the
// annotations now say on the line itself. But a warning whose fix site the graph no longer
// holds, or whose kind read.kinds forbids, has no line to be written on, and dropping it
// would make a report that is silently less complete than the one it replaced. So exactly
// those are listed, and nothing else: empty and invisible in the common case.
func uncoveredTail(warnings []domain.TopologyWarning, covered map[string]bool) string {
	var b strings.Builder
	for _, w := range warnings {
		if covered[w.ID] {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("\n\nAlso warned, no code to show:\n")
		}
		fmt.Fprintf(&b, "  [%s] %s (source: %s", w.Kind, w.Message, w.SourceID)
		if w.TargetID != "" {
			fmt.Fprintf(&b, ", target: %s", w.TargetID)
		}
		b.WriteString(")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// contains reports whether id is in the list the read was actually given.
func contains(ids []string, id string) bool {
	for _, got := range ids {
		if got == id {
			return true
		}
	}
	return false
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
