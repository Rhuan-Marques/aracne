package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// A BROKEN DATABASE AND A TYPO MUST NOT PRODUCE THE SAME ANSWER.
//
// `arac node count` and `arac check-updates` both report "database disk image is malformed".
// The read path did not: it caught the error from ReadAll, fell through to the raw-file
// fallback, and answered "not found in topology and not a readable file" -- the same sentence
// a mistyped id gets. That is the worst available answer, because it is the one thing the
// caller cannot check for itself: an agent told the declaration does not exist stops asking the
// graph and starts grepping the tree, or decides the code is missing and writes it again.
//
// Truncation rather than garbage bytes, because that is what a half-written database, a killed
// scan or a full disk actually leaves behind.
func corruptDB(t *testing.T, dbPath string) {
	t.Helper()
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	if len(data) < 4096 {
		t.Fatalf("fixture database is only %d bytes; truncating it proves nothing", len(data))
	}
	if err := os.WriteFile(dbPath, data[:len(data)/2], 0o644); err != nil {
		t.Fatalf("truncate db: %v", err)
	}
}

func TestReadAttributesAnUnreadableTopology(t *testing.T) {
	root, dbPath := scannedProject(t)
	_ = root

	newRead := func() *universaltools.Read {
		mgr := topology.New()
		if err := mgr.Load(dbPath); err != nil {
			t.Fatalf("load: %v", err)
		}
		return universaltools.NewRead(mgr, helper.LoadConfig(helper.ConfigPath(dbPath)), false, NewScannerRegistry())
	}

	// The fixture must answer the read BEFORE the corruption, or the assertion below passes
	// for the wrong reason.
	if out, err := newRead().ReadIDs([]string{"Serve"}, universaltools.ReadIDsOptions{}); err != nil {
		t.Fatalf("healthy fixture could not read Serve: %v\n%s", err, out)
	}

	corruptDB(t, dbPath)

	out, err := newRead().ReadIDs([]string{"Serve"}, universaltools.ReadIDsOptions{})
	if err == nil {
		t.Fatalf("a corrupt database resolved a read:\n%s", out)
	}
	var unresolved *universaltools.UnresolvedError
	if !asUnresolved(err, &unresolved) {
		// Any other error is fine too, as long as it says what went wrong.
		if !mentionsTopologyFailure(err.Error()) {
			t.Errorf("error does not attribute the failure to the topology: %v", err)
		}
		return
	}
	if !mentionsTopologyFailure(unresolved.Report) {
		t.Errorf("the report blames the id rather than the database:\n%s", unresolved.Report)
	}
	// The specific regression: the old text claimed the declaration was simply absent.
	if strings.Contains(unresolved.Report, "not a readable file") &&
		!strings.Contains(unresolved.Report, "could NOT be read") {
		t.Errorf("a corrupt database still reads as a missing declaration:\n%s", unresolved.Report)
	}
}

// A search degrading to file contents is right; doing it in silence is not. The node-name and
// description tiers vanish, so a plain-English query that only ever matched a stored
// description comes back empty and reads as "no such code".
func TestGrepSaysWhenItLostTheTopology(t *testing.T) {
	root, dbPath := scannedProject(t)
	corruptDB(t, dbPath)

	var stdout, stderr bytes.Buffer
	runGrep([]string{"--db", dbPath, "Serve", root}, &stdout, &stderr)

	if !mentionsTopologyFailure(stderr.String()) {
		t.Errorf("grep degraded to a content search without saying so; stderr was:\n%s", stderr.String())
	}
	// The content matches themselves must still be delivered, and on stdout, so anything
	// parsing the result is unaffected by the warning.
	if !strings.Contains(stdout.String(), "Serve") {
		t.Errorf("grep lost the content matches as well:\n%s", stdout.String())
	}
}

func mentionsTopologyFailure(s string) bool {
	return strings.Contains(s, "topology could NOT be read") ||
		strings.Contains(s, "topology could not be read")
}

// asUnresolved is errors.As with the concrete type, kept separate so the test reads as one
// question per line.
func asUnresolved(err error, target **universaltools.UnresolvedError) bool {
	for err != nil {
		if u, ok := err.(*universaltools.UnresolvedError); ok {
			*target = u
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
