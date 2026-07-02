package domain

import (
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// PathRule marks a path (relative to the topology root) as hidden or visible to
// the topology. Hidden paths are skipped by both the indexing stage (file
// discovery / manifest) and the scan stage (parsing) in every scan mode. More
// specific (more internal) rules win, so a parent directory can be hidden while
// a nested child stays visible: hiding "my_example" but leaving
// "my_example/another_layer" visible keeps the deeper files in the topology.
type PathRule struct {
	// Path is a directory (or file) path relative to the topology root, using
	// forward slashes. It is normalized when a PathVisibility is built.
	Path string `json:"path"`
	// Hidden reports whether the path (and everything under it, unless a more
	// specific rule says otherwise) is excluded from the topology.
	Hidden bool `json:"hidden"`
}

// PathVisibility resolves whether a filesystem path is hidden from the topology
// from an ordered set of PathRules. Rules are pre-sorted most-specific first so
// the closest (most internal) ancestor rule decides a path's visibility.
type PathVisibility struct {
	root  string
	rules []PathRule
}

// BuildPathVisibility normalizes rules against root and returns a matcher. root
// is resolved to an absolute path so it can be matched against the absolute
// paths produced by the scanner walks. Rules with empty or escaping ("..")
// paths are dropped. The result is safe to use even with no rules (nothing is
// hidden) and is nil-safe on all methods.
func BuildPathVisibility(root string, rules []PathRule) *PathVisibility {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	norm := make([]PathRule, 0, len(rules))
	for _, r := range rules {
		np := normalizeRulePath(r.Path)
		if np == "" {
			continue
		}
		norm = append(norm, PathRule{Path: np, Hidden: r.Hidden})
	}
	// Most specific first: more path segments win, then longer strings. A stable
	// sort keeps the first-declared rule ahead on exact ties.
	sort.SliceStable(norm, func(i, j int) bool {
		si, sj := strings.Count(norm[i].Path, "/"), strings.Count(norm[j].Path, "/")
		if si != sj {
			return si > sj
		}
		return len(norm[i].Path) > len(norm[j].Path)
	})
	return &PathVisibility{root: absRoot, rules: norm}
}

// normalizeRulePath cleans a user-supplied rule path to a root-relative,
// forward-slash form with no leading "./" or surrounding slashes. It returns ""
// for empty, current-dir, or root-escaping paths, which are then ignored.
func normalizeRulePath(p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	if p == "" || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

// relPath returns p expressed relative to the matcher's root in forward-slash
// form, or "" when p is the root itself or lies outside it.
func (pv *PathVisibility) relPath(p string) string {
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(pv.root, p)
	}
	rel, err := filepath.Rel(pv.root, abs)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return rel
}

// Hidden reports whether p is hidden by the closest (most internal) matching
// rule. Paths with no matching rule, and paths outside the root, are visible.
func (pv *PathVisibility) Hidden(p string) bool {
	if pv == nil || len(pv.rules) == 0 {
		return false
	}
	rel := pv.relPath(p)
	if rel == "" {
		return false
	}
	for _, r := range pv.rules {
		if rel == r.Path || strings.HasPrefix(rel, r.Path+"/") {
			return r.Hidden
		}
	}
	return false
}

// PruneDir reports whether a directory subtree can be skipped wholesale during a
// walk: the directory is hidden and no more-specific rule below it could reveal
// a descendant. When a nested rule exists the walk must descend and filter at
// the file level so the more-internal rule can take effect.
func (pv *PathVisibility) PruneDir(dir string) bool {
	if pv == nil || len(pv.rules) == 0 {
		return false
	}
	rel := pv.relPath(dir)
	if rel == "" {
		return false
	}
	if !pv.Hidden(dir) {
		return false
	}
	prefix := rel + "/"
	for _, r := range pv.rules {
		if strings.HasPrefix(r.Path, prefix) {
			return false
		}
	}
	return true
}

// Process-global active path-visibility filter. Scans are driven centrally (the
// topology manager installs the filter from config before file discovery and
// parsing), so a package global lets the deeply nested, config-unaware walk
// functions consult it without threading it through every signature. The filter
// is root-scoped, so a stale filter from another root simply matches nothing.
var (
	activePathVisMu sync.RWMutex
	activePathVis   *PathVisibility
)

// SetActivePathVisibility installs pv as the active filter consulted by
// PathHidden / PathPruneDir. Pass nil to disable filtering.
func SetActivePathVisibility(pv *PathVisibility) {
	activePathVisMu.Lock()
	activePathVis = pv
	activePathVisMu.Unlock()
}

func activePathVisibility() *PathVisibility {
	activePathVisMu.RLock()
	defer activePathVisMu.RUnlock()
	return activePathVis
}

// PathHidden reports whether p is hidden by the active path-visibility filter.
// It is false (nothing hidden) when no filter is installed.
func PathHidden(p string) bool {
	return activePathVisibility().Hidden(p)
}

// PathPruneDir reports whether the active filter allows skipping a directory
// subtree wholesale during a walk (see PathVisibility.PruneDir).
func PathPruneDir(dir string) bool {
	return activePathVisibility().PruneDir(dir)
}
