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
//
// The search itself spans three tiers, in priority order: a node whose TITLE matches,
// a node whose stored DESCRIPTION matches, then a matching LINE. The description tier
// is the one a plain grep cannot reach at all -- that prose lives only in the topology
// DB -- so `retry` finds a function documented as "retries on 5xx" whose code never
// says "retry". A node is reported once, never twice: a title match outranks a
// description match, and a node whose body already produced a line hit is promoted to
// its tier rather than given a second row of its own.
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

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
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
//
// LOWERED FROM 4096 AFTER MEASUREMENT. Four kilobytes is not "cannot matter": on the clap
// fixture, `grep -rn 'pub struct' src/` returns 730 bytes from a plain grep and came back at
// 2,211 -- 3.03x -- of which 1,481 bytes were headers. The matched ROWS were 1.00x native, so
// the entire blow-up was annotation waved through by this floor. A narrow search is precisely
// where a header per resource approaches a header per line, which is the shape
// AnnotateOverheadBudget exists to catch; the floor was letting it past unmeasured.
//
// 512 keeps the case the floor was written for -- a handful of hits with a handful of headers
// is still free -- while making anything larger earn its annotation proportionally.
const AnnotateFreeBytes = 512

// MatchSource says why a row is in the result, and its values are ordered by
// priority: a node named after the query outranks one merely described by it,
// which outranks a raw line hit. Ranking is therefore one extra sort key.
type MatchSource int

const (
	// MatchTitle: the node's ID or Name matched the pattern.
	MatchTitle MatchSource = iota
	// MatchDescription: the node's stored description matched. Descriptions live
	// only in the topology DB, so this is the one tier a plain grep cannot reach.
	MatchDescription
	// MatchContent: a source line matched. The default.
	MatchContent
)

// NodeHitBudget caps node rows -- rows that exist only because a title or a
// description matched -- at a fraction of the head limit, per tier.
//
// Prose is long and common words match a lot of it. Without a budget, one
// ordinary query ("value", "the") would fill the entire head limit with node
// rows and push out every line hit the caller actually grepped for, which is the
// opposite of ranking. Promoted line matches are NOT capped by this: those are
// real hits that would have been returned anyway.
const NodeHitBudget = 0.25

// MinNodeHits floors the budget so a small head_limit still shows some node
// rows; MaxNodeHits applies when the head limit is unlimited.
const (
	MinNodeHits = 5
	MaxNodeHits = 50
)

// maxLineBytes is the per-line ceiling. The previous 64 KB default silently dropped every
// match already found in a file the moment one long line appeared (minified JS, generated
// tables, vendored bundles) — see searchFile.
const maxLineBytes = 8 * 1024 * 1024

// Match is a single hit.
type Match struct {
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Text       string `json:"text"`
	ResourceID string `json:"resource_id,omitempty"`
	// ResourceStart and ResourceEnd are the enclosing resource's span. Carried so the
	// renderer can name a hit by the lines the caller can read rather than by an id.
	ResourceStart int      `json:"resource_start,omitempty"`
	ResourceEnd   int      `json:"resource_end,omitempty"`
	Description   string   `json:"description,omitempty"`
	Before        []string `json:"before,omitempty"`
	After         []string `json:"after,omitempty"`
	// MatchedOn is why this row ranked where it did. It is a property of the
	// enclosing resource, so every row of one resource shares it -- which is what
	// keeps a resource's matches contiguous after the tiered sort.
	MatchedOn MatchSource `json:"matched_on"`
	// NodeHit marks a row that is the node's declaration line rather than a
	// matching line: the node surfaced because its title or description matched
	// and its body contained no hit at all.
	NodeHit bool `json:"node_hit,omitempty"`
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
	// LineRange names each annotated hit by "path:start-end" instead of by resource id.
	// See helper.IdentifyLineRange: the id is aracne's vocabulary, the span is the shell's,
	// and a search result is precisely where the caller decides what to read next.
	LineRange bool
	// DescriptionKinds limits which resource kinds may match on their stored
	// description. nil means the caller did not choose and gets
	// DefaultDescriptionKinds(); an explicit empty slice disables description
	// matching entirely. Titles are never gated -- a name match is precise.
	DescriptionKinds []domain.ResourceKind
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
	// DistinctResources counts the distinct resources among the rendered LINE
	// matches; it decides whether annotation is worth its bytes (see
	// AnnotateLimit). Node rows are excluded: they are always annotated, because
	// for them the header is the answer.
	DistinctResources int
	// TitleWithheld and DescriptionWithheld count the node rows dropped by the
	// per-tier budget, so the trailer can say what was not shown.
	TitleWithheld       int
	DescriptionWithheld int
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
	tiers := matchNodes(re, topo, descriptionKindSet(opt.DescriptionKinds))
	out := &Result{Counts: map[string]int{}, Limit: effectiveLimit(opt)}

	var all []Match
	if err := walkSearch(opt, exts, func(path string) error {
		fileMatches, err := searchFile(path, re, index, tiers, opt)
		if err != nil {
			return err
		}
		all = append(all, fileMatches...)
		return nil
	}); err != nil {
		return nil, err
	}

	// Priority first, then the familiar path/line order within a tier. Tier is a
	// property of the resource, so one resource's rows never straddle two tiers and
	// FormatResult's one-header-per-run grouping still holds.
	sort.Slice(all, func(i, j int) bool {
		if all[i].MatchedOn != all[j].MatchedOn {
			return all[i].MatchedOn < all[j].MatchedOn
		}
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Line < all[j].Line
	})

	all, out.TitleWithheld, out.DescriptionWithheld = capNodeHits(all, out.Limit)

	// Accounting sits AFTER the node-hit budget, so Files and Counts describe rows the
	// caller can actually get, and BEFORE the head limit, so they stay the honest
	// pre-truncation numbers the trailer reports against.
	out.Matches = all
	out.Total = len(all)
	for _, m := range all {
		out.Counts[m.Path]++
	}
	for path := range out.Counts {
		out.Files = append(out.Files, path)
	}
	sort.Strings(out.Files)

	// Cap only what is rendered; Total and Counts keep the honest numbers so the trailer
	// can say how much was withheld.
	if out.Limit > 0 && len(out.Matches) > out.Limit {
		out.Matches = out.Matches[:out.Limit]
		out.Truncated = true
	}
	distinct := map[string]bool{}
	for _, m := range out.Matches {
		if m.ResourceID != "" && !m.NodeHit {
			distinct[m.ResourceID] = true
		}
	}
	out.DistinctResources = len(distinct)
	return out, nil
}

