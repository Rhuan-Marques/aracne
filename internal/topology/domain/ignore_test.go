package domain

import "testing"

const ignoreRoot = "/repo"

func TestIgnoreMatcher_DirOnlyFloating(t *testing.T) {
	m := BuildIgnoreMatcher(ignoreRoot, []string{"*my_folder/"})

	// The directory itself and its whole subtree are excluded, at any depth.
	if !m.MatchDir("/repo/a/xmy_folder") {
		t.Errorf("expected MatchDir to exclude a directory ending in my_folder")
	}
	if !m.Match("/repo/a/xmy_folder/inner/file.go") {
		t.Errorf("expected Match to exclude a file inside an ignored directory")
	}
	// A file whose own name merely contains "my_folder" is NOT excluded: the
	// pattern is directory-only.
	if m.Match("/repo/my_folder_notes.go") {
		t.Errorf("dir-only pattern must not match a file by its basename")
	}
	// A plain file literally named like the pattern is not excluded either.
	if m.Match("/repo/xmy_folder") {
		t.Errorf("dir-only pattern must not exclude a file named like the dir")
	}
}

func TestIgnoreMatcher_FloatingAnyDepth(t *testing.T) {
	m := BuildIgnoreMatcher(ignoreRoot, []string{"gen"})
	for _, dir := range []string{"/repo/gen", "/repo/a/b/gen"} {
		if !m.MatchDir(dir) {
			t.Errorf("expected floating pattern to match %q at any depth", dir)
		}
	}
	if !m.Match("/repo/a/gen/x.go") {
		t.Errorf("expected file under a floating-matched dir to be excluded")
	}
	if m.MatchDir("/repo/a/generated") {
		t.Errorf("floating pattern must match a full segment, not a prefix")
	}
}

func TestIgnoreMatcher_Anchored(t *testing.T) {
	m := BuildIgnoreMatcher(ignoreRoot, []string{"**/gen", "/build"})

	if !m.MatchDir("/repo/a/b/gen") {
		t.Errorf("expected **/gen to match a nested gen directory")
	}
	if !m.Match("/repo/a/b/gen/out.go") {
		t.Errorf("expected file under **/gen match to be excluded")
	}
	// Leading-slash anchoring: only the root-level build is excluded.
	if !m.MatchDir("/repo/build") {
		t.Errorf("expected /build to match the root-level build directory")
	}
	if m.MatchDir("/repo/sub/build") {
		t.Errorf("/build is root-anchored and must not match a nested build")
	}
}

func TestIgnoreMatcher_NonDirPattern(t *testing.T) {
	m := BuildIgnoreMatcher(ignoreRoot, []string{"*.tmp"})
	if !m.Match("/repo/a/x.tmp") {
		t.Errorf("expected *.tmp to exclude a matching file")
	}
	if !m.MatchDir("/repo/a/cache.tmp") {
		t.Errorf("expected *.tmp to also prune a matching directory")
	}
	if m.Match("/repo/a/x.go") {
		t.Errorf("*.tmp must not match a .go file")
	}
}

func TestIgnoreMatcher_CommentsAndBlanks(t *testing.T) {
	m := BuildIgnoreMatcher(ignoreRoot, []string{"# a comment", "", "   ", "node_modules/"})
	if len(m.rules) != 1 {
		t.Fatalf("expected blanks and comments to be skipped, got %d rules", len(m.rules))
	}
	if !m.MatchDir("/repo/pkg/node_modules") {
		t.Errorf("expected node_modules/ to be honored")
	}
}

func TestIgnoreMatcher_EmptyPatternsSkipped(t *testing.T) {
	// "/" collapses to an empty pattern and must be dropped, leaving a matcher
	// that excludes nothing (rather than everything).
	m := BuildIgnoreMatcher(ignoreRoot, []string{"/"})
	if len(m.rules) != 0 {
		t.Fatalf("expected no rules, got %d", len(m.rules))
	}
	if m.Match("/repo/anything.go") || m.MatchDir("/repo/anything") {
		t.Errorf("an empty pattern set must exclude nothing")
	}
}

func TestIgnoreMatcher_NilSafe(t *testing.T) {
	var m *IgnoreMatcher
	if m.Match("/repo/a.go") || m.MatchDir("/repo/a") {
		t.Errorf("a nil matcher must exclude nothing")
	}
}

func TestIgnoreMatcher_ActiveGlobalGates(t *testing.T) {
	defer SetActiveIgnore(nil)
	SetActiveIgnore(BuildIgnoreMatcher(ignoreRoot, []string{"*my_folder/"}))

	if !PathPruneDir("/repo/a/xmy_folder") {
		t.Errorf("expected PathPruneDir to honor the active scan.ignore matcher")
	}
	if !PathHidden("/repo/a/xmy_folder/file.go") {
		t.Errorf("expected PathHidden to honor the active scan.ignore matcher")
	}
	if PathHidden("/repo/keep/file.go") {
		t.Errorf("unrelated paths must remain visible")
	}

	SetActiveIgnore(nil)
	if PathHidden("/repo/a/xmy_folder/file.go") || PathPruneDir("/repo/a/xmy_folder") {
		t.Errorf("clearing the active matcher must disable ignore gating")
	}
}
