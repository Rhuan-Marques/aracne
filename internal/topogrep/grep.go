// Package topogrep is content search that annotates hits with the topology resource
// they fall inside.
//
// SIZING MATTERS HERE. This is the highest-volume tool aracne exposes, and the shipped
// agent contract tells the model to use it instead of its native grep. Measured against
// `grep -rn` on cli/cli, the previous implementation emitted 2.26x the bytes for
// `'func '`, 2.58x for `'err'` (3.1 MB from a single call) and 1.84x for `'Options'` —
// while offering none of a real grep's ways to ask for less: no glob, no file-type
// filter, no case-insensitivity, no output modes, no result cap, and no truncation at
// all. Being both bigger AND less capable than the tool it replaces is why the aracne arm
// spent more context than the baseline on discovery.
//
// Two rules follow, and the code below enforces them:
//
//   - Never return unbounded output. Every result is capped and says so.
//   - Pay for topology annotation ONCE per resource, not once per matching line.
package topogrep

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"aracne/internal/topology/domain"
)

// OutputMode selects how much of a hit is rendered.
type OutputMode string

const (
	// OutputContent renders matching lines (the default).
	OutputContent OutputMode = "content"
	// OutputFiles renders only the paths that contain a match — the cheapest way to
	// scope a follow-up search.
	OutputFiles OutputMode = "files_with_matches"
	// OutputCount renders per-file match counts.
	OutputCount OutputMode = "count"
)

// DefaultHeadLimit bounds `content` output when the caller does not choose. A search that
// returns thousands of lines has not answered a question, it has moved the cost from the
// search to the reader.
const DefaultHeadLimit = 200

// AnnotateLimit is the number of DISTINCT resources above which content output drops the
// topology annotation and renders as plain grep.
//
// Annotation earns its bytes only at triage scale. With five hits, knowing what each
// enclosing function does is exactly the information that saves a `read`. With two
// thousand hits spread over two thousand functions it is one header per match — the same
// per-line overhead that made this tool 2.1x the size of `grep -rn`, just moved onto its
// own line. Above the limit the caller is told how to get annotation back.
const AnnotateLimit = 30

// AnnotateOverheadBudget caps annotation at a fraction of the matched content.
//
// A resource count alone is the wrong test: 25 resources across 500 matching lines is 25
// headers of useful context, while 25 resources across 30 matching lines is nearly one
// header per line — the very shape that made this tool cost more than the grep it
// replaces. Measuring the headers against the content they annotate catches both.
const AnnotateOverheadBudget = 0.25

// AnnotateFreeBytes is the content size below which annotation is always kept.
//
// The ratio test alone punishes exactly the case annotation is FOR: one hit with one
// header is a poor ratio and a trivial absolute cost. Below this floor the overhead cannot
// matter; above it, annotation has to justify itself proportionally.
const AnnotateFreeBytes = 4096

// maxLineBytes is the per-line ceiling. The previous 64 KB default silently dropped every
// match already found in a file the moment one long line appeared (minified JS, generated
// tables, vendored bundles) — see searchFile.
const maxLineBytes = 8 * 1024 * 1024

// Match is a single hit.
type Match struct {
	Path        string   `json:"path"`
	Line        int      `json:"line"`
	Text        string   `json:"text"`
	ResourceID  string   `json:"resource_id,omitempty"`
	Description string   `json:"description,omitempty"`
	Before      []string `json:"before,omitempty"`
	After       []string `json:"after,omitempty"`
}

// Options configures one search.
type Options struct {
	Pattern    string
	Root       string
	Glob       string // filename glob, e.g. "*.go" or "**/*_test.go"
	Type       string // language shorthand, e.g. "go", "py", "ts"
	IgnoreCase bool
	Mode       OutputMode
	HeadLimit  int // 0 uses DefaultHeadLimit; negative means unlimited
	Before     int // context lines before each hit
	After      int // context lines after each hit
	// Ignore applies the project's scan.ignore rules. Build it with
	// domain.BuildIgnoreMatcher(root, cfg.Scan.Ignore); nil disables the check.
	Ignore *domain.IgnoreMatcher
}

// Result carries the hits plus enough accounting to tell the caller what it did not see.
type Result struct {
	Matches []Match
	// Files, in path order, that contained at least one match.
	Files []string
	// Counts maps a path to its match count (all matches, not just the rendered ones).
	Counts map[string]int
	// Total is how many matches existed before the head limit was applied.
	Total int
	// Truncated reports that Total exceeded the limit.
	Truncated bool
	// Limit is the limit actually applied (for rendering the trailer).
	Limit int
	// DistinctResources counts the distinct resources among the rendered matches; it
	// decides whether annotation is worth its bytes (see AnnotateLimit).
	DistinctResources int
}

