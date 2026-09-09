package tests_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Byte budgets for the two tools an agent spends nearly all its context on.
//
// These exist because the numbers regressed silently once already. The aracne arm of the
// scale40 benchmark consumed MORE context than plain Claude Code, and the two causes were
// output size: `arac grep` emitted 1.8x-2.6x the bytes of `grep -rn` (with no cap of any
// kind — one call returned 3.1 MB), and `arac read` emitted a multiple of the source span
// it was asked for. Nothing measured either, so nothing noticed.
//
// A tool that replaces a native one has to be at least as cheap for the same job. These
// tests pin that as a contract rather than an intention.

// grepAnnotationBudget is the ceiling on aracne grep's ANNOTATION -- the "# id — description"
// headers and the trailing "…" notes -- as a fraction of the matched content it annotates.
// It is the executable form of topogrep.AnnotateOverheadBudget.
//
// WHY THIS REPLACED A RATIO AGAINST NATIVE GREP. The old test capped total output at 1.05x
// `grep -rn` for the same pattern, and it had been failing on main. Measured on this corpus,
// pattern "err" returned 3,755 bytes against native's 3,333 -- but the breakdown is 245 bytes
// of headers, 0 of notes, and FIFTEEN extra result rows (1,090 bytes) that native grep does
// not produce at all, because aracne also searches node names and descriptions. The tool was
// being charged for finding things `grep` cannot find, which is the one thing it is for.
//
// So the tripwire now measures what it meant to: formatting overhead. Recall is covered by
// grepBlowupBudget below, which still catches a renderer that runs away.
const grepAnnotationBudget = 0.25

// grepBlowupBudget is the loose ceiling on total output against `grep -rn`, kept as a
// blow-up alarm rather than an efficiency target. Measured at 1.13x / 1.04x / 1.00x for
// "err" / "func " / "string" on this corpus.
const grepBlowupBudget = 1.5

// readBudget is the ceiling on `arac read` output as a multiple of the raw source span of
// the resource being read. Above 1.0 buys the CONTEXT section — the neighbours and their
// descriptions — which is the product's actual value; the budget keeps that from becoming
// a subgraph dump.
//
// CALIBRATION. This ratio is very sensitive to the denominator, so the number only means
// something against a fixed corpus. Measured over ten functions of testing_ground/go it
// sits at ~2.7x, because those functions are a handful of lines each and their CONTEXT is
// proportionally large. The same measurement over real code (cli/cli, shipped default
// config) is ~1.66x. The budget is therefore set for THIS corpus with modest headroom: it
// is a regression tripwire, not a target. If you tighten the renderer, lower it.
//
// RAISED 3.0 -> 3.5 when identification_mode "line_range" became the default. Naming each
// context entry by its span costs a FIXED ~25 bytes per entry: measured at +692 bytes over
// these ten reads, 2.90x -> 3.38x (+16.5%). A fixed cost against this corpus's ~145-byte
// functions is the worst case there is; against the real-code denominator above the same
// bytes are ~1.6%. Recalibrated rather than removed -- it still catches a runaway renderer.
const readBudget = 3.5

