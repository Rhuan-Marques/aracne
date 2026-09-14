package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/toolspec"
)

// A backslash escapes the next character, and the scanner used to know only quotes. So an
// escaped `;` was cut as a separator and an escaped `"` ended a string early, and the guard
// spliced `arac cmd --` into what bash hands echo as an argument: the output the command
// printed changed, with nothing to show it had.
func TestBackslashEscapesDoNotSplitCommands(t *testing.T) {
	// THE POSIX READING, pinned rather than inherited: on Windows a backslash is a path
	// separator and splitCommandSegments does not treat it as an escape at all (see
	// backslashEscapes). What this test is about is the escape itself, so it asks for the
	// reading it is describing and gets it on every platform.
	orig := backslashEscapes
	backslashEscapes = true
	t.Cleanup(func() { backslashEscapes = orig })

	texts := func(command string) []string {
		var out []string
		for _, s := range splitCommandSegments(command) {
			out = append(out, strings.ReplaceAll(s.text, string(quotedSpace), " "))
		}
		return out
	}
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{`echo \; grep -n x f.go`, []string{`echo ; grep -n x f.go`}},
		{`echo "x\" ; grep -n x f.go ; echo \""`, []string{`echo x" ; grep -n x f.go ; echo "`}},
		{`echo $'a\'; grep -n x f.go'`, []string{`echo $a'; grep -n x f.go`}},
		{`echo a\| grep x f.go`, []string{`echo a| grep x f.go`}},
		// An escaped backslash escapes nothing after it: the `;` is a real separator.
		{`echo a\\; grep -n x f.go`, []string{`echo a\`, ` grep -n x f.go`}},
		// Single quotes escape nothing at all.
		{`echo 'a\'; grep -n x f.go`, []string{`echo a\`, ` grep -n x f.go`}},
	} {
		if got := texts(tc.command); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q split into %q, want %q", tc.command, got, tc.want)
		}
	}

	// An escaped space holds a word together the way the shell does, and the escape itself is
	// removed, so the argv the guard classifies is the one the command receives.
	segs := splitCommandSegments(`grep -rn foo\ bar\. .`)
	if got := segmentArgv(segs[0]); !reflect.DeepEqual(got, []string{"grep", "-rn", "foo bar.", "."}) {
		t.Errorf("argv = %q", got)
	}
	// `\>` is a literal, not a redirection -- and the `>` after it still is one.
	if segs := splitCommandSegments(`echo \>> out`); !segs[0].redirectsOut {
		t.Error("`\\>> out` appends to out: the escaped `>` must not swallow the real operator")
	}
	if segs := splitCommandSegments(`echo \> out`); segs[0].redirectsOut {
		t.Error("`\\> out` prints `> out`: nothing is redirected")
	}
}

// The rewrite end to end: an escaped separator no longer produces a splice, a real one still
// does, and the command text around the splice is the model's own, byte for byte.
func TestEscapedSeparatorsAreNotRewrittenInto(t *testing.T) {
	// THE POSIX READING, pinned rather than inherited: on Windows a backslash is a path
	// separator and splitCommandSegments does not treat it as an escape at all (see
	// backslashEscapes). What this test is about is the escape itself, so it asks for the
	// reading it is describing and gets it on every platform.
	orig := backslashEscapes
	backslashEscapes = true
	t.Cleanup(func() { backslashEscapes = orig })

	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")

	for _, cmd := range []string{
		`echo \; grep -rn Serve ` + app,
		`echo "x\" ; grep -rn Serve ` + app + ` ; echo \""`,
		`printf '%s\n' $'a\'; grep -rn Serve ` + app + `'`,
	} {
		if got := rewriteOf(t, dbPath, cmd); got != "" {
			t.Errorf("%q: the rewrite landed inside an argument:\n%s", cmd, got)
		}
	}

	cmd := `echo a\\; grep -rn "Serve\|main" ` + app
	got := rewriteOf(t, dbPath, cmd)
	head := `echo a\\; `
	if !strings.HasPrefix(got, head) || !strings.HasSuffix(got, ` cmd -- grep -rn "Serve\|main" `+app) {
		t.Errorf("%q: want the grep segment rewritten and every other byte kept, got %q", cmd, got)
	}
}

// The shapes interception exists for must survive the scanner change: a search, a piped search,
// ripgrep, and the modelled reads on an indexed file.
func TestCommonShapesAreStillIntercepted(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")
	for _, cmd := range []string{
		"grep -rn Serve " + root,
		"grep -rn 'Serve\\|main' " + root + " | head -20",
		`grep -rnE "func\s+Serve" ` + root,
		"rg Serve",
		"rg -n Serve " + root,
		"head -20 " + app,
		"cat " + app,
		"sed -n '1,4p' " + app,
		"cd " + root + " && grep -rn Serve .",
	} {
		if rewriteOf(t, dbPath, cmd) == "" {
			t.Errorf("%q is no longer intercepted", cmd)
		}
	}
}

