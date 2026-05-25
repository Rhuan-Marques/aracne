package topology

import "llm-topology/internal/topology/domain"

type TopologyOptions struct {
	resourceFilter map[domain.ResourceName]bool
}

type TopologyOption func(*TopologyOptions)

func WithResourceFilter(resources ...domain.ResourceName) TopologyOption {
	return func(opts *TopologyOptions) {
		opts.resourceFilter = make(map[domain.ResourceName]bool, len(resources))
		for _, r := range resources {
			opts.resourceFilter[r] = true
		}
	}
}

func (o *TopologyOptions) hasResource(r domain.ResourceName) bool {
	if o == nil || o.resourceFilter == nil {
		return true
	}
	return o.resourceFilter[r]
}
