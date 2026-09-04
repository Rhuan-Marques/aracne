// Package universaltools holds the language-agnostic front end to the topology read path: one
// `read` tool (read.go) plus the ID resolution every read goes through.
package universaltools

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/idresolve"
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

// fileSuffixKind is what a trailing ":something" on a file-shaped id turned out to mean.
type fileSuffixKind int

const (
	suffixNone fileSuffixKind = iota
	// suffixSymbol is `path/to/file.rs:SymbolName` -- a symbol lookup scoped to one file.
	suffixSymbol
	// suffixRange is `path/to/file.rs:120-160` -- a line window.
	suffixRange
)

// splitFileSuffix recognizes the two id shapes a model invents when it wants part of a file:
// `src/app.rs:build_app` and `src/app.rs:95-115`. Every one of them missed in the benchmark
// run, and between them they were four of its six id misses.
//
// Splitting on a colon is safe because no id scheme in this codebase uses a lone one: Rust and
// C++ use `::`, which idresolve's tokenizer rewrites to `/` before it ever splits, and Java
// signatures use `#` and `$`. So the LAST colon that is not part of a `::` pair is ours.
func splitFileSuffix(id string) (base string, kind fileSuffixKind, start, end int, symbol string) {
	i := -1
	for j := len(id) - 1; j >= 0; j-- {
		if id[j] != ':' {
			continue
		}
		if (j > 0 && id[j-1] == ':') || (j+1 < len(id) && id[j+1] == ':') {
			j-- // part of a "::" pair, and so is its partner
			continue
		}
		i = j
		break
	}
	if i <= 0 || i == len(id)-1 {
		return id, suffixNone, 0, 0, ""
	}
	// A bare Windows drive letter is not a separator. A real suffix after a drive-lettered
	// path has a second colon later on, which the scan above already preferred.
	if i == 1 && len(id) > 2 && isASCIILetter(id[0]) {
		return id, suffixNone, 0, 0, ""
	}
	head, tail := id[:i], id[i+1:]
	if !looksLikeFilePath(head) {
		return id, suffixNone, 0, 0, ""
	}
	if a, b, ok := parseLineRange(tail); ok {
		return head, suffixRange, a, b, ""
	}
	if isIdentifierPath(tail) {
		return head, suffixSymbol, 0, 0, tail
	}
	return id, suffixNone, 0, 0, ""
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// looksLikeFilePath keeps the split away from ids that merely end in something colon-shaped:
// only a path with a separator or a file extension can carry one of our suffixes.
func looksLikeFilePath(s string) bool {
	if strings.ContainsAny(s, "/\\") {
		return true
	}
	dot := strings.LastIndex(s, ".")
	return dot > 0 && dot < len(s)-1 && !strings.Contains(s[dot+1:], " ")
}

func parseLineRange(s string) (int, int, bool) {
	dash := strings.IndexByte(s, '-')
	if dash <= 0 || dash == len(s)-1 {
		return 0, 0, false
	}
	a, errA := strconv.Atoi(s[:dash])
	b, errB := strconv.Atoi(s[dash+1:])
	if errA != nil || errB != nil {
		return 0, 0, false
	}
	return a, b, true
}

// isIdentifierPath accepts the shapes a scoped symbol reference takes -- `build_app`,
// `Type.method`, `Type::method` -- and nothing with spaces or punctuation that could not be a
// name.
//
// `::` is here because rejecting it silently broke the very form this suffix exists to serve.
// A model asked for `serde_derive/src/de.rs:Parameters::new`; the tail failed this test, so
// the whole string was looked up as one id, the file scope was lost, and it came back
// "Multiple resources matching" with five unrelated `::new` methods from across the crate.
// The scoping was right there in the id and was thrown away.
func isIdentifierPath(s string) bool {
	if s == "" || (!isASCIILetter(s[0]) && s[0] != '_') {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case isASCIILetter(c), c >= '0' && c <= '9', c == '_', c == '.':
		case c == ':':
			// Only as part of a `::` pair: a lone colon would mean splitFileSuffix cut in
			// the wrong place, and re-splitting here would hide that rather than fix it.
			if i+1 < len(s) && s[i+1] == ':' {
				i++
				continue
			}
			return false
		default:
			return false
		}
	}
	return true
}