// A `cd` in the command line moves every relative path after it, and it is the one working
// directory the hook can see. Resolving those paths against the project root made a read of a
// file in another directory look like a read of the project's own file.
func TestACdInTheCommandMovesTheOperandsAfterIt(t *testing.T) {
	const root = "/repo/worktree"
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{"cd /tmp/other && cat shapes/shape.go", []string{"/tmp/other", "/tmp/other/shapes/shape.go"}},
		{"cd /tmp/other; cat shapes/shape.go /repo/worktree/a.go", []string{"/tmp/other", "/tmp/other/shapes/shape.go", "/repo/worktree/a.go"}},
		{"cd sub && cat a.go", []string{"sub/a.go"}},
		{"cd /tmp && cd x && cat a.go", []string{"/tmp", "/tmp/x/a.go"}},
		{"cat a.go && cd /tmp/other", []string{"a.go", "/tmp/other"}},
		// A change the hook cannot follow keeps today's reading of everything after it.
		{"cd - && cat a.go", []string{"a.go"}},
		{`cd "$DIR" && cat a.go`, []string{"a.go"}},
		{"(cd /tmp/other && cat a.go); cat b.go", []string{"/tmp/other", "a.go", "b.go"}},
		{"cd /tmp/other | cat a.go", []string{"/tmp/other", "a.go"}},
		{"cd /tmp/other & cat a.go", []string{"/tmp/other", "a.go"}},
		{"popd && cat a.go", []string{"a.go"}},
		// No cd at all: unchanged.
		{"cat a.go /repo/worktree/b.go", []string{"a.go", "/repo/worktree/b.go"}},
	} {
		if got := commandPaths(tc.command); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("commandPaths(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}

	if !operatesOutsideProject("cd /tmp/other && cat shapes/shape.go", root) {
		t.Error("a read of /tmp/other/shapes/shape.go is outside the project")
	}
	for _, cmd := range []string{
		"cd /repo/worktree && cat shapes/shape.go", // into the project: still inside
		"cd /repo/worktree/sub && cat a.go",
		"cd /tmp/other && cat /repo/worktree/a.go", // an absolute operand is not moved
		"cd - && cat a.go",                         // unknowable: relative, assumed inside
		"cat shapes/shape.go",                      // no cd: unchanged
	} {
		if operatesOutsideProject(cmd, root) {
			t.Errorf("%q touches the project (or cannot be placed) and must stay engaged", cmd)
		}
	}
}

// The user-visible half: with blocked_tools ["read"] in mcp mode, a read of a file in another
// directory was refused and pointed at an MCP tool that cannot read it, and the post-call nudge
// recommended the same tool. A read of the project's own file keeps both.
func TestACdOutOfTheProjectIsNeitherRefusedNorNudged(t *testing.T) {
	root, dbPath := scannedProject(t)
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "app.go"), []byte("package other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := map[string]bool{toolspec.ReadToolName: true}
	bash := func(c string) map[string]interface{} { return map[string]interface{}{"command": c} }

	away := "cd " + other + " && cat app.go"
	if d := decideGuard("Bash", bash(away), blocked, true, dbPath, toolspec.SurfaceMCP); d.Deny {
		t.Errorf("%q reads %s/app.go, outside the project, and was refused: %s", away, other, d.Message)
	}
	if worthNudgingBash(bash(away), dbPath) {
		t.Errorf("%q was nudged toward a tool that cannot read it", away)
	}

	home := "cd " + root + " && cat app.go"
	if d := decideGuard("Bash", bash(home), blocked, true, dbPath, toolspec.SurfaceMCP); !d.Deny {
		t.Errorf("%q reads the project's own indexed file and must stay refused", home)
	}
	if !worthNudgingBash(bash(home), dbPath) {
		t.Errorf("%q reads an indexed file and keeps its nudge", home)
	}
}

// TestABackslashIsAPathSeparatorWhereItIsOne pins the fork in splitCommandSegments, from both
// sides, on any platform.
//
// A Windows agent writes `head -5 C:\Users\me\repo\a.go`, and read as a POSIX shell would read
// it that is the single word `C:Usersmerepoa.go` -- a file that does not exist, so the guard
// could not tell the read was of an indexed file, could not place a `cd`, and could not proxy a
// window. Interception stopped there for every natively-spelled path. The variable is what lets
// this be tested where the bug is not.
func TestABackslashIsAPathSeparatorWhereItIsOne(t *testing.T) {
	const command = `head -5 C:\Users\me\repo\CHANGELOG.md`
	orig := backslashEscapes
	t.Cleanup(func() { backslashEscapes = orig })

	backslashEscapes = false // the Windows reading
	segs := splitCommandSegments(command)
	if len(segs) != 1 {
		t.Fatalf("one command, got %d segments: %+v", len(segs), segs)
	}
	argv := segmentArgv(segs[0])
	if len(argv) != 3 || argv[2] != `C:\Users\me\repo\CHANGELOG.md` {
		t.Errorf("argv = %q, want the path whole", argv)
	}

	backslashEscapes = true // the POSIX reading, which is what Unix must keep
	argv = segmentArgv(splitCommandSegments(command)[0])
	if len(argv) != 3 || argv[2] != "C:UsersmerepoCHANGELOG.md" {
		t.Errorf("argv = %q, want the escapes consumed as a shell consumes them", argv)
	}
	// And the case the escape handling exists for keeps working under that reading.
	if got := len(splitCommandSegments(`echo \; grep x f`)); got != 1 {
		t.Errorf("an escaped `;` splits nothing: got %d segments", got)
	}
}
