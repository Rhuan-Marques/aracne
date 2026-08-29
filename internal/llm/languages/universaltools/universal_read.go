// Package universaltools holds the language-agnostic front end to the topology read path: one
// `read` tool (read.go) plus the ID resolution every read goes through.
package universaltools

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/idresolve"
)

// Holds a resolved resource ID and its domain.Resource metadata for read operations.
type readTarget struct {
	id  string
	res domain.Resource
}

// filterOption builds the context-block visibility option from the project config so neighbor
// rendering honors read.context_filter.
func filterOption(mgr *topology.TopologyManager) topology.TopologyOption {
	cfg := helper.LoadConfig(helper.ConfigPath(mgr.DbPath()))
	return topology.WithContextFilter(cfg.EffectiveContextFilter())
}

// Resolves a resource name to a readTarget using a custom match predicate, with fallback language handling.
func resolveReadTargetWith(mgr *topology.TopologyManager, name string, matches func(domain.Resource) bool) (readTarget, string, error) {
	topo, err := mgr.ReadAll()
	if err != nil {
		return readTarget{}, "", fmt.Errorf("read topology: %w", err)
	}
	fallbackLanguage := topo.Language
	if fallbackLanguage == "multi" {
		fallbackLanguage = ""
	}
	candidates := make([]readTarget, 0)
	tryID := func(id string) bool {
		res, ok := topo.Resources[id]
		if !ok || !matches(res) {
			return false
		}
		if res.Language == "" {
			res.Language = fallbackLanguage
		}
		candidates = append(candidates, readTarget{id: id, res: res})
		return true
	}
	id := helper.NormalizeResourceID(name)
	if tryID(id) {
		return candidates[0], "", nil
	}
	if abs, absErr := filepath.Abs(id); absErr == nil && abs != id && tryID(abs) {
		return candidates[0], "", nil
	}
	// Beyond an exact ID, hand the query to the shared resolver: it absorbs a wrong root
	// prefix and the wrong separator convention (the Python/JS "worktree/src/flask/app.X"
	// vs "flask.app.X" problem), consults the alias table for IDs minted under a previous
	// id-scheme, and — on a miss — returns ranked suggestions instead of a dead end. A
	// wrong guess should cost the model a correction, not a whole extra exploration turn.
	res := idresolve.Resolve(topo, name, idresolve.Options{
		Filter: matches,
		Alias:  func(old string) (string, bool) { return helper.ResolveAlias(mgr.DbPath(), old) },
	})
	switch {
	case res.Found():
		target := res.Resource
		if target.Language == "" {
			target.Language = fallbackLanguage
		}
		return readTarget{id: res.ID, res: target}, "", nil
	case res.Tier == idresolve.TierAmbiguous:
		for _, c := range res.Candidates {
			r := topo.Resources[c.ID]
			if r.Language == "" {
				r.Language = fallbackLanguage
			}
			candidates = append(candidates, readTarget{id: c.ID, res: r})
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })
		return readTarget{}, ambiguousTargets(name, candidates), nil
	}
	if hint := idresolve.FormatCandidates(name, res.Candidates); hint != "" {
		return readTarget{}, "", fmt.Errorf("resource %q not found in topology. %s", name, hint)
	}
	return readTarget{}, "", fmt.Errorf("resource %q not found in topology", name)
}

// Formats an error message listing multiple resource candidates matching a name query.
func ambiguousTargets(name string, candidates []readTarget) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Multiple resources matching %q found:\n", name))
	for _, target := range candidates {
		b.WriteString(fmt.Sprintf("- %s (%s, %s)\n", target.id, target.res.Language, target.res.Kind))
	}
	return b.String()
}

// Extracts a boolean property value from a resource, defaulting to false if missing or not a bool.
func boolProp(res domain.Resource, key string) bool {
	value, ok := res.Properties[key]
	if !ok || value == nil {
		return false
	}
	b, ok := value.(bool)
	return ok && b
}
