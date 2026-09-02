// Package lazydesc generates a missing description at the moment something is about to show
// it, instead of only in an `arac descriptions generate` sweep.
//
// WHY. `read.context_filter.hide_no_description` defaults to true, so on a repo that has
// never been swept an undescribed neighbour is not merely undescribed in the "# CONTEXT:"
// block -- it is absent from it. The sweep that fixes that is whole-repo, expensive and
// all-or-nothing, which is exactly the wrong shape for a large codebase where most of the code
// is never read. Filling on demand converges on the same place one answer at a time, and only
// for the parts of the graph anyone actually looks at.
//
// Everything here is best-effort. A read must never fail, or return less, because a
// description could not be generated: no provider, no API key, a refused completion, a
// rejected write or an expired deadline all mean "nothing changed", and the answer renders
// from the topology exactly as it stands.
package lazydesc

import (
	"sort"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

// Target is one resource a fill will try to describe.
type Target struct {
	ID   string
	Name string
	Kind domain.ResourceKind
	// rank orders the cap: lower is kept first. See seedRank.
	rank int
}

// Seed ranks. A cap has to drop something, and what it drops should be what the answer is
// least likely to print.
//
// A resource the caller named is rank 0 for a search -- its header IS the result row. Its
// direct outgoing neighbours are what the "# CONTEXT:" block lists, so they come next.
// Containment (a file's or package's members) follows: those sections exist, but a whole-file
// read is also the request most likely to name a hundred nodes at once, and it is the one
// where the caller already has the source in front of them. Incoming references are last: the
// "# USED BY:" section is off unless read.context_filter.include_incoming turns it on.
const (
	rankSelf = iota
	rankOutgoing
	rankContainment
	rankIncoming
)

// connection keys, restated here rather than exported from domain because the walk below
// wants them grouped by what they mean for a CONTEXT block, which is not how domain groups
// them (domain's outgoingKeys deliberately excludes containment; here it is a separate,
// lower-priority tier rather than an exclusion).
var (
	outgoingKeys = []string{
		"calls", "uses_struct", "uses_class", "uses_interface", "uses_named_type", "uses_extvar",
	}
	containmentKeys = []string{
		"has_function", "has_struct", "has_class", "has_interface", "has_named_type",
		"has_extvar", "methods", "constructor",
	}
)

// PlanOptions is what the planner needs to decide, beyond the graph itself.
type PlanOptions struct {
	// Targets is descriptions.kinds: which kinds are worth describing at all.
	Targets []domain.ResourceKind
	// Filter is the resolved read.context_filter, so a neighbour that would render as full
	// code or not at all is not described for a line that will never be printed.
	Filter domain.ContextFilter
	// IncludeNotVisible mirrors descriptions.include_not_visible.
	IncludeNotVisible bool
	// IncludeIncoming mirrors read.context_filter.include_incoming: with it off, the
	// "# USED BY:" section is not rendered and the resources feeding it need no prose.
	IncludeIncoming bool
	// MaxNodes caps the result. <= 0 means no cap.
	MaxNodes int
	// Restrict, when non-nil, is the only set of ids that may be described.
	//
	// A windowed read (`head -20 file.go`) bounds its context section to the resources the
	// WINDOW actually mentions, not everything the enclosing declaration reaches. Without
	// this the fill would plan from the declaration and pay for prose the window was never
	// going to print.
	Restrict map[string]bool
}

// ReadTargets returns the resources whose descriptions a read of `ids` would show and which do
// not have one yet.
//
// The resources being READ are not in the result. Their body is source code; a read shows what
// they do by showing them. It is their neighbourhood that renders as "## id: description", and
// that is what a fill is for.
func ReadTargets(topo *domain.Topology, ids []string, opt PlanOptions) []Target {
	if topo == nil || len(ids) == 0 {
		return nil
	}
	seedRank := map[string]int{}
	requested := map[string]bool{}
	for _, id := range ids {
		requested[id] = true
	}

	note := func(id string, rank int) {
		if id == "" || requested[id] {
			return
		}
		if cur, ok := seedRank[id]; !ok || rank < cur {
			seedRank[id] = rank
		}
	}

	for _, id := range ids {
		res, ok := topo.Resources[id]
		if !ok {
			continue
		}
		for _, key := range outgoingKeys {
			for _, target := range res.Connections[key] {
				note(target, rankOutgoing)
			}
		}
		for _, key := range containmentKeys {
			for _, target := range res.Connections[key] {
				note(target, rankContainment)
			}
		}
	}

	// Incoming edges have no index in the topology, so they are found by walking every
	// resource once. That is O(V+E) over a graph already fully in memory, and it happens only
	// when the section that consumes them is switched on.
	if opt.IncludeIncoming {
		for id, res := range topo.Resources {
			if requested[id] {
				continue
			}
			if referencesAny(res, requested) {
				note(id, rankIncoming)
			}
		}
	}

	return collect(topo, seedRank, opt)
}

// NodeTargets returns the subset of `ids` that themselves need a description.
//
// This is the search case. A description that does not exist cannot match a pattern, so lazy
// generation can never widen grep's description tier -- but a node found by its NAME or by a
// line in its BODY is about to be printed with a "# <id> — <description>" header, and that
// header is the part of a search result that answers the question without a follow-up read.
func NodeTargets(topo *domain.Topology, ids []string, opt PlanOptions) []Target {
	if topo == nil || len(ids) == 0 {
		return nil
	}
	seedRank := make(map[string]int, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, seen := seedRank[id]; !seen {
			seedRank[id] = rankSelf
		}
	}
	return collect(topo, seedRank, opt)
}

// referencesAny reports whether res points at any id in the set, by an edge that would put it
// in a "used by" section.
func referencesAny(res domain.Resource, set map[string]bool) bool {
	for _, key := range outgoingKeys {
		for _, target := range res.Connections[key] {
			if set[target] {
				return true
			}
		}
	}
	return false
}

// collect filters the ranked seeds down to describable targets and applies the cap.
func collect(topo *domain.Topology, seedRank map[string]int, opt PlanOptions) []Target {
	targetSet := helper.DescribeTargetSet(opt.Targets)
	out := make([]Target, 0, len(seedRank))
	for id, rank := range seedRank {
		res, ok := topo.Resources[id]
		if !ok {
			continue
		}
		if opt.Restrict != nil && !opt.Restrict[id] {
			continue
		}
		// One gate, shared with `descriptions generate` and `node_list_no_description`: a
		// resource this says no to is one no surface would have shown prose for anyway.
		if !helper.ShouldDescribe(res, targetSet, opt.Filter, opt.IncludeNotVisible) {
			continue
		}
		out = append(out, Target{ID: id, Name: res.Name, Kind: res.Kind, rank: rank})
	}
	// Rank first, then id: a stable order is what makes two identical reads describe the same
	// nodes, which is what makes the cap reproducible instead of arbitrary.
	sort.Slice(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		return out[i].ID < out[j].ID
	})
	if opt.MaxNodes > 0 && len(out) > opt.MaxNodes {
		out = out[:opt.MaxNodes]
	}
	return out
}

// TargetIDs is the ids of a plan, in plan order.
func TargetIDs(targets []Target) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.ID)
	}
	return out
}