// scanCorpus scans a copy of testing_ground/go and returns its directory.
func scanCorpus(t *testing.T) string {
	t.Helper()
	root := copyGoCorpus(t)
	mustRun(t, root, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	return root
}

// nativeGrepBytes is `grep -rn <pattern> --include=*.go .` measured in bytes. Returns 0
// when grep is unavailable, which skips the comparison rather than failing on the tool.
func nativeGrepBytes(dir, pattern string) int {
	cmd := exec.Command("grep", "-rn", pattern, "--include=*.go", ".")
	cmd.Dir = dir
	out, _ := cmd.Output() // exit status 1 just means "no matches"
	return len(out)
}

func TestGrepStaysWithinNativeBudget(t *testing.T) {
	root := scanCorpus(t)

	// Broad patterns are the ones that matter: a narrow search is cheap either way, while
	// a sweep is where an uncapped, verbosely-annotated result blew up.
	for _, pattern := range []string{"func ", "err", "string"} {
		native := nativeGrepBytes(root, pattern)
		if native == 0 {
			t.Skipf("native grep unavailable or no matches for %q", pattern)
		}
		// --head-limit -1 disables the cap so this compares FORMAT against format rather
		// than measuring truncation, which would flatter aracne for the wrong reason.
		out := mustRun(t, root, "grep", "--head-limit", "-1", pattern, ".")

		var annotation, content int
		for _, line := range strings.Split(out, "\n") {
			switch {
			case strings.HasPrefix(line, "# "), strings.HasPrefix(line, "\u2026 "):
				annotation += len(line) + 1
			default:
				content += len(line) + 1
			}
		}
		if content == 0 {
			t.Fatalf("grep %q returned no content rows:\n%s", pattern, out)
		}
		if ratio := float64(annotation) / float64(content); ratio > grepAnnotationBudget {
			t.Errorf("grep %q: annotation %d bytes on %d of content = %.2f, budget %.2f",
				pattern, annotation, content, ratio, grepAnnotationBudget)
		}
		if ratio := float64(len(out)) / float64(native); ratio > grepBlowupBudget {
			t.Errorf("grep %q: %d bytes vs native %d = %.2fx, blow-up budget %.2fx",
				pattern, len(out), native, ratio, grepBlowupBudget)
		} else {
			t.Logf("grep %q: %.2fx of native (%d vs %d), annotation %.1f%% of content",
				pattern, ratio, len(out), native,
				100*float64(annotation)/float64(content))
		}
	}
}

func TestGrepIsCappedByDefault(t *testing.T) {
	// The default cap is the difference between a search and a context dump. The corpus is
	// small, so make a file with more matches than the cap rather than relying on it.
	root := scanCorpus(t)
	var big strings.Builder
	for i := 0; i < 500; i++ {
		big.WriteString("// cap_probe_token\n")
	}
	writeFile(t, filepath.Join(root, "cap_probe.txt"), big.String())

	capped := mustRun(t, root, "grep", "cap_probe_token", ".")
	uncapped := mustRun(t, root, "grep", "--head-limit", "-1", "cap_probe_token", ".")
	if len(capped) >= len(uncapped) {
		t.Fatalf("default search is not capped: %d bytes capped vs %d uncapped",
			len(capped), len(uncapped))
	}
	if !strings.Contains(capped, "not shown") {
		t.Errorf("a truncated result must say so; got:\n%s", tail(capped, 300))
	}
}

func TestGrepReportsNoMatchesExplicitly(t *testing.T) {
	// `arac grep` answers in grep's own vocabulary: nothing matched is exit 1, and the
	// explanation goes to STDERR. It used to exit 0 and print the sentence on stdout, so a
	// script or an agent branching on the status could not tell "found nothing" from "found
	// something", and `files=$(arac grep ...)` captured the prose as if it were a filename.
	//
	// The message itself stays -- an empty answer with no explanation reads as a broken
	// command -- it simply goes where a diagnostic goes. (The MCP `grep` tool is a separate
	// surface and keeps prose on its result, where an empty string reads as a broken tool.)
	root := scanCorpus(t)
	out, err := runLtp(t, root, "grep", "zzz_no_such_symbol_zzz", ".")
	if err == nil {
		t.Fatalf("a search that found nothing exited 0; want grep's exit 1\n%s", out)
	}
	if !strings.Contains(out, "no matches") {
		t.Fatalf("want an explicit no-match message, got %q", out)
	}

	// And a search that DID find something still exits 0.
	mustRun(t, root, "grep", "func ", ".")
}

// resourceRow is enough of a resource to locate its source span.
type resourceRow struct {
	ID       string
	Kind     string
	Path     string
	StartsAt int
	EndsAt   int
}

func TestReadStaysWithinSourceBudget(t *testing.T) {
	root := scanCorpus(t)

	// Sample functions straight out of the database rather than hardcoding IDs, so this
	// keeps working across id-scheme changes.
	rows := sampleFunctions(t, root, 10)
	if len(rows) == 0 {
		t.Skip("no function resources with a line span in the corpus")
	}

	var totalSrc, totalOut int
	for _, r := range rows {
		src := spanBytes(r.Path, r.StartsAt, r.EndsAt)
		if src == 0 {
			continue
		}
		out := len(mustRun(t, root, "read", "--kind", r.Kind, r.ID))
		totalSrc += src
		totalOut += out
	}
	if totalSrc == 0 {
		t.Skip("could not measure any source spans")
	}
	ratio := float64(totalOut) / float64(totalSrc)
	t.Logf("read: %d bytes for %d bytes of source = %.2fx over %d resources",
		totalOut, totalSrc, ratio, len(rows))
	if ratio > readBudget {
		t.Errorf("read output is %.2fx the source span, budget %.2fx", ratio, readBudget)
	}
}

// sampleFunctions pulls up to n function/method resources that have a real line span,
// reading the topology database directly rather than through a CLI listing.
func sampleFunctions(t *testing.T, root string, n int) []resourceRow {
	t.Helper()
	topo, err := helper.ReadDb(filepath.Join(root, ".aracne", "topology.db"))
	if err != nil || topo == nil {
		t.Skipf("could not read topology: %v", err)
	}
	ids := make([]string, 0, len(topo.Resources))
	for id := range topo.Resources {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic sample
	var picked []resourceRow
	for _, id := range ids {
		r := topo.Resources[id]
		if (r.Kind != domain.ResourceFunction && r.Kind != domain.ResourceMethod) ||
			r.Location.Path == "" || r.Location.StartsAt <= 0 ||
			r.Location.EndsAt < r.Location.StartsAt {
			continue
		}
		picked = append(picked, resourceRow{
			ID: id, Kind: string(r.Kind), Path: r.Location.Path,
			StartsAt: r.Location.StartsAt, EndsAt: r.Location.EndsAt,
		})
		if len(picked) == n {
			break
		}
	}
	return picked
}

// spanBytes is the byte length of lines [start, end] of a file, or 0 if unreadable.
func spanBytes(path string, start, end int) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	lines := strings.Split(string(data), "\n")
	if start < 1 || end > len(lines) {
		return 0
	}
	return len(strings.Join(lines[start-1:end], "\n"))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// terminalWindowBudget is the ceiling on an intercepted windowed read, as a multiple of the
// bytes the plain command would have printed.
//
// WHY IT NEEDS ITS OWN NUMBER. readBudget above measures a RESOURCE read against the resource's
// source span, where 2-3x is the price of the context block. A window is a much smaller
// denominator -- `tail -2` asks for two lines -- so the same ratio would be meaningless: the
// enclosing signature alone can exceed it. What must hold instead is the thing the surface is
// for: asking for a slice must cost dramatically less than asking for the file. This is set
// against the WHOLE FILE, which is what `cat` would have cost and what the old guard proxy
// actually returned (measured at 5.8x the request on average, 41.9x at worst -- the regression
// that made a window-aware answer necessary in the first place).
const terminalWindowBudget = 0.5

// A windowed read must be a window. The failure this pins is not hypothetical: mapping a line
// range to the resources covering it and reading those whole -- which is what the read tool
// does for a `file.go:10-20` id -- returns the enclosing declaration entire, and for a small
// window inside a large function that is most of the file.
func TestInterceptedWindowIsCheaperThanTheFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module big\n\ngo 1.21\n")

	// One long function, so a two-line window sits deep inside a single declaration -- the
	// shape where "read the covering resource" and "read the window" diverge most.
	var body strings.Builder
	body.WriteString("package big\n\n// Grow appends many lines.\nfunc Grow() int {\n\tn := 0\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&body, "\tn += %d // filler line %d\n", i, i)
	}
	body.WriteString("\treturn n\n}\n")
	writeFile(t, filepath.Join(root, "big.go"), body.String())

	mustRun(t, root, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")

	onDisk, err := os.ReadFile(filepath.Join(root, "big.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"tail", "-2", "big.go"},
		{"head", "-6", "big.go"},
		{"sed", "-n", "200,205p", "big.go"},
	} {
		out := mustRun(t, root, append([]string{"cmd", "--"}, argv...)...)
		ratio := float64(len(out)) / float64(len(onDisk))
		if ratio > terminalWindowBudget {
			t.Errorf("`%s` returned %d bytes against a %d-byte file = %.2fx, budget %.2fx\n%s",
				strings.Join(argv, " "), len(out), len(onDisk), ratio, terminalWindowBudget, out)
			continue
		}
		t.Logf("`%s`: %d bytes, %.3fx the file", strings.Join(argv, " "), len(out), ratio)
	}
}
