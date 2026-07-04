package domain

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ignoreRule is a single compiled scan.ignore pattern.
type ignoreRule struct {
	re *regexp.Regexp
	// dirOnly is set when the pattern ended with "/": it then matches only
	// directories (and, transitively, everything inside them), never a file by
	// its own name.
	dirOnly bool
	// anchored is set when the pattern contains a leading or internal "/": it is
	// then resolved relative to the topology root instead of floating so it can
	// match a path segment at any depth.
	anchored bool
}

// IgnoreMatcher matches filesystem paths against the scan.ignore glob patterns
// from the config. Patterns behave like a simplified .gitignore:
//
//   - "*" matches within a single path segment, "**" matches across segments,
//     "?" matches a single non-slash character;
//   - a trailing "/" restricts the pattern to directories (their whole subtree
//     is excluded);
//   - a pattern with a leading or internal "/" is anchored to the topology root;
//     one without floats and matches a segment at any depth (e.g. "*my_folder/"
//     excludes a directory ending in "my_folder" anywhere in the tree).
//
// Unlike .gitignore there is no "!" negation. IgnoreMatcher is nil-safe on all
// methods (a nil matcher excludes nothing).
type IgnoreMatcher struct {
	root  string
	rules []ignoreRule
}

// BuildIgnoreMatcher compiles patterns (typically cfg.Scan.Ignore) into a
// matcher scoped to root. root is resolved to an absolute path so it can be
// matched against the absolute paths produced by the scanner walks. Blank lines
// and "#" comments are skipped, and any pattern that fails to compile is dropped
// so a single malformed entry can never break scanning.
func BuildIgnoreMatcher(root string, patterns []string) *IgnoreMatcher {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	rules := make([]ignoreRule, 0, len(patterns))
	for _, raw := range patterns {
		p := strings.TrimSpace(raw)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		p = filepath.ToSlash(p)
		dirOnly := false
		if strings.HasSuffix(p, "/") {
			dirOnly = true
			p = strings.TrimRight(p, "/")
		}
		anchored := false
		if strings.HasPrefix(p, "/") {
			anchored = true
			p = strings.TrimPrefix(p, "/")
		}
		if strings.Contains(p, "/") {
			anchored = true
		}
		if p == "" {
			continue
		}
		re, err := globToRegexp(p)
		if err != nil {
			continue
		}
		rules = append(rules, ignoreRule{re: re, dirOnly: dirOnly, anchored: anchored})
	}
	return &IgnoreMatcher{root: absRoot, rules: rules}
}

// globToRegexp converts a glob pattern (with *, **, ?) to a compiled, fully
// anchored regexp. "**" spans path separators, "*" and "?" do not.
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '[', ']', '{', '}', '^', '$', '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// relPath returns p expressed relative to the matcher's root in forward-slash
// form, or "" when p is the root itself or lies outside it.
func (m *IgnoreMatcher) relPath(p string) string {
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(m.root, p)
	}
	rel, err := filepath.Rel(m.root, abs)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return rel
}

// matches reports whether rule r matches path segments segs. When includeLast is
// false the final segment (a file's basename) is not considered a match target,
// only its ancestor directories — this is how a dirOnly rule excludes a file
// that lives inside a matched directory without matching the file by its name.
// An anchored rule is tested against each root-relative prefix; a floating rule
// against each individual segment.
func (r ignoreRule) matches(segs []string, includeLast bool) bool {
	last := len(segs)
	if !includeLast {
		last--
	}
	if last <= 0 {
		return false
	}
	if r.anchored {
		for k := 1; k <= last; k++ {
			if r.re.MatchString(strings.Join(segs[:k], "/")) {
				return true
			}
		}
		return false
	}
	for i := 0; i < last; i++ {
		if r.re.MatchString(segs[i]) {
			return true
		}
	}
	return false
}

// Match reports whether the file at path p is excluded by any pattern: either p
// itself matches a (non-dir-only) pattern, or p lives inside a directory that a
// pattern matches. It is false when no pattern applies or no matcher is set.
func (m *IgnoreMatcher) Match(p string) bool {
	if m == nil || len(m.rules) == 0 {
		return false
	}
	rel := m.relPath(p)
	if rel == "" {
		return false
	}
	segs := strings.Split(rel, "/")
	for _, r := range m.rules {
		// A dir-only rule may only match p's ancestor directories, never the
		// file's own basename; other rules may match the basename too.
		if r.matches(segs, !r.dirOnly) {
			return true
		}
	}
	return false
}

// MatchDir reports whether the directory dir (and thus its whole subtree) is
// excluded by any pattern, allowing a walk to skip it wholesale. Because dir is
// a directory, every rule — including dir-only ones — may match its basename.
func (m *IgnoreMatcher) MatchDir(dir string) bool {
	if m == nil || len(m.rules) == 0 {
		return false
	}
	rel := m.relPath(dir)
	if rel == "" {
		return false
	}
	segs := strings.Split(rel, "/")
	for _, r := range m.rules {
		if r.matches(segs, true) {
			return true
		}
	}
	return false
}

// Process-global active scan.ignore matcher, mirroring the path-visibility
// filter above: the topology manager installs it from config before file
// discovery and parsing so the deeply nested, config-unaware walk functions can
// consult it (via PathHidden / PathPruneDir) without threading it through every
// signature.
var (
	activeIgnoreMu sync.RWMutex
	activeIgnore   *IgnoreMatcher
)

// SetActiveIgnore installs m as the active scan.ignore matcher consulted by
// PathHidden / PathPruneDir. Pass nil to disable ignore matching.
func SetActiveIgnore(m *IgnoreMatcher) {
	activeIgnoreMu.Lock()
	activeIgnore = m
	activeIgnoreMu.Unlock()
}

func activeIgnoreMatcher() *IgnoreMatcher {
	activeIgnoreMu.RLock()
	defer activeIgnoreMu.RUnlock()
	return activeIgnore
}
