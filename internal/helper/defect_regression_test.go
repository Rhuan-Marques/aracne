package helper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// TestEnsureConfigKeepsAnUnreadableFile pins AR-02.
//
// LoadConfigStrict returned the same `false` for "could not read this file" and "this is not
// the current schema", and EnsureConfig treats false as licence to overwrite. A config that was
// momentarily unreadable -- a permission change, a lock, an NFS hiccup -- was therefore replaced
// by defaults, taking the project's mode, feature flags, ignore rules and agent tool lists with
// it, under a message claiming the schema was wrong when it had never been read.
func TestEnsureConfigKeepsAnUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; a 0000 file is still readable")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := []byte(`{"mode":"intercept_id","features":{"chat":true}}`)
	if err := os.WriteFile(path, original, 0o000); err != nil {
		t.Fatal(err)
	}

	cfg := EnsureConfig(path)
	if cfg == nil {
		t.Fatal("EnsureConfig returned nil")
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("EnsureConfig overwrote a config it could not read:\n got: %s\nwant: %s", after, original)
	}
}

// TestEnsureConfigStillReplacesAnOldSchema is the other half of AR-02: a file that WAS read
// and is not this schema keeps its clean break.
func TestEnsureConfigStillReplacesAnOldSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"totally":"unrelated"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	EnsureConfig(path)
	after, _ := os.ReadFile(path)
	if !validConfig(after) {
		t.Fatalf("an old-format config should have been replaced with defaults, got: %s", after)
	}
}

// TestResourceSignatureUsesTheStoredDescription pins AR-14.
//
// The writers persist domain.DescriptionForStorage(kind, desc); the signature hashed the raw
// text. A freshly-parsed resource could therefore never match what had been stored for it, so
// DiffResources reported every doc-commented resource as changed on every scan and
// WriteIncremental rewrote its row and all of its connection rows.
func TestResourceSignatureUsesTheStoredDescription(t *testing.T) {
	parsed := domain.Resource{
		ID: "x", Kind: domain.ResourceFunction, Name: "F",
		Description: "line one\nline two",
		Location:    domain.Location{Path: "f.go", StartsAt: 1, EndsAt: 2},
	}
	stored := parsed
	stored.Description = domain.DescriptionForStorage(parsed.Kind, parsed.Description)

	if ResourceSignatureOf(parsed) != ResourceSignatureOf(stored) {
		t.Fatalf("a resource must fingerprint the same before and after storage\n parsed: %q\n stored: %q",
			parsed.Description, stored.Description)
	}

	// An over-budget doc comment stores as nothing at all, and must fingerprint that way too.
	long := parsed
	for i := 0; i < 200; i++ {
		long.Description += "a"
	}
	dropped := long
	dropped.Description = domain.DescriptionForStorage(long.Kind, long.Description)
	if dropped.Description != "" {
		t.Fatalf("expected an over-budget description to store as empty, got %q", dropped.Description)
	}
	if ResourceSignatureOf(long) != ResourceSignatureOf(dropped) {
		t.Fatal("an over-budget description must not make a resource look changed on every scan")
	}
}

// TestSyncManifestStampsAttemptedFiles pins AR-03.
//
// The kept set was derived purely from the topology's file nodes, so a file the scanner could
// not parse produced no node, was never stamped, and DiffScanFiles reported it as `added` on
// every later run -- re-parsing it forever, which under the default scan.pre_tool means once
// per tool call.
func TestSyncManifestStampsAttemptedFiles(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good.go")
	broken := filepath.Join(dir, "broken.go")
	for _, p := range []string{good, broken} {
		if err := os.WriteFile(p, []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Only `good` produced a node; `broken` is the file the scanner choked on.
	topo := &domain.Topology{
		Root:     dir,
		Language: "go",
		Resources: map[string]domain.Resource{
			good: {ID: good, Kind: domain.ResourceFile, Name: "good.go", Language: "go"},
		},
	}
	SyncManifest(topo, dbPath, SnapshotManifest([]string{broken}))

	manifest := ReadManifest(ManifestPath(dbPath))
	if _, ok := manifest[broken]; !ok {
		t.Fatalf("a file the scan attempted must be stamped even when it produced no node; manifest = %v", manifest)
	}

	added, _, _, err := DiffScanFiles(dir, "go", ManifestPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range added {
		if p == broken {
			t.Fatal("an unparseable file is still reported as added on the next scan")
		}
	}
}

// TestSyncManifestIgnoresAttemptedPathsThatAreGone guards the one rule the stamp must not
// break: a path that no longer exists must not be resurrected into the manifest, or the
// deletion sweep can never remove it.
func TestSyncManifestIgnoresAttemptedPathsThatAreGone(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone.go")

	topo := &domain.Topology{Root: dir, Language: "go", Resources: map[string]domain.Resource{}}
	// A stamp handed in for it (the file was deleted after the snapshot) must still be dropped.
	SyncManifest(topo, dbPath, FileManifest{gone: "2020-01-01T00:00:00Z"})

	if _, ok := ReadManifest(ManifestPath(dbPath))[gone]; ok {
		t.Fatal("a deleted path must not be stamped back into the manifest")
	}
}

// TestDescriptionAttemptsSurviveTheProcess pins the persistence half of AR-21: the lazy
// filler's "already tried this" record was process-scoped, and every intercepted shell command
// is a new process, so it never suppressed anything where it mattered.
func TestDescriptionAttemptsSurviveTheProcess(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	res := domain.Resource{
		ID: "pkg.F", Kind: domain.ResourceFunction, Name: "F",
		Location: domain.Location{Path: "f.go", StartsAt: 10, EndsAt: 20},
	}
	fp := DescriptionAttemptFingerprint(res)

	if err := RecordDescriptionAttempts(dbPath, map[string]string{res.ID: fp}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDescriptionAttempts(dbPath, []string{res.ID, "pkg.Other"})
	if err != nil {
		t.Fatal(err)
	}
	if got[res.ID] != fp {
		t.Fatalf("recorded attempt not read back: %v", got)
	}
	if _, ok := got["pkg.Other"]; ok {
		t.Fatal("an id with no record must be absent")
	}

	// The record expires when the code moves, so edited code is always retried.
	moved := res
	moved.Location.StartsAt = 40
	moved.Location.EndsAt = 55
	if DescriptionAttemptFingerprint(moved) == fp {
		t.Fatal("a moved declaration must fingerprint differently, or a failure never expires")
	}

	// Clearing descriptions must clear the record, or "regenerate this" becomes "never again".
	if err := ClearDescriptionAttempts(dbPath); err != nil {
		t.Fatal(err)
	}
	got, err = ReadDescriptionAttempts(dbPath, []string{res.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("descriptions clear must drop the attempt record, got %v", got)
	}
}

// TestReadDescriptionAttemptsToleratesAMissingTable: a project that has never filled a
// description has no table, and that must read as "nothing attempted" rather than fail.
func TestReadDescriptionAttemptsToleratesAMissingTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteDb(&domain.Topology{Root: dir, Language: "go"}, dbPath); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDescriptionAttempts(dbPath, []string{"pkg.F"})
	if err != nil {
		t.Fatalf("a missing table must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no attempts, got %v", got)
	}
}