type resourceLocation struct {
	id          string
	description string
	kind        domain.ResourceKind
	startsAt    int
	endsAt      int
}

// Search is the back-compatible entry point: content mode, default cap, no filters.
func Search(pattern, root string, topo *domain.Topology) ([]Match, error) {
	res, err := SearchWith(Options{Pattern: pattern, Root: root}, topo)
	if err != nil {
		return nil, err
	}
	return res.Matches, nil
}

// SearchWith runs a configured search.
func SearchWith(opt Options, topo *domain.Topology) (*Result, error) {
	if opt.Pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	if opt.Root == "" {
		opt.Root = "."
	}
	if opt.Mode == "" {
		opt.Mode = OutputContent
	}
	pattern := opt.Pattern
	if opt.IgnoreCase && !strings.HasPrefix(pattern, "(?i)") {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern: %w", err)
	}
	exts, err := typeExtensions(opt.Type)
	if err != nil {
		return nil, err
	}

	index := buildResourceIndex(topo)
	out := &Result{Counts: map[string]int{}, Limit: effectiveLimit(opt)}

	if err := walkSearch(opt, exts, func(path string) error {
		fileMatches, err := searchFile(path, re, index, opt)
		if err != nil {
			return err
		}
		if len(fileMatches) == 0 {
			return nil
		}
		display := fileMatches[0].Path
		out.Files = append(out.Files, display)
		out.Counts[display] = len(fileMatches)
		out.Total += len(fileMatches)
		out.Matches = append(out.Matches, fileMatches...)
		return nil
	}); err != nil {
		return nil, err
	}

	sort.Slice(out.Matches, func(i, j int) bool {
		if out.Matches[i].Path != out.Matches[j].Path {
			return out.Matches[i].Path < out.Matches[j].Path
		}
		return out.Matches[i].Line < out.Matches[j].Line
	})
	sort.Strings(out.Files)

	// Cap only what is rendered; Total and Counts keep the honest numbers so the trailer
	// can say how much was withheld.
	if out.Limit > 0 && len(out.Matches) > out.Limit {
		out.Matches = out.Matches[:out.Limit]
		out.Truncated = true
	}
	distinct := map[string]bool{}
	for _, m := range out.Matches {
		if m.ResourceID != "" {
			distinct[m.ResourceID] = true
		}
	}
	out.DistinctResources = len(distinct)
	return out, nil
}

func effectiveLimit(opt Options) int {
	switch {
	case opt.Mode != OutputContent:
		return 0 // file lists and counts are already one line per file
	case opt.HeadLimit < 0:
		return 0
	case opt.HeadLimit == 0:
		return DefaultHeadLimit
	default:
		return opt.HeadLimit
	}
}

// Format renders content-mode matches with the default options.
func Format(matches []Match) string {
	distinct := map[string]bool{}
	for _, m := range matches {
		if m.ResourceID != "" {
			distinct[m.ResourceID] = true
		}
	}
	return FormatResult(&Result{
		Matches: matches, Total: len(matches), DistinctResources: len(distinct),
	}, Options{Mode: OutputContent})
}

