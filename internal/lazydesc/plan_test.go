package lazydesc

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// fixture builds a small graph:
//
//	caller  --calls-->  seed  --calls-->  callee (undescribed)
//	                        \--calls-->   described (already has prose)
//	                        \--uses_struct--> Thing (undescribed)
//	file --has_function--> member (undescribed)
func fixture() *domain.Topology {
	fn := func(id string, conns map[string][]string) domain.Resource {
		return domain.Resource{
			ID: id, Name: id, Kind: domain.ResourceFunction, Language: "go",
			Location:    domain.Location{Path: "a.go", StartsAt: 1, EndsAt: 30},
			Connections: conns,
		}
	}
	return &domain.Topology{Resources: map[string]domain.Resource{
		"seed": fn("seed", map[string][]string{
			"calls":           {"callee", "described"},
			"uses_struct":     {"Thing"},
			"uses_named_type": {"missing-from-graph"},
		}),
		"callee":    fn("callee", nil),
		"caller":    fn("caller", map[string][]string{"calls": {"seed"}}),
		"described": withDesc(fn("described", nil), "already documented"),
		"Thing": {
			ID: "Thing", Name: "Thing", Kind: domain.ResourceStruct, Language: "go",
			Location: domain.Location{Path: "a.go", StartsAt: 40, EndsAt: 45},
		},
		"file": {
			ID: "file", Name: "a.go", Kind: domain.ResourceFile, Language: "go",
			Location:    domain.Location{Path: "a.go", StartsAt: 1, EndsAt: 100},
			Connections: map[string][]string{"has_function": {"member"}},
		},
		"member": fn("member", nil),
	}}
}

func withDesc(r domain.Resource, d string) domain.Resource {
	r.Description = d
	return r
}

func defaultOptions() PlanOptions {
	return PlanOptions{
		Targets: helper.DefaultNeedDescription(),
		Filter:  domain.DefaultContextFilter(),
	}
}

func ids(targets []Target) []string { return TargetIDs(targets) }

