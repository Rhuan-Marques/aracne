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
//     "?" matches a single non-slash character, "[...]" a bracket class ("[!...]"
//     negates), and "\" escapes the next character;
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
		// NOT filepath.ToSlash. A .gitignore pattern is forward-slash on every platform and
		// `\` is its ESCAPE character -- the only way to spell a literal `[`, `*` or `?`. On
		// Windows ToSlash rewrote those escapes into separators (`s\[1].go` became
		// `s/[1].go`, `\[slug\]` became `/[slug/]`), so every bracket or literal-glob rule
		// copied from a real .gitignore silently matched nothing there.
		dirOnly := false
		anchored := false
		// `X/**` means "everything under X", which is what a directory rule already
		// means -- and spelling it as a regex instead left MatchDir unable to prune X
		// itself, so a walk descended into an ignored tree to reject it file by file.
		//
		// The slash before `**` is a MIDDLE slash, and in .gitignore a middle slash anchors
		// the pattern to the root. It has to be counted before it is stripped: `foo/**`
		// became the floating `foo/`, which ignored every nested directory named foo.
		if strings.HasSuffix(p, "/**") {
			dirOnly = true
			anchored = true
			p = strings.TrimSuffix(p, "/**")
		}
		if strings.HasSuffix(p, "/") {
			dirOnly = true
			p = strings.TrimRight(p, "/")
		}
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
		re, err := GlobToRegexp(p)
		if err != nil {
			continue
		}
		rules = append(rules, ignoreRule{re: re, dirOnly: dirOnly, anchored: anchored})
	}
	return &IgnoreMatcher{root: absRoot, rules: rules}
}

// GlobToRegexp converts a glob pattern (with *, **, ?, [...] and \ escapes) to a compiled, fully
// anchored regexp. "**" spans path separators, "*", "?" and a bracket class do not.
//
// A "**" FOLLOWED BY A SLASH MATCHES ZERO DIRECTORIES, which is what `.gitignore` means by
// it and what the configuration reference promises. Compiling `**/` to `.*/` instead required
// at least one directory above the match, so `**/node_modules/` skipped a nested
// node_modules and silently indexed the one at the repository root -- the failure in the
// direction that costs most, since the tree the project asked to skip got scanned anyway.
//
// Exported because the chat glob tool needs exactly this compiler and had grown a
// byte-identical private copy, carrying the same bug: `**/*.go` there never matched a file at
// the search root.
func GlobToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?") // "**/" spans zero or more directories
					i += 2
					continue
				}
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			// A bracket expression, as in .gitignore and every shell glob: `s[12].go`,
			// `[a-c]*`, `[!0-9]`. Treated as a literal "[" when it never closes.
			if class, next, ok := bracketClass(pattern, i); ok {
				b.WriteString(class)
				i = next
				continue
			}
			b.WriteString(`\[`)
		case '\\':
			// An escape, as in .gitignore: `\[id\].js` names a file with literal brackets,
			// which bracket classes would otherwise read as a set.
			if i+1 < len(pattern) {
				i++
				b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
				continue
			}
			b.WriteString(`\\`)
		case '.', '+', '(', ')', '|', ']', '{', '}', '^', '$':
			b.WriteByte('\\')
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// bracketClass compiles the glob bracket expression that opens at pattern[open] into a regexp
// character class, returning it and the index of its closing "]". ok is false when the bracket
// never closes.
//
// The syntax is the one .gitignore (git's wildmatch) and fnmatch share: a leading "!" or "^"
// negates, a "]" right after the opening (or after the negation) is a literal member, "a-z" is
// a range, "\" escapes the next character, and "[:alpha:]"-style POSIX classes are passed
// through. A class never matches "/", negated or not, since a glob character matches within
// one path segment; a literal "/" member is dropped for the same reason.
func bracketClass(pattern string, open int) (class string, closeIdx int, ok bool) {
	j := open + 1
	negate := false
	if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
		negate = true
		j++
	}
	start := j
	if j < len(pattern) && pattern[j] == ']' {
		j++
	}
	for j < len(pattern) && pattern[j] != ']' {
		if pattern[j] == '\\' && j+1 < len(pattern) {
			j += 2
		} else if end := posixClassEnd(pattern, j); end > j {
			j = end
		} else {
			j++
		}
	}
	if j >= len(pattern) {
		return "", 0, false
	}
	body := pattern[start:j]
	var b strings.Builder
	b.WriteString("[")
	if negate {
		b.WriteString("^/")
	}
	for k := 0; k < len(body); k++ {
		ch := body[k]
		switch {
		case ch == '\\' && k+1 < len(body):
			k++
			writeClassByte(&b, body[k])
		case posixClassEnd(body, k) > k:
			end := posixClassEnd(body, k)
			b.WriteString(body[k:end])
			k = end - 1
		case ch == '-' && k > 0 && k+1 < len(body):
			b.WriteByte('-') // a range operator between two members
		default:
			writeClassByte(&b, ch)
		}
	}
	b.WriteString("]")
	return b.String(), j, true
}

// posixClassEnd returns the index just past a "[:name:]" POSIX class starting at s[i], or i when
// none starts there.
func posixClassEnd(s string, i int) int {
	if !strings.HasPrefix(s[i:], "[:") {
		return i
	}
	end := strings.Index(s[i+2:], ":]")
	if end < 0 {
		return i
	}
	return i + 2 + end + 2
}

// writeClassByte writes one member byte of a bracket expression as a literal inside a regexp
// class: ASCII punctuation is backslash-escaped, "/" is dropped (see bracketClass), and letters,
// digits and the bytes of a multi-byte character are written as they are.
func writeClassByte(b *strings.Builder, ch byte) {
	switch {
	case ch == '/':
	case ch < 0x80 && !('a' <= ch && ch <= 'z' || 'A' <= ch && ch <= 'Z' || '0' <= ch && ch <= '9'):
		b.WriteByte('\\')
		b.WriteByte(ch)
	default:
		b.WriteByte(ch)
	}
}

// relPath returns p expressed relative to the matcher's root in forward-slash
// form, or "" when p is the root itself or lies outside it.
//
// A RELATIVE PATH IS RESOLVED AGAINST THE PROCESS, NOT AGAINST THE ROOT. Every caller hands
// this the output of a real directory walk, so a relative path is relative to the working
// directory the walk started from -- which is the root only when the caller happens to be
// standing in it. Joining it onto m.root instead was right in that one case and wrong in every
// other: an intercepted `grep -rn x ../../` run from a subdirectory produced
// `../../generated/gen.go`, which joined to `<root>/../../generated/gen.go`, rel'd back to a
// `../` chain, and returned "" -- read by every rule as "outside the root, nothing applies". So
// scan.ignore silently stopped applying the moment the agent's shell was not at the project
// root, and ignored trees reappeared in searches carrying no annotation and no node.
//
// filepath.Abs agrees with the old behaviour whenever the walk root WAS the working directory,
// which is every other call site; it only differs where the join was already producing a path
// that does not exist.
func (m *IgnoreMatcher) relPath(p string) string {
	abs := p
	if !filepath.IsAbs(p) {
		var err error
		if abs, err = filepath.Abs(p); err != nil {
			abs = filepath.Join(m.root, p)
		}
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