// FormatResult renders a result for the requested mode.
//
// In content mode the resource annotation is emitted ONCE per resource, as a header above
// its matches, rather than as two extra lines under every matching line. A resource with
// forty hits used to repeat its description forty times.
func FormatResult(res *Result, opt Options) string {
	if res == nil {
		return ""
	}
	switch opt.Mode {
	case OutputFiles:
		if len(res.Files) == 0 {
			return noMatches(opt)
		}
		return strings.Join(res.Files, "\n")
	case OutputCount:
		if len(res.Counts) == 0 {
			return noMatches(opt)
		}
		paths := make([]string, 0, len(res.Counts))
		for p := range res.Counts {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		var b strings.Builder
		for i, p := range paths {
			if i > 0 {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "%s:%d", p, res.Counts[p])
		}
		return b.String()
	}

	if len(res.Matches) == 0 {
		return noMatches(opt)
	}
	// Annotate only when the result set is small enough to triage by reading the
	// descriptions AND the headers stay a small fraction of the content they describe;
	// otherwise degrade to plain grep, which is what a broad sweep wanted anyway.
	annotate := res.DistinctResources > 0 && res.DistinctResources <= AnnotateLimit &&
		annotationFits(res.Matches)

	var b strings.Builder
	lastResource := ""
	for i, m := range res.Matches {
		if i > 0 {
			b.WriteByte('\n')
		}
		// One header per resource run, not per line.
		if annotate && m.ResourceID != "" && m.ResourceID != lastResource {
			b.WriteString("# ")
			b.WriteString(m.ResourceID)
			if m.Description != "" {
				b.WriteString(" — ")
				b.WriteString(m.Description)
			}
			b.WriteByte('\n')
		}
		lastResource = m.ResourceID
		for _, c := range m.Before {
			fmt.Fprintf(&b, "%s-%d-%s\n", m.Path, m.Line-len(m.Before), c)
		}
		fmt.Fprintf(&b, "%s:%d:%s", m.Path, m.Line, m.Text)
		for j, c := range m.After {
			fmt.Fprintf(&b, "\n%s-%d-%s", m.Path, m.Line+j+1, c)
		}
	}
	if !annotate && res.DistinctResources > AnnotateLimit {
		fmt.Fprintf(&b, "\n… %d distinct resources matched, so topology annotation is "+
			"omitted. Narrow with glob/type/path (or use output_mode=files_with_matches) "+
			"to get resource IDs and descriptions back.", res.DistinctResources)
	}
	if res.Truncated {
		fmt.Fprintf(&b, "\n… %d more match(es) not shown (showing %d of %d). "+
			"Narrow with glob/type/path, or raise head_limit.",
			res.Total-len(res.Matches), len(res.Matches), res.Total)
	}
	return b.String()
}

// annotationFits reports whether the per-resource headers would stay within
// AnnotateOverheadBudget of the matched content.
func annotationFits(matches []Match) bool {
	content, header := 0, 0
	seen := map[string]bool{}
	for _, m := range matches {
		content += len(m.Path) + len(m.Text) + 8 // + "path:line:"
		if m.ResourceID == "" || seen[m.ResourceID] {
			continue
		}
		seen[m.ResourceID] = true
		header += len(m.ResourceID) + len(m.Description) + 6 // + "# " and the separator
	}
	if content == 0 {
		return false
	}
	if content+header <= AnnotateFreeBytes {
		return true
	}
	return float64(header)/float64(content) <= AnnotateOverheadBudget
}

// noMatches is an explicit answer. Returning "" made a successful search with zero hits
// indistinguishable from a broken tool.
func noMatches(opt Options) string {
	if opt.Pattern == "" {
		return "no matches"
	}
	return fmt.Sprintf("no matches for %q", opt.Pattern)
}

// walkSearch visits every candidate file under opt.Root.
func walkSearch(opt Options, exts map[string]bool, visit func(path string) error) error {
	info, err := os.Stat(opt.Root)
	if err != nil {
		return fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return visit(opt.Root)
	}
	return filepath.WalkDir(opt.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// The search root is never pruned by its own name: searching inside
			// ~/.dotfiles must work.
			if path != opt.Root && shouldSkipDir(path, d.Name(), opt) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeType != 0 {
			return nil
		}
		if !wantFile(path, d.Name(), exts, opt) {
			return nil
		}
		return visit(path)
	})
}

