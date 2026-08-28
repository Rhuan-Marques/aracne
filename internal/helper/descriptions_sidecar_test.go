package helper

import (
	"os"
	"path/filepath"
	"testing"

	"aracne/internal/topology/domain"
)

// The sidecar exists so that a change to any resource-ID format cannot destroy
// LLM-authored descriptions. These tests pin each match tier, because a migration that
// quietly falls back to a weaker tier (or to none) is the failure mode that matters.

func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

const shapesSrc = `class Circle:
    def area(self):
        return 3.14

def make():
    return Circle()
`

// buildTopo returns a small Python-shaped topology over one real file.
// idFor lets a test restate every ID under a different scheme.
func buildTopo(t *testing.T, root string, idFor func(string) string, described bool) *domain.Topology {
	t.Helper()
	path := writeFile(t, root, "src/shapes.py", shapesSrc)
	desc := func(s string) string {
		if described {
			return s
		}
		return ""
	}
	classID := idFor("shapes.Circle")
	topo := &domain.Topology{Root: root, Resources: map[string]domain.Resource{}}
	topo.Resources[classID] = domain.Resource{
		ID: classID, Kind: domain.ResourceStruct, Name: "Circle", Language: "python",
		Description: desc("A circle."),
		Location:    domain.Location{Path: path, StartsAt: 1, EndsAt: 3},
	}
	methodID := idFor("shapes.Circle.area")
	topo.Resources[methodID] = domain.Resource{
		ID: methodID, Kind: domain.ResourceMethod, Name: "area", Language: "python",
		Description: desc("Area of the circle."),
		Location:    domain.Location{Path: path, StartsAt: 2, EndsAt: 3},
		Properties:  map[string]any{"method_from": classID},
	}
	fnID := idFor("shapes.make")
	topo.Resources[fnID] = domain.Resource{
		ID: fnID, Kind: domain.ResourceFunction, Name: "make", Language: "python",
		Description: desc("Builds a circle."),
		Location:    domain.Location{Path: path, StartsAt: 5, EndsAt: 6},
	}
	return topo
}

func identity(s string) string { return s }

func tierCounts(t *testing.T, res DescriptionImportResult) map[string]int {
	t.Helper()
	if len(res.Unmatched) > 0 {
		t.Fatalf("unmatched records: %+v", res.Unmatched)
	}
	return res.ByTier
}

func TestExportRoundTripMatchesOnExactID(t *testing.T) {
	root := t.TempDir()
	src := buildTopo(t, root, identity, true)
	recs := BuildDescriptionRecords(src, root)
	if len(recs) != 3 {
		t.Fatalf("want 3 exported records, got %d", len(recs))
	}

	// A fresh scan of the same tree: same IDs, no descriptions yet.
	dst := buildTopo(t, root, identity, false)
	res := MatchDescriptions(recs, dst, root)
	if got := tierCounts(t, res)[MatchExactID]; got != 3 {
		t.Fatalf("want 3 exact_id matches, got %d (%v)", got, res.ByTier)
	}
	if res.MatchRate() != 1 {
		t.Fatalf("want match rate 1.0, got %v", res.MatchRate())
	}
}

func TestSurvivesACompleteIDSchemeChange(t *testing.T) {
	// This is the scenario the whole file exists for: every ID is restated (the project
	// directory name is dropped, separators change) and NOT ONE description may be lost.
	root := t.TempDir()
	old := buildTopo(t, root, func(s string) string { return "myproj/src/" + s }, true)
	recs := BuildDescriptionRecords(old, root)

	newScheme := buildTopo(t, root, func(s string) string { return "src." + s }, false)
	res := MatchDescriptions(recs, newScheme, root)

	if got := tierCounts(t, res)[MatchIdentity]; got != 3 {
		t.Fatalf("want 3 path_identity matches, got %d (%v)", got, res.ByTier)
	}
	// The method must reattach to the method, not to the class of the same file.
	for _, m := range res.Matched {
		if m.Record.Name == "area" && m.ResourceID != "src.shapes.Circle.area" {
			t.Fatalf("method 'area' reattached to %q", m.ResourceID)
		}
	}
}

func TestParentIsRecordedAsANameNotAnID(t *testing.T) {
	// `method_from` holds the parent's ID, which a scheme change invalidates. Storing the
	// parent NAME is what lets the identity key survive.
	root := t.TempDir()
	topo := buildTopo(t, root, func(s string) string { return "old/" + s }, true)
	for _, r := range BuildDescriptionRecords(topo, root) {
		if r.Name == "area" && r.Parent != "Circle" {
			t.Fatalf("want parent %q, got %q", "Circle", r.Parent)
		}
		// A free function has no `method_from`, so the parent falls back to the segment
		// before its name in the ID — here the module stem. That is stable under a scheme
		// change (both sides derive it the same way) and it is what disambiguates
		// same-named members that TypeScript records as plain functions.
		if r.Name == "make" && r.Parent != "shapes" {
			t.Fatalf("free function parent = %q, want the module stem %q", r.Parent, "shapes")
		}
	}
}

