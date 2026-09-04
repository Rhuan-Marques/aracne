package topogrep

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func TestSearchAnnotatesResourceMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.go")
	content := "package main\n\nfunc Target() {\n\tfmt.Println(\"needle\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	topo := &domain.Topology{Resources: map[string]domain.Resource{
		"example.Target": {
			ID:          "example.Target",
			Kind:        domain.ResourceFunction,
			Name:        "Target",
			Description: "prints a test needle",
			Location:    domain.Location{Path: filePath, StartsAt: 3, EndsAt: 5},
		},
	}}

	matches, err := Search("needle", dir, topo)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].ResourceID != "example.Target" || matches[0].Description != "prints a test needle" {
		t.Fatalf("unexpected annotation: %+v", matches[0])
	}

	// The annotation is now a single header ABOVE the resource's matches rather than two
	// extra lines under every matching line — a resource with forty hits used to repeat
	// its description forty times.
	formatted := Format(matches)
	for _, want := range []string{"sample.go:4:", "# example.Target", "prints a test needle"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, formatted)
		}
	}
}

func TestSearchReturnsTopologyAndRawMatches(t *testing.T) {
	dir := t.TempDir()
	topoFile := filepath.Join(dir, "topo.go")
	rawFile := filepath.Join(dir, "raw.go")

	if err := os.WriteFile(topoFile, []byte("package main\n\nfunc Target() {\n\tfmt.Println(\"needle\")\n}\n"), 0644); err != nil {
		t.Fatalf("write topology file: %v", err)
	}
	if err := os.WriteFile(rawFile, []byte("package main\n\nfunc Other() {\n\tfmt.Println(\"needle\")\n}\n"), 0644); err != nil {
		t.Fatalf("write raw file: %v", err)
	}

	topo := &domain.Topology{Resources: map[string]domain.Resource{
		"example.Target": {
			ID:       "example.Target",
			Kind:     domain.ResourceFunction,
			Name:     "Target",
			Location: domain.Location{Path: topoFile, StartsAt: 3, EndsAt: 5},
		},
	}}

	matches, err := Search("needle", dir, topo)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches (topology + raw), got %d: %+v", len(matches), matches)
	}

	var topoMatch, rawMatch *Match
	for i, m := range matches {
		if strings.HasSuffix(m.Path, "topo.go") {
			topoMatch = &matches[i]
		} else if strings.HasSuffix(m.Path, "raw.go") {
			rawMatch = &matches[i]
		}
	}
	if topoMatch == nil || topoMatch.ResourceID != "example.Target" {
		t.Fatalf("topology file missing annotation: %+v", topoMatch)
	}
	if rawMatch == nil || rawMatch.ResourceID != "" {
		t.Fatalf("raw file should have no annotation: %+v", rawMatch)
	}
}

func TestSearchReturnsAllMatchesWithoutTopology(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc Target() {\n\tfmt.Println(\"needle\")\n}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	matches, err := Search("needle", dir, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match without topology, got %d: %+v", len(matches), matches)
	}
	if matches[0].ResourceID != "" {
		t.Fatalf("expected no annotation without topology, got ResourceID=%q", matches[0].ResourceID)
	}
}

