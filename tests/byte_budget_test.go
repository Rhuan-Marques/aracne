package tests_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
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

// grepBudget is the ceiling on `arac grep` output as a fraction of `grep -rn` for the same
// pattern, comparing like for like (annotation off, no cap).
const grepBudget = 1.05

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
const readBudget = 3.0

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
		got := len(mustRun(t, root, "grep", "--head-limit", "-1", pattern, "."))
		ratio := float64(got) / float64(native)
		if ratio > grepBudget {
			t.Errorf("grep %q: %d bytes vs native %d = %.2fx, budget %.2fx",
				pattern, got, native, ratio, grepBudget)
		} else {
			t.Logf("grep %q: %.2fx of native (%d vs %d)", pattern, ratio, got, native)
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
	// An empty tool result reads to a model as a broken tool, not as "nothing matched".
	root := scanCorpus(t)
	out := mustRun(t, root, "grep", "zzz_no_such_symbol_zzz", ".")
	if !strings.Contains(out, "no matches") {
		t.Fatalf("want an explicit no-match message, got %q", out)
	}
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
