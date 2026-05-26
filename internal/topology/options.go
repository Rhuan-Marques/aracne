package topology

import "llm-topology/internal/topology/domain"

type TopologyOptions struct {
	resourceFilter map[domain.ResourceKind]bool
	hasDescription *bool
}

type TopologyOption func(*TopologyOptions)

func WithResourceFilter(resources ...domain.ResourceKind) TopologyOption {
	return func(opts *TopologyOptions) {
		opts.resourceFilter = make(map[domain.ResourceKind]bool, len(resources))
		for _, r := range resources {
			opts.resourceFilter[r] = true
		}
	}
}

func WithHasDescription(has bool) TopologyOption {
	return func(opts *TopologyOptions) {
		opts.hasDescription = &has
	}
}

func (o *TopologyOptions) HasResource(r domain.ResourceKind) bool {
	if o == nil || o.resourceFilter == nil {
		return true
	}
	return o.resourceFilter[r]
}
