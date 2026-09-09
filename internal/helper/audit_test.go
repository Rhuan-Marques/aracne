//go:build audit

package helper

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// A-03: EffectiveMode/EffectiveContractVerbosity both promise "Validate() is where a typo is
// reported, loudly and once". normalizeConfig stamps the coerced value onto the struct during
// LoadConfigStrict, and every Validate() call site (cli/serve.go, cli/setup.go,
// chat/manager.go) loads through it -- so the check can never see the typo.
func TestAudit_ValidateRejectsAModeTypoAfterLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"mode":"intercept_lineranges","contract_verbosity":"hgih","read":{"context_filter":"ful"}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, ok := LoadConfigStrict(path)
	if !ok {
		t.Fatalf("config did not load as the current schema")
	}
	if err := cfg.Validate(); err == nil {
		t.Errorf("Validate() accepted a config with three unknown values;\n"+
			"  mode              %q -> silently resolved to %q\n"+
			"  contract_verbosity %q -> silently resolved to %q\n"+
			"  read.context_filter %q -> silently resolved to %q\n"+
			"the project loses interception with nothing reported",
			"intercept_lineranges", cfg.Mode,
			"hgih", cfg.ContractVerbosity,
			"ful", cfg.Read.ContextFilter)
	}
}

// A-04: openSQLite passes `cache=shared&_busy_timeout=N` in the DSN. Those are the
// mattn/go-sqlite3 spellings; modernc.org/sqlite's applyQueryParams understands only
// _pragma/_time_format/_txlock/_timezone/vfs and ignores every other key silently. The
// driver's own spelling is `_pragma=busy_timeout(N)`.
func TestAudit_DSNBusyTimeoutReachesTheDriver(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")

	read := func(dsn string) int {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var ms int
		if err := db.QueryRow("PRAGMA busy_timeout").Scan(&ms); err != nil {
			t.Fatal(err)
		}
		return ms
	}

	viaDSN := read(dbPath + "?cache=shared&_busy_timeout=10000")
	viaPragma := read(dbPath + "?_pragma=busy_timeout(10000)")

	if viaDSN != sqliteBusyTimeoutMillis {
		t.Errorf("the DSN aracne opens with yields busy_timeout=%d, not %d;\n"+
			"the driver's own spelling (_pragma) yields %d, so both DSN parameters "+
			"(cache=shared and _busy_timeout) are dead",
			viaDSN, sqliteBusyTimeoutMillis, viaPragma)
	}
}

// A-05: SyncManifest stamps each file with the mtime it has AFTER the scan, not the mtime the
// parser saw. A write that lands between parse and stamp is therefore recorded as already
// indexed, and no later incremental scan will ever pick it up.
func TestAudit_ManifestDoesNotSwallowAWriteDuringTheScan(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "a.go")
	if err := os.WriteFile(src, []byte("package p\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}

	// The graph as the scanner parsed it, at T0.
	topo := &domain.Topology{
		Root:     root,
		Language: "go",
		Resources: map[string]domain.Resource{
			src: {ID: src, Kind: domain.ResourceFile, Name: "a.go", Language: "go"},
		},
	}

	// The write that races the scan: the parser has already read the old bytes.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(src, []byte("package p\n\nfunc A(x int) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// ...and only now does the scan finish and stamp the manifest.
	SyncManifest(topo, dbPath)

	_, modified, _, err := DiffScanFiles(root, "go", ManifestPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(modified) == 0 {
		t.Errorf("the write that landed during the scan is invisible to every later "+
			"incremental scan: DiffScanFiles reports no modification for %s, while the "+
			"database still holds the pre-write parse", src)
	}
}