// DefaultDescriptionKinds is the set of resource kinds whose description may match
// when the caller does not choose. It mirrors helper.DefaultGrepDescriptionKinds;
// topogrep deliberately does not import the config package, so the default is
// restated here and the two are kept in step by TestDefaultDescriptionKindsMatchConfig.
func DefaultDescriptionKinds() []domain.ResourceKind {
	return []domain.ResourceKind{
		domain.ResourceFunction,
		domain.ResourceMethod,
		domain.ResourceStruct,
		domain.ResourceInterface,
	}
}

// descriptionKindSet resolves Options.DescriptionKinds. nil means "the caller did not
// choose" and gets the defaults; an explicit empty slice means "never match on a
// description" and must not be back-filled into the defaults.
func descriptionKindSet(kinds []domain.ResourceKind) map[domain.ResourceKind]bool {
	if kinds == nil {
		kinds = DefaultDescriptionKinds()
	}
	if len(kinds) == 0 {
		return nil
	}
	set := make(map[domain.ResourceKind]bool, len(kinds))
	for _, kind := range kinds {
		set[kind] = true
	}
	return set
}

// matchNodes decides once, for the whole topology, which nodes the pattern matches on
// their title or on their stored description.
//
// This is the half of the search a plain grep cannot do: a description exists only in
// the topology DB, so `retry` can find a function documented as "retries on 5xx" whose
// code never says "retry". Title wins over description, so a node is never reported
// twice for the same query.
func matchNodes(re *regexp.Regexp, topo *domain.Topology, kinds map[domain.ResourceKind]bool) map[string]MatchSource {
	if topo == nil || len(topo.Resources) == 0 {
		return nil
	}
	tiers := make(map[string]MatchSource)
	for id, res := range topo.Resources {
		switch {
		case re.MatchString(res.Name) || re.MatchString(id):
			tiers[id] = MatchTitle
		case res.Description != "" && kinds[res.Kind] && re.MatchString(res.Description):
			tiers[id] = MatchDescription
		}
	}
	if len(tiers) == 0 {
		return nil
	}
	return tiers
}

// capNodeHits bounds the rows that exist only because a node's title or description
// matched, per tier, and reports how many it dropped. See NodeHitBudget for why.
func capNodeHits(matches []Match, limit int) ([]Match, int, int) {
	budget := nodeHitBudget(limit)
	kept := matches[:0]
	shown := map[MatchSource]int{}
	titleWithheld, descriptionWithheld := 0, 0
	for _, m := range matches {
		if m.NodeHit {
			shown[m.MatchedOn]++
			if shown[m.MatchedOn] > budget {
				if m.MatchedOn == MatchTitle {
					titleWithheld++
				} else {
					descriptionWithheld++
				}
				continue
			}
		}
		kept = append(kept, m)
	}
	return kept, titleWithheld, descriptionWithheld
}

