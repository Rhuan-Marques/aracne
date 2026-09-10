package topogrep

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// plainFixture is a file with more matches than DefaultHeadLimit, plus a node whose stored
// DESCRIPTION matches the pattern -- the two additions Plain has to switch off.
func plainFixture(t *testing.T) (dir string, topo *domain.Topology) {
	t.Helper()
	dir = t.TempDir()
	var b strings.Builder
	b.WriteString("package fixture\n\n")
	for i := 0; i < DefaultHeadLimit+50; i++ {
		b.WriteString("var needle" + strings.Repeat("x", i%3) + " = 1 // needle\n")
	}
	path := filepath.Join(dir, "many.go")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	topo = &domain.Topology{
		Root: dir,
		Resources: map[string]domain.Resource{
			"fixture.Described": {
				ID:          "fixture.Described",
				Name:        "Described",
				Kind:        domain.ResourceFunction,
				Description: "finds a needle in a haystack",
				Location:    domain.Location{Path: path, StartsAt: 3, EndsAt: 4},
			},
		},
	}
	return dir, topo
}

// A CAP THE PIPELINE CANNOT SEE IS DATA IT SILENTLY LOSES.
//
// The intercepted search left HeadLimit at zero, so content mode capped at DefaultHeadLimit and
// said so on a trailer line -- which the next stage filters out, or `head -N` discards. Measured
// on this repository, `grep -rn dbPath internal/cli | grep -v _test` returned 181 lines from the
// real pipeline and 120 through the intercepted one, with nothing left in the stream to mark the
// difference.
func TestPlainIsNeverCapped(t *testing.T) {
	dir, topo := plainFixture(t)
	t.Chdir(dir)

	capped, err := SearchWith(Options{Pattern: "needle", Root: ".", Terse: true}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if !capped.Truncated {
		t.Fatal("the fixture must exceed the default head limit for this test to mean anything")
	}

	plain, err := SearchWith(Options{Pattern: "needle", Root: ".", Terse: true, Plain: true}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Truncated {
		t.Error("a piped search must not be truncated")
	}
	if len(plain.Matches) != plain.Total {
		t.Errorf("rendered %d of %d matches; a pipeline sees only what is rendered",
			len(plain.Matches), plain.Total)
	}
}

// Plain emits the rows a real grep would have printed and nothing else: no `#` resource header,
// no row a node earned on its name or description, and no trailer. Each of those is a line the
// consumer counts, filters and caps alongside the real matches.
func TestPlainEmitsOnlyTheRowsARealGrepWould(t *testing.T) {
	dir, topo := plainFixture(t)
	t.Chdir(dir)

	opt := Options{Pattern: "needle", Root: ".", Terse: true, Plain: true, LineNumbers: true}
	res, err := SearchWith(opt, topo)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Matches {
		if m.NodeHit {
			t.Fatalf("a node row survived Plain: %+v", m)
		}
	}
	out := FormatResult(res, opt)
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			t.Errorf("annotation header reached a pipeline: %q", line)
		}
		if strings.HasPrefix(line, "…") {
			t.Errorf("a trailer reached a pipeline: %q", line)
		}
	}

	// The description tier still works when a reader is the one looking.
	enriched, err := SearchWith(Options{Pattern: "haystack", Root: ".", Terse: true}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if len(enriched.Matches) == 0 {
		t.Error("a description-only match is the half a plain grep cannot reach; it must survive when not piped")
	}
	piped, err := SearchWith(Options{Pattern: "haystack", Root: ".", Terse: true, Plain: true}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if len(piped.Matches) != 0 {
		t.Errorf("a description match is a row the real command never printed: %+v", piped.Matches)
	}
}

// Rows come back in path and line order, not in the tiered ranking. The ranking decides what a
// READER sees first; a `| head -N` reading the same stream would otherwise keep a different N,
// and the first row could be a promoted title match from further down the file.
func TestPlainRendersInFileOrder(t *testing.T) {
	dir, topo := plainFixture(t)
	t.Chdir(dir)

	opt := Options{Pattern: "needle", Root: ".", Terse: true, Plain: true, LineNumbers: true}
	res, err := SearchWith(opt, topo)
	if err != nil {
		t.Fatal(err)
	}
	last := 0
	for _, m := range res.Matches {
		if m.Line < last {
			t.Fatalf("rows are out of file order: %d after %d", m.Line, last)
		}
		last = m.Line
	}
}

// The `./` a real grep echoes belongs to the shell surface only. `arac grep` and the MCP tool
// address rows by path and line unconditionally and are not imitating anything.
func TestDotSlashIsEchoedOnlyOnTheShellSurface(t *testing.T) {
	dir, topo := plainFixture(t)
	t.Chdir(dir)

	shell, err := SearchWith(Options{Pattern: "needle", Root: ".", Terse: true, Plain: true}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if len(shell.Matches) == 0 || !strings.HasPrefix(shell.Matches[0].Path, "./") {
		t.Errorf("the shell surface must echo the operand it walked: %q", shell.Matches[0].Path)
	}

	tool, err := SearchWith(Options{Pattern: "needle", Root: "."}, topo)
	if err != nil {
		t.Fatal(err)
	}
	if len(tool.Matches) == 0 || strings.HasPrefix(tool.Matches[0].Path, "./") {
		t.Errorf("the tool surface must keep its own spelling: %q", tool.Matches[0].Path)
	}
}