// --------------------------------------------------------------------------- //
// id-scheme-2-era additions: the capability gap that made this tool cost more
// than the native grep it replaces.
// --------------------------------------------------------------------------- //

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnnotationIsEmittedOncePerResourceNotPerLine(t *testing.T) {
	// This is the whole point of the format change.
	dir := t.TempDir()
	path := filepath.Join(dir, "s.go")
	writeTree(t, dir, map[string]string{"s.go": "package p\n\nfunc T() {\n\tneedle\n\tneedle\n\tneedle\n}\n"})
	topo := &domain.Topology{Resources: map[string]domain.Resource{
		"p.T": {ID: "p.T", Kind: domain.ResourceFunction, Name: "T",
			Description: "has needles", Location: domain.Location{Path: path, StartsAt: 3, EndsAt: 7}},
	}}
	res, err := SearchWith(Options{Pattern: "needle", Root: dir}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 3 {
		t.Fatalf("want 3 matches, got %d", len(res.Matches))
	}
	out := FormatResult(res, Options{Mode: OutputContent})
	if n := strings.Count(out, "has needles"); n != 1 {
		t.Fatalf("description rendered %d times, want exactly 1:\n%s", n, out)
	}
}

func TestHeadLimitCapsOutputAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	var body strings.Builder
	for i := 0; i < 50; i++ {
		body.WriteString("needle\n")
	}
	writeTree(t, dir, map[string]string{"a.txt": body.String()})

	res, err := SearchWith(Options{Pattern: "needle", Root: dir, HeadLimit: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 10 || res.Total != 50 || !res.Truncated {
		t.Fatalf("want 10 of 50 truncated, got %d of %d truncated=%v",
			len(res.Matches), res.Total, res.Truncated)
	}
	out := FormatResult(res, Options{Mode: OutputContent, Pattern: "needle"})
	if !strings.Contains(out, "40 more match(es) not shown") {
		t.Fatalf("truncation trailer missing:\n%s", out)
	}
}

func TestDefaultLimitApplies(t *testing.T) {
	// An uncapped search is how a single call returned 3.1 MB.
	dir := t.TempDir()
	var body strings.Builder
	for i := 0; i < DefaultHeadLimit+25; i++ {
		body.WriteString("needle\n")
	}
	writeTree(t, dir, map[string]string{"a.txt": body.String()})
	res, err := SearchWith(Options{Pattern: "needle", Root: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != DefaultHeadLimit || !res.Truncated {
		t.Fatalf("want the default cap applied, got %d truncated=%v", len(res.Matches), res.Truncated)
	}
}

func TestGlobAndTypeFilters(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.go":          "needle\n",
		"b.ts":          "needle\n",
		"sub/c.go":      "needle\n",
		"sub/d_test.go": "needle\n",
	})
	check := func(opt Options, want int, label string) {
		t.Helper()
		res, err := SearchWith(opt, nil)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if len(res.Matches) != want {
			t.Fatalf("%s: want %d matches, got %d (%v)", label, want, len(res.Matches), res.Files)
		}
	}
	check(Options{Pattern: "needle", Root: dir}, 4, "no filter")
	check(Options{Pattern: "needle", Root: dir, Type: "go"}, 3, "type=go")
	check(Options{Pattern: "needle", Root: dir, Type: "ts"}, 1, "type=ts")
	check(Options{Pattern: "needle", Root: dir, Glob: "*.go"}, 3, "glob=*.go")
	check(Options{Pattern: "needle", Root: dir, Glob: "**/*_test.go"}, 1, "glob=**/*_test.go")
}

func TestUnknownTypeIsAnError(t *testing.T) {
	if _, err := SearchWith(Options{Pattern: "x", Root: t.TempDir(), Type: "cobol"}, nil); err == nil {
		t.Fatal("want an error for an unknown type")
	}
}

func TestIgnoreCase(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.txt": "Needle\nNEEDLE\nneedle\n"})
	res, err := SearchWith(Options{Pattern: "needle", Root: dir, IgnoreCase: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 3 {
		t.Fatalf("want 3 case-insensitive matches, got %d", len(res.Matches))
	}
}

func TestOutputModes(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.txt": "needle\nneedle\n", "b.txt": "needle\n"})
	res, err := SearchWith(Options{Pattern: "needle", Root: dir, Mode: OutputFiles}, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := FormatResult(res, Options{Mode: OutputFiles})
	if strings.Count(files, "\n") != 1 || !strings.Contains(files, "a.txt") {
		t.Fatalf("files_with_matches should list 2 paths, got:\n%s", files)
	}
	counts := FormatResult(res, Options{Mode: OutputCount})
	if !strings.Contains(counts, ":2") || !strings.Contains(counts, ":1") {
		t.Fatalf("count mode wrong:\n%s", counts)
	}
}

func TestContextLines(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.txt": "one\ntwo\nneedle\nfour\nfive\n"})
	res, err := SearchWith(Options{Pattern: "needle", Root: dir, Before: 2, After: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := res.Matches[0]
	if len(m.Before) != 2 || m.Before[0] != "one" || m.Before[1] != "two" {
		t.Fatalf("before context wrong: %v", m.Before)
	}
	if len(m.After) != 2 || m.After[0] != "four" || m.After[1] != "five" {
		t.Fatalf("after context wrong: %v", m.After)
	}
}

func TestLongLineDoesNotDiscardEarlierMatches(t *testing.T) {
	// The old 64 KB scanner returned nil,nil on error, erasing every hit already found in
	// the file — a search that looked successful and silently lied. Minified JS and
	// generated tables hit this routinely.
	dir := t.TempDir()
	long := strings.Repeat("x", 200*1024)
	writeTree(t, dir, map[string]string{"a.js": "needle\n" + long + "\nneedle\n"})
	res, err := SearchWith(Options{Pattern: "needle", Root: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 2 {
		t.Fatalf("want both matches around the long line, got %d", len(res.Matches))
	}
}

func TestZeroMatchesSaysSoInsteadOfReturningEmpty(t *testing.T) {
	// An empty tool result reads to a model as a broken tool, not as "no hits".
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.txt": "nothing here\n"})
	res, err := SearchWith(Options{Pattern: "needle", Root: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := FormatResult(res, Options{Mode: OutputContent, Pattern: "needle"})
	if !strings.Contains(out, "no matches for") {
		t.Fatalf("want an explicit no-match message, got %q", out)
	}
}

func TestLeadingIndentationIsTrimmed(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.go": "func f() {\n\t\t\tneedle\n}\n"})
	res, err := SearchWith(Options{Pattern: "needle", Root: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Matches[0].Text != "needle" {
		t.Fatalf("want trimmed text, got %q", res.Matches[0].Text)
	}
}

func TestAnnotationIsDroppedWhenTooManyDistinctResources(t *testing.T) {
	// Annotation earns its bytes only at triage scale. A sweep spanning hundreds of
	// functions gets one header per match, which is the per-line overhead that made this
	// tool 2.1x `grep -rn`. Above the limit it degrades to plain grep and says so.
	dir := t.TempDir()
	var body strings.Builder
	resources := map[string]domain.Resource{}
	path := filepath.Join(dir, "big.go")
	for i := 0; i < AnnotateLimit+5; i++ {
		body.WriteString("needle\n")
		id := fmt.Sprintf("pkg.Fn%d", i)
		resources[id] = domain.Resource{
			ID: id, Kind: domain.ResourceFunction, Name: fmt.Sprintf("Fn%d", i),
			Description: "a description long enough to matter for byte accounting",
			Location:    domain.Location{Path: path, StartsAt: i + 1, EndsAt: i + 1},
		}
	}
	writeTree(t, dir, map[string]string{"big.go": body.String()})

	res, err := SearchWith(Options{Pattern: "needle", Root: dir, HeadLimit: -1},
		&domain.Topology{Resources: resources})
	if err != nil {
		t.Fatal(err)
	}
	if res.DistinctResources <= AnnotateLimit {
		t.Fatalf("test setup: want > %d distinct resources, got %d",
			AnnotateLimit, res.DistinctResources)
	}
	out := FormatResult(res, Options{Mode: OutputContent, Pattern: "needle"})
	if strings.Contains(out, "a description long enough") {
		t.Fatalf("annotation should be suppressed above the limit:\n%s", out)
	}
	if !strings.Contains(out, "annotation is omitted") {
		t.Fatalf("suppression must be explained:\n%s", out)
	}

	// Below the limit it is fully annotated.
	small := &Result{
		Matches: res.Matches[:3], Total: 3,
		DistinctResources: 3,
	}
	if !strings.Contains(FormatResult(small, Options{Mode: OutputContent}), "# pkg.Fn0") {
		t.Fatal("small result sets must keep their annotation")
	}
}

// --------------------------------------------------------------------------- //
// Node search: titles and descriptions are searchable, ranked above line hits.
// --------------------------------------------------------------------------- //

// nodeTopo builds a one-file topology from (id, name, kind, description, start, end).
func nodeTopo(resources ...domain.Resource) *domain.Topology {
	byID := map[string]domain.Resource{}
	for _, r := range resources {
		byID[r.ID] = r
	}
	return &domain.Topology{Resources: byID}
}

func TestDescriptionMatchSurfacesNodeWithNoLineMatch(t *testing.T) {
	// The whole point: a description lives only in the topology DB, so this node is
	// unreachable by any content-only grep. Its body never says "retry".
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"client.go": "package http\n\nfunc (c *Client) do() error {\n\treturn c.send()\n}\n",
	})

	res, err := SearchWith(Options{Pattern: "retries", Root: dir}, nodeTopo(domain.Resource{
		ID: "http.Client.do", Kind: domain.ResourceMethod, Name: "do",
		Description: "Sends the request and retries on 5xx with backoff.",
		Location:    domain.Location{Path: filepath.Join(dir, "client.go"), StartsAt: 3, EndsAt: 5},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("want 1 node row, got %d: %+v", len(res.Matches), res.Matches)
	}
	m := res.Matches[0]
	if !m.NodeHit || m.MatchedOn != MatchDescription {
		t.Fatalf("want a description node row, got %+v", m)
	}
	if m.Line != 3 || !strings.HasPrefix(m.Text, "func (c *Client) do()") {
		t.Fatalf("node row must point at the declaration line, got %+v", m)
	}
	// The header is the answer here, so it must be rendered.
	out := FormatResult(res, Options{Mode: OutputContent, Pattern: "retries"})
	for _, want := range []string{"# http.Client.do", "retries on 5xx", "client.go:3:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestNodeWithLineMatchGetsNoSecondRow(t *testing.T) {
	// The line hit already surfaces the node, with its description in the header.
	// A separate node row would be the same node reported twice.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"client.go": "package http\n\nfunc do() error {\n\treturn retryOnce()\n}\n",
	})

	res, err := SearchWith(Options{Pattern: "retr", Root: dir}, nodeTopo(domain.Resource{
		ID: "http.do", Kind: domain.ResourceFunction, Name: "do",
		Description: "Sends the request and retries on 5xx.",
		Location:    domain.Location{Path: filepath.Join(dir, "client.go"), StartsAt: 3, EndsAt: 5},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("want exactly 1 row (no duplicate), got %d: %+v", len(res.Matches), res.Matches)
	}
	m := res.Matches[0]
	if m.NodeHit {
		t.Fatalf("a node with a line hit must not also get a node row: %+v", m)
	}
	// It is still ranked as a description match: the line is that node's hit.
	if m.MatchedOn != MatchDescription || m.Line != 4 {
		t.Fatalf("line hit should be promoted to the node's tier, got %+v", m)
	}
}

func TestTitleMatchSuppressesDescriptionMatchForSameNode(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.go": "package a\n\nvar x = 1\n"})

	res, err := SearchWith(Options{Pattern: "retry", Root: dir}, nodeTopo(domain.Resource{
		ID: "a.retry", Kind: domain.ResourceFunction, Name: "retry",
		Description: "retry helper",
		Location:    domain.Location{Path: filepath.Join(dir, "a.go"), StartsAt: 3, EndsAt: 3},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("want 1 row, got %d: %+v", len(res.Matches), res.Matches)
	}
	if res.Matches[0].MatchedOn != MatchTitle {
		t.Fatalf("title must win over description, got %+v", res.Matches[0])
	}
}

func TestTiersRankTitleThenDescriptionThenLine(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		// Three files so path ordering alone would NOT produce the wanted order:
		// alphabetically raw < named < described.
		"named.go":     "package p\n\nfunc needleName() {\n\tnop()\n}\n",
		"described.go": "package p\n\nfunc other() {\n\tnop()\n}\n",
		"raw.go":       "package p\n\n// needle in a plain file\n",
	})
	res, err := SearchWith(Options{Pattern: "needle", Root: dir}, nodeTopo(
		domain.Resource{
			ID: "p.needleName", Kind: domain.ResourceFunction, Name: "needleName",
			Location: domain.Location{Path: filepath.Join(dir, "named.go"), StartsAt: 3, EndsAt: 5},
		},
		domain.Resource{
			ID: "p.other", Kind: domain.ResourceFunction, Name: "other",
			Description: "finds the needle",
			Location:    domain.Location{Path: filepath.Join(dir, "described.go"), StartsAt: 3, EndsAt: 5},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	var got []MatchSource
	var paths []string
	for _, m := range res.Matches {
		got = append(got, m.MatchedOn)
		paths = append(paths, filepath.Base(m.Path))
	}
	want := []MatchSource{MatchTitle, MatchDescription, MatchContent}
	if len(got) != len(want) {
		t.Fatalf("want %d rows, got %d: %v %v", len(want), len(got), got, paths)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wrong ranking: got %v (%v), want %v", got, paths, want)
		}
	}
	if paths[0] != "named.go" || paths[1] != "described.go" || paths[2] != "raw.go" {
		t.Fatalf("ranking must beat path order, got %v", paths)
	}
}

func TestDescriptionKindsGateWhichNodesMayMatch(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.go": "package a\n\nvar x = 1\n"})
	topo := nodeTopo(domain.Resource{
		ID: "a.go", Kind: domain.ResourceFile, Name: "a.go",
		Description: "holds the needle",
		Location:    domain.Location{Path: filepath.Join(dir, "a.go"), StartsAt: 1, EndsAt: 3},
	})

	// Default kinds exclude `file`, whose descriptions are scraped from doc comments.
	res, err := SearchWith(Options{Pattern: "needle", Root: dir}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 0 {
		t.Fatalf("file kind is not a default description kind: %+v", res.Matches)
	}

	// Opting the kind in surfaces it.
	res, err = SearchWith(Options{
		Pattern: "needle", Root: dir,
		DescriptionKinds: []domain.ResourceKind{domain.ResourceFile},
	}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.Matches[0].MatchedOn != MatchDescription {
		t.Fatalf("configured kind should match on description: %+v", res.Matches)
	}

	// An explicit empty list is "never match on description" and must not be
	// back-filled into the defaults.
	res, err = SearchWith(Options{
		Pattern: "needle", Root: dir,
		DescriptionKinds: []domain.ResourceKind{},
	}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 0 {
		t.Fatalf("empty kind list must disable description matching: %+v", res.Matches)
	}
}

func TestNodeRowsAreSubCappedAndSayHowMany(t *testing.T) {
	// Prose is long and common words match a lot of it. Without a budget one such
	// query fills the head limit with node rows and pushes out every line hit.
	dir := t.TempDir()
	var body strings.Builder
	var resources []domain.Resource
	total := MaxNodeHits + 20
	for i := 0; i < total; i++ {
		body.WriteString(fmt.Sprintf("func Fn%d() {}\n", i))
		resources = append(resources, domain.Resource{
			ID: fmt.Sprintf("pkg.Fn%d", i), Kind: domain.ResourceFunction,
			Name:        fmt.Sprintf("Fn%d", i),
			Description: "handles the widget lifecycle",
			Location:    domain.Location{Path: filepath.Join(dir, "big.go"), StartsAt: i + 1, EndsAt: i + 1},
		})
	}
	writeTree(t, dir, map[string]string{"big.go": body.String()})

	res, err := SearchWith(Options{Pattern: "widget", Root: dir, HeadLimit: -1},
		nodeTopo(resources...))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != MaxNodeHits {
		t.Fatalf("want %d node rows after the cap, got %d", MaxNodeHits, len(res.Matches))
	}
	if res.DescriptionWithheld != total-MaxNodeHits {
		t.Fatalf("want %d withheld, got %d", total-MaxNodeHits, res.DescriptionWithheld)
	}
	// Accounting runs after the cap, so Counts describes what the caller can get.
	if res.Total != MaxNodeHits {
		t.Fatalf("Total should count the capped set, got %d", res.Total)
	}
	out := FormatResult(res, Options{Mode: OutputContent, Pattern: "widget"})
	if !strings.Contains(out, "more node(s) matched on name or description") {
		t.Fatalf("the cap must be explained:\n%s", out)
	}
}

func TestNodeRowsObeyGlobAndPathScoping(t *testing.T) {
	// A node row is produced during the walk, so it can never escape the filters the
	// caller asked for.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"keep/a.go": "package a\n\nfunc A() {}\n",
		"skip/b.ts": "export function B() {}\n",
	})
	topo := nodeTopo(
		domain.Resource{
			ID: "a.A", Kind: domain.ResourceFunction, Name: "A", Description: "the needle",
			Location: domain.Location{Path: filepath.Join(dir, "keep", "a.go"), StartsAt: 3, EndsAt: 3},
		},
		domain.Resource{
			ID: "b.B", Kind: domain.ResourceFunction, Name: "B", Description: "the needle",
			Location: domain.Location{Path: filepath.Join(dir, "skip", "b.ts"), StartsAt: 1, EndsAt: 1},
		},
	)

	res, err := SearchWith(Options{Pattern: "needle", Root: dir, Type: "go"}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.Matches[0].ResourceID != "a.A" {
		t.Fatalf("type filter must scope node rows too: %+v", res.Matches)
	}

	res, err = SearchWith(Options{Pattern: "needle", Root: filepath.Join(dir, "skip")}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.Matches[0].ResourceID != "b.B" {
		t.Fatalf("path scoping must apply to node rows too: %+v", res.Matches)
	}
}

func TestNodeRowsSurfaceInFileAndCountModes(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.go": "package a\n\nfunc A() {}\n"})
	res, err := SearchWith(Options{Pattern: "needle", Root: dir, Mode: OutputFiles},
		nodeTopo(domain.Resource{
			ID: "a.A", Kind: domain.ResourceFunction, Name: "A", Description: "the needle",
			Location: domain.Location{Path: filepath.Join(dir, "a.go"), StartsAt: 3, EndsAt: 3},
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Counts[res.Files[0]] != 1 {
		t.Fatalf("a description-only hit must still list its file: %+v %+v", res.Files, res.Counts)
	}
}

func TestNodeSearchIsANoOpWithoutTopology(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.go": "package a\n\nfunc A() {}\n"})
	res, err := SearchWith(Options{Pattern: "needle", Root: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 0 {
		t.Fatalf("no topology means no node rows: %+v", res.Matches)
	}
}