func nodeHitBudget(limit int) int {
	if limit <= 0 {
		return MaxNodeHits
	}
	if budget := int(float64(limit) * NodeHitBudget); budget > MinNodeHits {
		return budget
	}
	return MinNodeHits
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
		if m.ResourceID != "" && !m.NodeHit {
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
		// One header per resource run, not per line. A node row is annotated whatever
		// the gate says: it is in the result BECAUSE of its title or description, so
		// without the header it renders as an unexplained declaration line.
		if (annotate || m.NodeHit) && m.ResourceID != "" && m.ResourceID != lastResource {
			b.WriteString("# ")
			b.WriteString(resourceLabel(m, opt))
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
	if withheld := res.TitleWithheld + res.DescriptionWithheld; withheld > 0 {
		fmt.Fprintf(&b, "\n… %d more node(s) matched on name or description but were not "+
			"shown. Narrow the pattern, or raise head_limit.", withheld)
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
		// Node rows are annotated unconditionally, so they neither earn nor owe
		// anything in the budget that decides annotation for the line matches.
		if m.NodeHit {
			continue
		}
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
func searchFile(path string, re *regexp.Regexp, index map[string][]resourceLocation, tiers map[string]MatchSource, opt Options) ([]Match, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	canonical := canonicalPath(path)
	resources := index[canonical]
	display := displayPath(path)

	// Declaration lines to capture for nodes this file owns whose title or description
	// matched. Reading them here is free -- the scan is already walking every line --
	// and it means a node row can never escape the glob/type/path/ignore filters the
	// caller asked for, because it is only produced for a file the walk visited.
	wanted := declarationLines(resources, tiers)
	declared := map[int]string{} // index into resources -> declaration line text
	lineHit := map[string]bool{} // resource id -> the body contained a match

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

		for _, i := range wanted[lineNo] {
			declared[i] = strings.TrimLeft(line, " \t")
		}

		if re.MatchString(line) {
			m := Match{Path: display, Line: lineNo, Text: strings.TrimLeft(line, " \t"), MatchedOn: MatchContent}
			if resource := bestResource(resources, lineNo); resource != nil {
				m.ResourceID = resource.id
				m.ResourceStart, m.ResourceEnd = resource.startsAt, resource.endsAt
				m.Description = resource.description
				// A line inside a node the pattern also named or described is that
				// node's hit: promote it rather than emitting a second row for the
				// same node further down.
				if tier, ok := tiers[resource.id]; ok {
					m.MatchedOn = tier
				}
				lineHit[resource.id] = true
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

	// Only now, for nodes the pattern named or described whose body produced nothing:
	// these are exactly the nodes a content-only grep misses.
	for i, text := range declared {
		resource := &resources[i]
		if lineHit[resource.id] {
			continue
		}
		matches = append(matches, Match{
			Path:          display,
			Line:          declarationLine(resource),
			Text:          text,
			ResourceID:    resource.id,
			ResourceStart: resource.startsAt,
			ResourceEnd:   resource.endsAt,
			Description:   resource.description,
			MatchedOn:     tiers[resource.id],
			NodeHit:       true,
		})
	}
	return matches, nil
}

// declarationLines maps a line number to the resources whose declaration starts there,
// for the matched nodes of one file. Nothing is allocated when the file owns no matched
// node, which is the common case.
func declarationLines(resources []resourceLocation, tiers map[string]MatchSource) map[int][]int {
	if len(tiers) == 0 {
		return nil
	}
	var wanted map[int][]int
	for i := range resources {
		if _, ok := tiers[resources[i].id]; !ok {
			continue
		}
		if wanted == nil {
			wanted = map[int][]int{}
		}
		line := declarationLine(&resources[i])
		wanted[line] = append(wanted[line], i)
	}
	return wanted
}

// declarationLine is where a node row points. A resource with no line span (a
// file-level resource, say) points at the top of its file.
func declarationLine(resource *resourceLocation) int {
	if resource.startsAt <= 0 {
		return 1
	}
	return resource.startsAt
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

// resourceLabel names a hit's enclosing resource for the header line.
//
// Under LineRange it is "path:start-end", which is directly runnable: the caller's next move is
// a read of exactly those lines, and aracne answers that read with the whole declaration. The
// id form is kept for identification_mode "id" and whenever a span is unavailable.
func resourceLabel(m Match, opt Options) string {
	if opt.LineRange && m.ResourceStart > 0 && m.ResourceEnd >= m.ResourceStart {
		return fmt.Sprintf("%s:%d-%d", m.Path, m.ResourceStart, m.ResourceEnd)
	}
	return m.ResourceID
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
