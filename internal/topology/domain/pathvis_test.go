package domain

import (
	"path/filepath"
	"testing"
)

// testRoot is an absolute root valid on every platform.
//
// "/proj" is rooted but carries no VOLUME, and on Windows that is not an absolute path:
// BuildPathVisibility resolves its own root through filepath.Abs, which qualifies it with the
// current drive, and filepath.Rel can then relate nothing to it -- every rule matched nothing
// and every assertion here failed for a reason the matcher does not have.
func testRoot(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.FromSlash(p))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestPathVisibilityMostInternalWins(t *testing.T) {
	root := testRoot(t, "/proj")
	pv := BuildPathVisibility(root, []PathRule{
		{Path: "my_example", Hidden: true},
		{Path: "my_example/another_layer", Hidden: false},
	})

	cases := []struct {
		path string
		want bool
	}{
		// Direct file under the hidden parent is hidden.
		{"my_example/foo.go", true},
		// The parent directory itself is hidden.
		{"my_example", true},
		// The more-internal, un-hidden rule wins for its subtree.
		{"my_example/another_layer/bar.go", false},
		{"my_example/another_layer", false},
		{"my_example/another_layer/deep/baz.go", false},
		// Unrelated path is visible.
		{"other/main.go", false},
		// Outside the root is visible (never matches).
		{"../escape.go", false},
	}
	for _, c := range cases {
		abs := filepath.Join(root, filepath.FromSlash(c.path))
		if got := pv.Hidden(abs); got != c.want {
			t.Errorf("Hidden(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestPathVisibilityPruneDir(t *testing.T) {
	root := testRoot(t, "/proj")
	pv := BuildPathVisibility(root, []PathRule{
		{Path: "secret", Hidden: true},
		{Path: "my_example", Hidden: true},
		{Path: "my_example/another_layer", Hidden: false},
	})

	// A fully hidden dir with no nested rule can be pruned wholesale.
	if !pv.PruneDir(filepath.Join(root, "secret")) {
		t.Errorf("PruneDir(secret) = false, want true")
	}
	// A hidden dir that has a more-specific nested rule must be descended.
	if pv.PruneDir(filepath.Join(root, "my_example")) {
		t.Errorf("PruneDir(my_example) = true, want false (nested rule must be honored)")
	}
	// A visible dir is never pruned.
	if pv.PruneDir(filepath.Join(root, "other")) {
		t.Errorf("PruneDir(other) = true, want false")
	}
}

func TestPathVisibilityNilAndEmpty(t *testing.T) {
	var pv *PathVisibility
	if pv.Hidden("/anything") {
		t.Errorf("nil PathVisibility should hide nothing")
	}
	if pv.PruneDir("/anything") {
		t.Errorf("nil PathVisibility should prune nothing")
	}
	root := testRoot(t, "/proj")
	empty := BuildPathVisibility(root, nil)
	if empty.Hidden(filepath.Join(root, "x.go")) {
		t.Errorf("empty rules should hide nothing")
	}
}

func TestPathVisibilityNormalization(t *testing.T) {
	root := testRoot(t, "/proj")
	pv := BuildPathVisibility(root, []PathRule{
		{Path: "./gen/", Hidden: true},  // leading ./ and trailing slash
		{Path: "", Hidden: true},        // dropped
		{Path: "../oops", Hidden: true}, // escaping, dropped
	})
	if !pv.Hidden(filepath.Join(root, "gen", "out.go")) {
		t.Errorf("normalized rule './gen/' should hide gen/out.go")
	}
	if len(pv.rules) != 1 {
		t.Errorf("expected 1 valid rule after normalization, got %d", len(pv.rules))
	}
}

func TestActivePathVisibilityGlobal(t *testing.T) {
	t.Cleanup(func() { SetActivePathVisibility(nil) })
	root := testRoot(t, "/proj")
	SetActivePathVisibility(BuildPathVisibility(root, []PathRule{{Path: "hidden", Hidden: true}}))
	if !PathHidden(filepath.Join(root, "hidden", "x.go")) {
		t.Errorf("PathHidden should consult the active filter")
	}
	if PathHidden(filepath.Join(root, "visible", "x.go")) {
		t.Errorf("PathHidden should not hide unmatched paths")
	}
	SetActivePathVisibility(nil)
	if PathHidden(filepath.Join(root, "hidden", "x.go")) {
		t.Errorf("cleared filter should hide nothing")
	}
}