func TestReadTargetsAreTheNeighboursNotTheRequest(t *testing.T) {
	got := ids(ReadTargets(fixture(), []string{"seed"}, defaultOptions()))
	want := []string{"Thing", "callee"} // outgoing rank, then by id
	if !equal(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

// The resource being read is not described by its own read: its body IS the answer, and its
// description is not printed anywhere in the response.
func TestReadTargetsExcludeTheRequestedResources(t *testing.T) {
	topo := fixture()
	// Make the seed itself undescribed and describable -- it already is -- and confirm it
	// still never appears.
	for _, id := range ids(ReadTargets(topo, []string{"seed"}, defaultOptions())) {
		if id == "seed" {
			t.Fatal("the requested resource must not be a lazy target")
		}
	}
}

func TestReadTargetsSkipAlreadyDescribed(t *testing.T) {
	for _, id := range ids(ReadTargets(fixture(), []string{"seed"}, defaultOptions())) {
		if id == "described" {
			t.Fatal("a resource that already has a description is not a target")
		}
	}
}

func TestReadTargetsSkipUnknownIDs(t *testing.T) {
	for _, id := range ids(ReadTargets(fixture(), []string{"seed"}, defaultOptions())) {
		if id == "missing-from-graph" {
			t.Fatal("an edge pointing outside the graph is not a target")
		}
	}
}

// A whole-file read lists what the file declares, so those members are targets too -- but at a
// lower rank than a direct reference, because a file read is the request most likely to name a
// hundred nodes at once.
func TestReadTargetsIncludeContainment(t *testing.T) {
	got := ids(ReadTargets(fixture(), []string{"file"}, defaultOptions()))
	if !equal(got, []string{"member"}) {
		t.Fatalf("targets = %v, want [member]", got)
	}
}

// "# USED BY:" only exists when read.context_filter.include_incoming is on, so the resources
// that feed it are only worth describing then.
func TestReadTargetsIncomingFollowsTheSection(t *testing.T) {
	opt := defaultOptions()
	if containsID(ids(ReadTargets(fixture(), []string{"seed"}, opt)), "caller") {
		t.Fatal("incoming references should not be targets with include_incoming off")
	}
	opt.IncludeIncoming = true
	if !containsID(ids(ReadTargets(fixture(), []string{"seed"}, opt)), "caller") {
		t.Fatal("incoming references should be targets with include_incoming on")
	}
}

// The rank order is what makes the cap meaningful: what a cap drops must be what the answer is
// least likely to print.
func TestReadTargetsRankOutgoingBeforeIncoming(t *testing.T) {
	opt := defaultOptions()
	opt.IncludeIncoming = true
	got := ids(ReadTargets(fixture(), []string{"seed"}, opt))
	want := []string{"Thing", "callee", "caller"}
	if !equal(got, want) {
		t.Fatalf("targets = %v, want %v (outgoing before incoming)", got, want)
	}
}

func TestReadTargetsRespectMaxNodes(t *testing.T) {
	opt := defaultOptions()
	opt.IncludeIncoming = true
	opt.MaxNodes = 2
	got := ids(ReadTargets(fixture(), []string{"seed"}, opt))
	if !equal(got, []string{"Thing", "callee"}) {
		t.Fatalf("targets = %v, want the two highest-ranked", got)
	}
	// 0 means no cap, which is a setting a project can reasonably choose.
	opt.MaxNodes = 0
	if n := len(ReadTargets(fixture(), []string{"seed"}, opt)); n != 3 {
		t.Fatalf("max_nodes 0 capped at %d, want no cap", n)
	}
}

func TestReadTargetsRespectDescriptionKinds(t *testing.T) {
	opt := defaultOptions()
	opt.Targets = []domain.ResourceKind{domain.ResourceStruct}
	got := ids(ReadTargets(fixture(), []string{"seed"}, opt))
	if !equal(got, []string{"Thing"}) {
		t.Fatalf("targets = %v, want only the struct", got)
	}
}

// A neighbour the context filter renders as full code, or hides outright, shows no prose --
// so generating prose for it would be paid for and never printed.
func TestReadTargetsRespectContextVisibility(t *testing.T) {
	topo := fixture()
	// A five-line function is "small" under the default threshold.
	small := topo.Resources["callee"]
	small.Location.EndsAt = small.Location.StartsAt + 2
	topo.Resources["callee"] = small

	opt := defaultOptions()
	opt.Filter.SmallFnVisibility = domain.VisibilityHidden
	if containsID(ids(ReadTargets(topo, []string{"seed"}, opt)), "callee") {
		t.Fatal("a hidden small function should not be described")
	}
	opt.Filter.SmallFnVisibility = domain.VisibilityFull
	if containsID(ids(ReadTargets(topo, []string{"seed"}, opt)), "callee") {
		t.Fatal("a small function rendered as full code should not be described")
	}
	// ...unless the project asked for those too.
	opt.IncludeNotVisible = true
	if !containsID(ids(ReadTargets(topo, []string{"seed"}, opt)), "callee") {
		t.Fatal("include_not_visible should bring it back")
	}
}

func TestReadTargetsRestrictBoundsThePlan(t *testing.T) {
	opt := defaultOptions()
	opt.Restrict = map[string]bool{"Thing": true}
	got := ids(ReadTargets(fixture(), []string{"seed"}, opt))
	if !equal(got, []string{"Thing"}) {
		t.Fatalf("targets = %v, want only the restricted id", got)
	}
	// An empty (but non-nil) restriction means "nothing this answer will print".
	opt.Restrict = map[string]bool{}
	if n := len(ReadTargets(fixture(), []string{"seed"}, opt)); n != 0 {
		t.Fatalf("an empty restriction planned %d targets, want 0", n)
	}
}

// The search case is the mirror image: the node itself is the row, so the node itself is the
// target.
func TestNodeTargetsAreTheNodesThemselves(t *testing.T) {
	got := ids(NodeTargets(fixture(), []string{"seed", "described", "callee", "callee"}, defaultOptions()))
	if !equal(got, []string{"callee", "seed"}) {
		t.Fatalf("targets = %v, want the undescribed nodes, deduped", got)
	}
}

func TestPlannersTolerateNoTopology(t *testing.T) {
	if n := len(ReadTargets(nil, []string{"seed"}, defaultOptions())); n != 0 {
		t.Fatal("a nil topology should plan nothing")
	}
	if n := len(NodeTargets(fixture(), nil, defaultOptions())); n != 0 {
		t.Fatal("no ids should plan nothing")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsID(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}