func TestSurvivesAFileMoveViaSourceHash(t *testing.T) {
	root := t.TempDir()
	old := buildTopo(t, root, identity, true)
	recs := BuildDescriptionRecords(old, root)

	// Same code, different path AND different IDs: only the source hash can bridge it.
	moved := writeFile(t, root, "lib/shapes.py", shapesSrc)
	dst := &domain.Topology{Root: root, Resources: map[string]domain.Resource{
		"lib.shapes.make": {
			ID: "lib.shapes.make", Kind: domain.ResourceFunction, Name: "make",
			Location: domain.Location{Path: moved, StartsAt: 5, EndsAt: 6},
		},
	}}
	res := MatchDescriptions(recs, dst, root)
	if res.ByTier[MatchMovedSrc] != 1 {
		t.Fatalf("want 1 moved_source match, got %v", res.ByTier)
	}
	if len(res.Matched) != 1 || res.Matched[0].Record.Description != "Builds a circle." {
		t.Fatalf("wrong record restored: %+v", res.Matched)
	}
}

func TestRelPathIsRepoRelative(t *testing.T) {
	// loc_path is absolute in the DB, so it embeds the machine that scanned it. A sidecar
	// keyed on it would not survive being moved between checkouts.
	root := t.TempDir()
	topo := buildTopo(t, root, identity, true)
	for _, r := range BuildDescriptionRecords(topo, root) {
		if r.RelPath != "src/shapes.py" {
			t.Fatalf("want repo-relative %q, got %q", "src/shapes.py", r.RelPath)
		}
	}
}

func TestAmbiguousIdentityIsReportedNotGuessed(t *testing.T) {
	// Attaching a description to the wrong resource is worse than leaving it unattached.
	root := t.TempDir()
	path := writeFile(t, root, "src/shapes.py", shapesSrc)
	recs := []DescriptionRecord{{
		ID: "a", Kind: string(domain.ResourceFunction), Name: "make", Parent: "shapes",
		RelPath: "src/shapes.py", Description: "Builds a circle.",
	}}
	// Genuinely indistinguishable: same kind, same file, same name AND same enclosing
	// segment. Only an overload/duplicate looks like this.
	dst := &domain.Topology{Root: root, Resources: map[string]domain.Resource{
		"pkg.shapes.make": {ID: "pkg.shapes.make", Kind: domain.ResourceFunction, Name: "make",
			Location: domain.Location{Path: path, StartsAt: 5, EndsAt: 6}},
		"other.shapes.make": {ID: "other.shapes.make", Kind: domain.ResourceFunction, Name: "make",
			Location: domain.Location{Path: path, StartsAt: 5, EndsAt: 6}},
	}}
	res := MatchDescriptions(recs, dst, root)
	if len(res.Matched) != 0 {
		t.Fatalf("ambiguous record must not be attached, got %+v", res.Matched)
	}
	if len(res.Ambiguous) != 1 || len(res.Unmatched) != 1 {
		t.Fatalf("want 1 ambiguous + 1 unmatched, got %d / %d",
			len(res.Ambiguous), len(res.Unmatched))
	}
}

func TestSameNamedMembersInOneFileAreDistinguishedByTheirEnclosingType(t *testing.T) {
	// TypeScript records an interface member as a plain `function`, so `method_from` is
	// absent and two `install` members in one file both keyed to (function, file, install, "")
	// — ambiguous, and refused. This cost exactly 2 descriptions in each vue and svelte
	// fixture. The enclosing segment comes from the ID instead.
	root := t.TempDir()
	path := writeFile(t, root, "app.test-d.ts", "export const a = 1\nexport const b = 2\n")
	mk := func(id, desc string) domain.Resource {
		return domain.Resource{ID: id, Kind: domain.ResourceFunction, Name: "install",
			Description: desc, Location: domain.Location{Path: path, StartsAt: 1, EndsAt: 2}}
	}
	old := &domain.Topology{Root: root, Resources: map[string]domain.Resource{
		"proj/app.test-d.PluginNoOptions.install":   mk("proj/app.test-d.PluginNoOptions.install", "no options"),
		"proj/app.test-d.PluginWithoutType.install": mk("proj/app.test-d.PluginWithoutType.install", "without type"),
	}}
	recs := BuildDescriptionRecords(old, root)

	// The same two, under scheme 2 (leading project directory dropped), undescribed.
	fresh := &domain.Topology{Root: root, Resources: map[string]domain.Resource{
		"app.test-d.PluginNoOptions.install":   mk("app.test-d.PluginNoOptions.install", ""),
		"app.test-d.PluginWithoutType.install": mk("app.test-d.PluginWithoutType.install", ""),
	}}
	res := MatchDescriptions(recs, fresh, root)
	if len(res.Unmatched) != 0 {
		t.Fatalf("want 0 unmatched, got %d: %+v", len(res.Unmatched), res.Unmatched)
	}
	for _, m := range res.Matched {
		want := "no options"
		if m.ResourceID == "app.test-d.PluginWithoutType.install" {
			want = "without type"
		}
		if m.Record.Description != want {
			t.Fatalf("%s got %q, want %q", m.ResourceID, m.Record.Description, want)
		}
	}
}

