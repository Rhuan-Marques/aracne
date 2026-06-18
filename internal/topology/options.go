package topology

import "aracne/internal/topology/domain"

// Filtering options for ReadAll operations, supporting resource kind filtering and description presence filtering.
type TopologyOptions struct {
	resourceFilter map[domain.ResourceKind]bool
	hasDescription *bool
	contextFilter  *domain.ContextFilter
}

type TopologyOption func(*TopologyOptions)

// Returns a TopologyOption that filters ReadAll results to only include resources matching the given ResourceKind values.
func WithResourceFilter(resources ...domain.ResourceKind) TopologyOption {
	return func(opts *TopologyOptions) {
		opts.resourceFilter = make(map[domain.ResourceKind]bool, len(resources))
		for _, r := range resources {
			opts.resourceFilter[r] = true
		}
	}
}

// Returns a TopologyOption that filters resources based on whether they have a non-empty description. When true, only resources with descriptions are returned; when false, only undocumented resources.
func WithHasDescription(has bool) TopologyOption {
	return func(opts *TopologyOptions) {
		opts.hasDescription = &has
	}
}

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