// declaredIn reports whether a resource lives in the file named by `base`, comparing the same
// normalize-then-absolute way resolveReadTargetWith matches an exact id, so a relative id and
// the topology's absolute path still meet.
func declaredIn(base string, res domain.Resource) bool {
	want := helper.NormalizeResourceID(base)
	got := res.Location.Path
	if got == "" {
		return false
	}
	if got == want || strings.HasSuffix(filepath.ToSlash(got), "/"+filepath.ToSlash(want)) {
		return true
	}
	if abs, err := filepath.Abs(want); err == nil {
		return got == abs
	}
	return false
}

// nodesSpanning names the resources covering a line window, so a `file:120-160` id can answer
// with the ID to read instead of a dead end.
//
// The window form is deliberately NOT served. Read.Parameters records why there is no
// start_line/end_line parameter: walking a file in ranges was the most expensive habit an
// earlier benchmark found, at a turn per window. Honouring `:120-160` would reintroduce it by
// the back door. Naming the enclosing node costs the same single turn a miss already costs,
// and leaves the model holding an id it can batch next time.
func nodesSpanning(topo *domain.Topology, base string, start, end int) []string {
	var hits []string
	for id, res := range topo.Resources {
		if res.Kind == domain.ResourceFile || !declaredIn(base, res) {
			continue
		}
		if res.Location.StartsAt <= end && res.Location.EndsAt >= start {
			hits = append(hits, id)
		}
	}
	sort.Strings(hits)
	if len(hits) > 6 {
		hits = hits[:6]
	}
	return hits
}

// rangeRedirect turns `path/to/file.rs:120-160` into the error the model can act on: the ids
// of the resources covering those lines, or a plain miss when nothing does.
func rangeRedirect(topo *domain.Topology, id, base string, from, to int) error {
	if from < 1 || to < from {
		return fmt.Errorf("resource %q not found in topology: %d-%d is not a line range", id, from, to)
	}
	hits := nodesSpanning(topo, base, from, to)
	if len(hits) == 0 {
		return fmt.Errorf("resource %q not found in topology: nothing is declared at lines %d-%d of %s",
			id, from, to, base)
	}
	return fmt.Errorf("resource %q not found in topology. Lines %d-%d of %s are inside: %s. "+
		"Pass the id -- reading by resource is cheaper than walking a file in line windows",
		id, from, to, base, strings.Join(hits, ", "))
}

// ReadWindow answers a request for a LINE RANGE of a file with the resources that cover it.
//
// WHY THIS EXISTS. The tool guard can answer a denied `sed -n 660,760p args.rs` instead of
// merely refusing it, but until now it answered by reading the WHOLE file: soleReadTarget
// pulled the path out of the command and threw the window away. Measured over
// compact-blocked-after-bs-20260830c, that served 281,925 bytes against ~48,800 asked for --
// 5.8x on average and 41.9x at worst, where a 40-line `awk` window returned an entire 59KB
// test file. The read tool has always known how to map a window to the ids covering it
// (nodesSpanning, used by the `file.rs:120-160` id form); the guard just had no way in.
//
// Returns "" with a nil error when nothing is declared across those lines, which is a real
// answer -- imports, a license header, a data table -- and tells the caller to fall back to
// the whole-file read rather than invent something.
func (r *Read) ReadWindow(path string, from, to int, opt ReadIDsOptions) (string, error) {
	if from < 1 || to < from {
		return "", nil
	}
	topo, err := r.mgr.ReadAll()
	if err != nil {
		return "", err
	}
	ids := nodesSpanning(topo, path, from, to)
	if len(ids) == 0 {
		return "", nil
	}
	return r.ReadIDs(ids, opt)
}