func TestExistingDescriptionIsNeverOverwritten(t *testing.T) {
	root := t.TempDir()
	src := buildTopo(t, root, identity, true)
	recs := BuildDescriptionRecords(src, root)
	dst := buildTopo(t, root, identity, true) // already described
	res := MatchDescriptions(recs, dst, root)
	if len(res.Matched) != 0 || len(res.Skipped) != 3 {
		t.Fatalf("want 0 matched / 3 skipped, got %d / %d", len(res.Matched), len(res.Skipped))
	}
	if res.MatchRate() != 1 {
		t.Fatalf("skipped records still count as found; want rate 1.0, got %v", res.MatchRate())
	}
}

func TestWriteAndReadRecordsRoundTrip(t *testing.T) {
	root := t.TempDir()
	topo := buildTopo(t, root, identity, true)
	recs := BuildDescriptionRecords(topo, root)
	path := filepath.Join(root, ".aracne", DescriptionsSidecarName)
	if err := WriteDescriptionRecords(path, recs); err != nil {
		t.Fatal(err)
	}
	back, err := ReadDescriptionRecords(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(recs) {
		t.Fatalf("want %d records back, got %d", len(recs), len(back))
	}
	for i := range recs {
		if back[i] != recs[i] {
			t.Fatalf("record %d changed across the round trip:\n got %+v\nwant %+v",
				i, back[i], recs[i])
		}
	}
}

func TestReadRejectsMalformedLineWithLocation(t *testing.T) {
	// A partial restore that looks complete is worse than a loud failure.
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	os.WriteFile(path, []byte(`{"id":"a","description":"ok"}`+"\n"+`{not json`+"\n"), 0o644)
	if _, err := ReadDescriptionRecords(path); err == nil {
		t.Fatal("want an error for a malformed line, got nil")
	}
}

func TestUnreadableSourceStillExports(t *testing.T) {
	// A missing file only weakens one match tier; it must never fail an export.
	root := t.TempDir()
	topo := &domain.Topology{Root: root, Resources: map[string]domain.Resource{
		"gone.fn": {ID: "gone.fn", Kind: domain.ResourceFunction, Name: "fn",
			Description: "still worth keeping",
			Location:    domain.Location{Path: filepath.Join(root, "nope.py"), StartsAt: 1, EndsAt: 2}},
	}}
	recs := BuildDescriptionRecords(topo, root)
	if len(recs) != 1 || recs[0].SrcSHA256 != "" {
		t.Fatalf("want 1 record with an empty hash, got %+v", recs)
	}
}

func TestFileResourcesUseTheirIDAsTheirPath(t *testing.T) {
	// `file` resources store an EMPTY loc_path — the ID *is* the absolute path. Reading
	// Location.Path blindly gave every file the identity (file, "", basename, ""), so
	// `client.go` in ten packages collapsed to one key and all of them were rejected as
	// ambiguous. On the cli/cli fixture that silently dropped 15 of 804 descriptions.
	root := t.TempDir()
	a := writeFile(t, root, "api/client.go", "package api\n")
	b := writeFile(t, root, "auth/client.go", "package auth\n")
	topo := &domain.Topology{Root: root, Resources: map[string]domain.Resource{
		a: {ID: a, Kind: domain.ResourceFile, Name: "client.go", Description: "api client"},
		b: {ID: b, Kind: domain.ResourceFile, Name: "client.go", Description: "auth client"},
	}}
	recs := BuildDescriptionRecords(topo, root)
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	paths := map[string]bool{}
	for _, r := range recs {
		if r.RelPath == "" {
			t.Fatalf("file record has no rel_path: %+v", r)
		}
		paths[r.RelPath] = true
	}
	if len(paths) != 2 {
		t.Fatalf("two distinct files collapsed to one rel_path: %v", paths)
	}

	// And they must re-attach to the RIGHT file after an ID change.
	fresh := &domain.Topology{Root: root, Resources: map[string]domain.Resource{
		a: {ID: a, Kind: domain.ResourceFile, Name: "client.go"},
		b: {ID: b, Kind: domain.ResourceFile, Name: "client.go"},
	}}
	res := MatchDescriptions(recs, fresh, root)
	if len(res.Unmatched) != 0 {
		t.Fatalf("want 0 unmatched, got %d (%+v)", len(res.Unmatched), res.Unmatched)
	}
	for _, m := range res.Matched {
		want := "api client"
		if m.ResourceID == b {
			want = "auth client"
		}
		if m.Record.Description != want {
			t.Fatalf("%s got %q, want %q", m.ResourceID, m.Record.Description, want)
		}
	}
}

func TestExportIsDeterministic(t *testing.T) {
	root := t.TempDir()
	topo := buildTopo(t, root, identity, true)
	a := BuildDescriptionRecords(topo, root)
	b := BuildDescriptionRecords(topo, root)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("export is not deterministic at %d", i)
		}
	}
}
