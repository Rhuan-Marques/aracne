package topology

import "github.com/Rhuan-Marques/aracne/internal/topology/domain"

// Filtering options for ReadAll operations: which resource kinds are included, and how much
// of a neighbour a context block renders.
//
// It used to claim a third, "description presence filtering", on the strength of a
// hasDescription field that nothing ever set or read. Describing a filter the struct does not
// apply is worse than not having it: the real one is ContextFilter.HideNoDescription, and a
// reader looking for it here found a field that agreed with them and did nothing.
type TopologyOptions struct {
	resourceFilter map[domain.ResourceKind]bool
	contextFilter  *domain.ContextFilter
}

type TopologyOption func(*TopologyOptions)

// Checks whether a given ResourceKind is included in the resource filter. Returns true if no filter is set or the kind is present in the filter map.
func (o *TopologyOptions) HasResource(r domain.ResourceKind) bool {
	if o == nil || o.resourceFilter == nil {
		return true
	}
	return o.resourceFilter[r]
}

// WithContextFilter returns a TopologyOption that sets the context-block
// visibility filter used by the read managers when assembling neighbor lists.
func WithContextFilter(f domain.ContextFilter) TopologyOption {
	return func(opts *TopologyOptions) {
		cf := f
		opts.contextFilter = &cf
	}
}

// ContextFilter returns the configured context filter, or the all-Normal
// default (current behavior) when none was set.
func (o *TopologyOptions) ContextFilter() domain.ContextFilter {
	if o == nil || o.contextFilter == nil {
		return domain.DefaultContextFilter()
	}
	return *o.contextFilter
}
