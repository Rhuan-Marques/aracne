package topogrep

import (
	"aracne/internal/topology/domain"
)

// The search half of descriptions.lazy.
//
// A description that does not exist cannot match a pattern, so lazy generation can never widen
// the description TIER of a search -- that much is inherent. What it can do is describe the
// nodes a search already found by their name or by a line in their body, because those nodes
// are about to be printed with a "# <id> — <description>" header, and that header is the part
// of a search result that answers the question without a follow-up read.
//
// The subtlety is that the header is not unconditional. FormatResult drops annotation entirely
// once the headers grow past AnnotateOverheadBudget of the content they annotate -- and adding
// a description to a header is exactly what makes it grow. A fill that ignored this could
// generate prose and, by generating it, push the result over the line that stops it being
// printed: strictly worse than not filling at all, and paid for. So the targets are chosen
// under a projection of the same budget FormatResult will apply.

// LazyTargets returns the resources in a result that have no description and whose header the
// result will actually print, in the order they should be described.
//
// projectedDescBytes is how long a not-yet-written description is assumed to be; pass a kind's
// budget from domain.DescriptionBudget, or 0 for the function budget.
func LazyTargets(res *Result, projectedDescBytes int) []string {
	if res == nil || len(res.Matches) == 0 {
		return nil
	}
	if projectedDescBytes <= 0 {
		projectedDescBytes = domain.DescriptionBudgetFunction
	}

	seen := map[string]bool{}
	var out []string

	// Node rows are annotated whatever the budget says -- they are in the result BECAUSE of
	// their title or description, and without the header they render as an unexplained
	// declaration line. They are therefore never in question.
	for _, m := range res.Matches {
		if !m.NodeHit || m.ResourceID == "" || m.Description != "" || seen[m.ResourceID] {
			continue
		}
		seen[m.ResourceID] = true
		out = append(out, m.ResourceID)
	}

	// Line matches are annotated only if the whole result is. Reproduce that decision, then
	// keep adding projected descriptions for as long as it survives.
	if res.DistinctResources <= 0 || res.DistinctResources > AnnotateLimit {
		return out
	}
	content, header := 0, 0
	counted := map[string]bool{}
	for _, m := range res.Matches {
		if m.NodeHit {
			continue
		}
		content += len(m.Path) + len(m.Text) + 8
		if m.ResourceID == "" || counted[m.ResourceID] {
			continue
		}
		counted[m.ResourceID] = true
		header += len(m.ResourceID) + len(m.Description) + 6
	}
	if content == 0 {
		return out
	}
	fits := func(h int) bool {
		if content+h <= AnnotateFreeBytes {
			return true
		}
		return float64(h)/float64(content) <= AnnotateOverheadBudget
	}
	// Annotation is already off for the line matches, so no description written now would be
	// printed by this call. The node rows above still stand.
	if !fits(header) {
		return out
	}
	for _, m := range res.Matches {
		if m.NodeHit || m.ResourceID == "" || m.Description != "" || seen[m.ResourceID] {
			continue
		}
		if !fits(header + projectedDescBytes) {
			break
		}
		header += projectedDescBytes
		seen[m.ResourceID] = true
		out = append(out, m.ResourceID)
	}
	return out
}

// ApplyDescriptions re-reads every match's description from a topology, which is how a result
// picks up prose written after the search ran.
//
// It rewrites rather than fills gaps: the topology is the source of truth for a description,
// and a match holding a different one is holding a stale copy.
func ApplyDescriptions(res *Result, topo *domain.Topology) {
	if res == nil || topo == nil {
		return
	}
	for i := range res.Matches {
		id := res.Matches[i].ResourceID
		if id == "" {
			continue
		}
		if r, ok := topo.Resources[id]; ok {
			res.Matches[i].Description = r.Description
		}
	}
}
