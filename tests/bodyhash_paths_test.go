package tests_test

import (
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// A description follows moved code by its body fingerprint, and the fingerprint is only there
// if the scan path that last wrote the row put it there. There are FOUR such paths -- the cold
// scan, the full rescan, the incremental scan and the scoped partial path -- and each one
// writes the resources table itself. A path that forgets to stamp does not fail: it writes a
// row with an empty hash over a row that had one, and the loss is silent until someone moves
// that code and the description does not follow.
//
// The scoped partial path is the one that was actually missed while building this. It takes
// the dominant case -- a single-file Go edit with no cross-file caller -- so forgetting it
// would have meant the fingerprints were wrong for most edits an agent makes.

const hashProbeSrc = `package pkg

func Summarize(rows []int) (int, int) {
	total := 0
	count := 0
	for _, row := range rows {
		if row == 0 {
			continue
		}
		total += row
		count++
	}
	return total, count
}
`

// hashOf reads one resource's fingerprints out of the project's own database.
func hashOf(t *testing.T, dir, id string) domain.Resource {
	t.Helper()
	topo, err := helper.ReadDb(filepath.Join(dir, ".aracne", "topology.db"))
	if err != nil {
		t.Fatalf("read topology: %v", err)
	}
	res, ok := topo.Resources[id]
	if !ok {
		ids := make([]string, 0, len(topo.Resources))
		for k := range topo.Resources {
			ids = append(ids, k)
		}
		t.Fatalf("%q not in topology; have %v", id, ids)
	}
	return res
}

func requireStamped(t *testing.T, dir, id, after string) domain.Resource {
	t.Helper()
	res := hashOf(t, dir, id)
	if res.ExactHash == "" || res.NormHash == "" || res.NormLines == 0 {
		t.Fatalf("after %s: %q has no body fingerprint (exact=%q norm=%q lines=%d) -- that scan "+
			"path writes the resources table without stamping, so a later move of this code "+
			"has nothing to match on", after, id, res.ExactHash, res.NormHash, res.NormLines)
	}
	return res
}

func TestBodyHashesSurviveEveryScanPath(t *testing.T) {
	dir := t.TempDir()
	writeFileMk(t, dir, "go.mod", "module example.com/h\n\ngo 1.21\n")
	writeFileMk(t, dir, "pkg/a.go", hashProbeSrc)
	const id = "example.com/h/pkg.Summarize"

	// 1. Cold full scan.
	mustRun(t, dir, "scan", "--all")
	cold := requireStamped(t, dir, id, "scan --all")

	// 2. Full rescan of an unchanged tree must not disturb them.
	mustRun(t, dir, "scan", "--all")
	if again := requireStamped(t, dir, id, "a second scan --all"); again.ExactHash != cold.ExactHash {
		t.Errorf("a rescan of unchanged source moved the exact hash: %q -> %q", cold.ExactHash, again.ExactHash)
	}

	// 3. A single-file Go edit with no cross-file caller: the scoped PARTIAL path.
	writeFileMk(t, dir, "pkg/a.go", hashProbeSrc+`
func Helper(xs []int) int {
	n := 0
	for range xs {
		n++
	}
	return n
}
`)
	mustRun(t, dir, "scan")
	edited := requireStamped(t, dir, id, "an incremental scan")
	if edited.ExactHash != cold.ExactHash {
		t.Errorf("an edit elsewhere in the file changed Summarize's hash: %q -> %q",
			cold.ExactHash, edited.ExactHash)
	}
	requireStamped(t, dir, "example.com/h/pkg.Helper", "an incremental scan (new symbol)")

	// 4. The single-file hook path.
	writeFileMk(t, dir, "pkg/a.go", hashProbeSrc)
	mustRun(t, dir, "update-file", "pkg/a.go")
	requireStamped(t, dir, id, "update-file")

	// 5. Hard scan: everything is rebuilt from source.
	mustRun(t, dir, "scan", "--hard")
	hard := requireStamped(t, dir, id, "scan --hard")
	if hard.ExactHash != cold.ExactHash {
		t.Errorf("scan --hard produced a different hash for identical source: %q vs %q",
			cold.ExactHash, hard.ExactHash)
	}
}

// TestEditingABodyMovesItsStoredHash. The hash is only useful if it tracks the body, and it is
// stored on a row that is rewritten only when resourceSignature says something changed -- so
// the hash has to be part of that signature. Without it, editing a body in place (same span,
// same signature) leaves the PREVIOUS body's hash in the database.
func TestEditingABodyMovesItsStoredHash(t *testing.T) {
	dir := t.TempDir()
	writeFileMk(t, dir, "go.mod", "module example.com/h2\n\ngo 1.21\n")
	writeFileMk(t, dir, "pkg/a.go", hashProbeSrc)
	const id = "example.com/h2/pkg.Summarize"

	mustRun(t, dir, "scan", "--all")
	before := requireStamped(t, dir, id, "scan --all")

	// Same span, same signature, same name: only the arithmetic moves.
	writeFileMk(t, dir, "pkg/a.go", replaceOnce(hashProbeSrc, "total += row", "total -= row"))
	mustRun(t, dir, "scan")
	after := requireStamped(t, dir, id, "an in-place body edit")

	if after.ExactHash == before.ExactHash {
		t.Error("an in-place body edit left the stored hash unchanged -- it is stale, and a " +
			"move matcher would compare against the body that used to be there")
	}
}

func replaceOnce(s, old, new string) string {
	i := indexOf(s, old)
	if i < 0 {
		panic("anchor not found: " + old)
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