// shouldSkipDir prunes a directory. Beyond the always-noise set it consults the project's
// own scan.ignore rules, which were previously not wired in here at all — so a search
// happily descended into build output the scanner itself had been told to skip.
func shouldSkipDir(path, name string, opt Options) bool {
	if name == "." {
		return false
	}
	switch name {
	case ".git", ".aracne", "node_modules", "vendor":
		return true
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	return opt.Ignore.MatchDir(path)
}

// wantFile applies the glob and type filters and the ignore rules.
func wantFile(path, name string, exts map[string]bool, opt Options) bool {
	if opt.Ignore.Match(path) {
		return false
	}
	if len(exts) > 0 && !exts[strings.ToLower(filepath.Ext(name))] {
		return false
	}
	if opt.Glob == "" {
		return true
	}
	return matchGlob(opt.Glob, path, name)
}

// matchGlob supports a bare filename pattern ("*.go"), a "**/"-prefixed pattern
// ("**/*_test.go", equivalent to the bare form), and a path pattern ("src/*.ts").
func matchGlob(glob, path, name string) bool {
	g := strings.TrimPrefix(glob, "**/")
	if !strings.ContainsAny(g, "/\\") {
		ok, err := filepath.Match(g, name)
		return err == nil && ok
	}
	slashed := filepath.ToSlash(path)
	if ok, err := filepath.Match(g, slashed); err == nil && ok {
		return true
	}
	// Also try matching the trailing segments so "src/*.ts" hits "./a/src/x.ts".
	segs := strings.Split(slashed, "/")
	want := len(strings.Split(g, "/"))
	if want < len(segs) {
		tail := strings.Join(segs[len(segs)-want:], "/")
		ok, err := filepath.Match(g, tail)
		return err == nil && ok
	}
	return false
}

// typeExtensions maps a language shorthand to the extensions it covers.
func typeExtensions(t string) (map[string]bool, error) {
	if t == "" {
		return nil, nil
	}
	table := map[string][]string{
		"go":         {".go"},
		"py":         {".py", ".pyi"},
		"python":     {".py", ".pyi"},
		"js":         {".js", ".jsx", ".mjs", ".cjs"},
		"javascript": {".js", ".jsx", ".mjs", ".cjs"},
		"ts":         {".ts", ".tsx", ".mts", ".cts"},
		"typescript": {".ts", ".tsx", ".mts", ".cts"},
		"rust":       {".rs"},
		"rs":         {".rs"},
		"java":       {".java"},
		"c":          {".c", ".h"},
		"cpp":        {".cc", ".cpp", ".cxx", ".hpp", ".hh"},
		"md":         {".md", ".markdown"},
		"json":       {".json"},
		"yaml":       {".yaml", ".yml"},
		"toml":       {".toml"},
		"sh":         {".sh", ".bash"},
	}
	exts, ok := table[strings.ToLower(t)]
	if !ok {
		known := make([]string, 0, len(table))
		for k := range table {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("unknown type %q; known types: %s", t, strings.Join(known, ", "))
	}
	set := make(map[string]bool, len(exts))
	for _, e := range exts {
		set[e] = true
	}
	return set, nil
}

// searchFile scans one file.
//
// On a scanner error it returns the matches found SO FAR rather than discarding them. The
// previous `return nil, nil` meant one over-long line (a minified bundle, a generated
// table) silently erased every hit in that file — a search that looked successful and
// simply lied.
func searchFile(path string, re *regexp.Regexp, index map[string][]resourceLocation, opt Options) ([]Match, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	canonical := canonicalPath(path)
	resources := index[canonical]
	display := displayPath(path)

	var matches []Match
	var ring []string // rolling window of the previous opt.Before lines
	pendingAfter := 0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimRight(scanner.Text(), "\r")

		if pendingAfter > 0 && len(matches) > 0 {
			last := &matches[len(matches)-1]
			last.After = append(last.After, line)
			pendingAfter--
		}

		if re.MatchString(line) {
			m := Match{Path: display, Line: lineNo, Text: strings.TrimLeft(line, " \t")}
			if resource := bestResource(resources, lineNo); resource != nil {
				m.ResourceID = resource.id
				m.Description = resource.description
			}
			if opt.Before > 0 && len(ring) > 0 {
				m.Before = append([]string(nil), ring...)
			}
			matches = append(matches, m)
			pendingAfter = opt.After
		}

		if opt.Before > 0 {
			ring = append(ring, line)
			if len(ring) > opt.Before {
				ring = ring[1:]
			}
		}
	}
	// Deliberately swallow the scanner error: whatever was found before the failure is
	// real, and returning it beats erasing a whole file's hits because one line was long.
	_ = scanner.Err()
	return matches, nil
}

// buildResourceIndex maps canonical file path -> resources, narrowest span first.
func buildResourceIndex(topo *domain.Topology) map[string][]resourceLocation {
	index := make(map[string][]resourceLocation)
	if topo == nil {
		return index
	}
	for id, res := range topo.Resources {
		if res.Location.Path == "" {
			continue
		}
		path := canonicalPath(res.Location.Path)
		index[path] = append(index[path], resourceLocation{
			id:          id,
			description: res.Description,
			kind:        res.Kind,
			startsAt:    res.Location.StartsAt,
			endsAt:      res.Location.EndsAt,
		})
	}
	for path := range index {
		sort.Slice(index[path], func(i, j int) bool {
			left, right := span(index[path][i]), span(index[path][j])
			if left != right {
				return left < right
			}
			return index[path][i].id < index[path][j].id
		})
	}
	return index
}

// bestResource picks the narrowest resource containing the line, falling back to the
// file-level resource.
func bestResource(resources []resourceLocation, line int) *resourceLocation {
	var fallback *resourceLocation
	for i := range resources {
		resource := &resources[i]
		if resource.kind == domain.ResourceFile {
			if fallback == nil {
				fallback = resource
			}
			continue
		}
		if resource.startsAt <= line && (resource.endsAt == 0 || line <= resource.endsAt) {
			return resource
		}
	}
	return fallback
}

func span(resource resourceLocation) int {
	if resource.startsAt == 0 || resource.endsAt == 0 {
		return 1 << 30
	}
	return resource.endsAt - resource.startsAt
}

func canonicalPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func displayPath(path string) string {
	if rel, err := filepath.Rel(".", path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
