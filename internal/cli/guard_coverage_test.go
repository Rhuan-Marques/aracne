package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The compact-blocked-20260830c benchmark run left behind a precise record of what the guard
// did and did not catch: replaying all 215 Bash calls from its transcripts reproduced the
// transcript verdict 214 times, so the tokenizer was never the problem. What walked through
// walked through because it was never classified -- and the write side was far leakier than
// the read side, which is why eight of those runs edited source the topology never saw.
//
// Each case below is a real command shape from that run (or the minimal form of one). The
// "must stay open" half matters as much as the other: a guard that refuses `git grep` or a
// build step's `cp` buys a denial and delivers nothing.
func TestClassifierCoversTheShapesThatLeaked(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want string // "" means deliberately unclassified
	}{
		// --- readers that used to walk through ---
		{"git show of a file", `git show HEAD:doc/config.go`, "read"},
		{"git cat-file", `git cat-file -p HEAD:src/app.rs`, "read"},
		{"python -c reading source", `python3 -c "print(''.join(open('src/component.js').readlines()[600:760]))"`, "read"},
		{"node -e reading source", `node -e "console.log(require('fs').readFileSync('src/index.ts','utf8'))"`, "read"},
		{"unknown wrapper around sed", `rtk proxy sed -n '1280,1400p' src/parse/parser.rs`, "read"},
		{"more as a cat synonym", `more src/main.go`, "read"},
		{"od as a cat synonym", `od -c src/main.go`, "read"},

		// --- writers that used to walk through ---
		{"python heredoc rewriting source", `python3 - <<EOF s=open('src/flask/config.py').read() open('src/flask/config.py','w').write(s) EOF`, "edit"},
		{"cp over source", `cp /tmp/x src/bytes.rs`, "edit"},
		{"mv over source", `mv /tmp/x src/bytes.rs`, "edit"},
		{"tee into source", `tee src/bytes.rs`, "edit"},
		{"git apply", `git apply /tmp/pr.diff`, "edit"},
		{"git checkout of a path", `git checkout HEAD -- src/bytes.rs`, "edit"},
		{"git restore", `git restore src/bytes.rs`, "edit"},

		// --- shapes the guard already got right, kept as regression guards ---
		{"plain cat", `cat src/main.go`, "read"},
		{"sed range read", `sed -n 1600,1660p src/bytes_mut.rs`, "read"},
		{"sed in place", `sed -i s/a/b/ src/main.go`, "edit"},
		{"cd prefix does not defeat the matcher", `cd src && sed -n 1,20p bytes.rs`, "read"},

		// --- must stay open: refusing these costs a denial and delivers nothing ---
		{"git grep", `git grep needle`, ""},
		{"git log piped", `git log --oneline | tail -5`, ""},
		{"git show of a commit", `git show HEAD --stat`, ""},
		{"git checkout of a branch", `git checkout main`, ""},
		{"python not touching source", `python3 -c "print(1)"`, ""},
		{"python running a test suite", `python3 -m pytest tests/ -q`, ""},
		{"cp between build artifacts", `cp dist/a.bundle dist/b.bundle`, ""},
		{"go build", `go build ./...`, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keys := commandKeys(tc.cmd, true)
			got := ""
			for _, k := range keys {
				// "bash" is appended to every Bash command by implicatedKeys, not by
				// commandKeys; anything else here is a real classification.
				if k != "bash" {
					got = k
					break
				}
			}
			if got != tc.want {
				t.Errorf("commandKeys(%q) = %q, want %q (all keys: %v)", tc.cmd, got, tc.want, keys)
			}
		})
	}
}

// The pipe exemption exists so `git log | tail` is not refused: it filters command output,
// which no MCP tool can serve. It must not become a way to read a file -- the read has to be
// classified wherever in the pipeline it happens.
func TestPipeExemptionDoesNotLaunderAFileRead(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{`git log --oneline | sed -n 1,5p`, ""},             // reads command output
		{`sed -n 1,20p src/main.go | cat`, "read"},          // reads a file, then pipes
		{`git show HEAD:src/main.go | sed -n 1,5p`, "read"}, // the producer is the file read
	}
	for _, tc := range cases {
		keys := commandKeys(tc.cmd, true)
		got := ""
		for _, k := range keys {
			if k != "bash" {
				got = k
				break
			}
		}
		if got != tc.want {
			t.Errorf("commandKeys(%q) = %q, want %q (all keys: %v)", tc.cmd, got, tc.want, keys)
		}
	}
}

// The classifier can only refuse the spellings it knows. Eight of twenty-seven runs in the
// compact-blocked-20260830c benchmark edited source through paths nobody had enumerated, so
// the drift check has to fire on anything the classifier could not account for -- that being
// exactly the case a backstop exists for.
func TestDriftCheckFiresOnWritesAndOnTheUnrecognized(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"classified write", `cp /tmp/x src/main.go`, true},
		{"unrecognized command", `some-unknown-tool --do-things`, true},
		{"unrecognized with a source arg", `weirdgen --out src/main.go`, true},
		{"a plain read is governed already", `cat src/main.go`, false},
		{"a grep is governed already", `grep -n needle src/main.go`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keys := implicatedKeys("Bash", map[string]interface{}{"command": tc.cmd}, true)
			if got := mayHaveWrittenSource(keys); got != tc.want {
				t.Errorf("mayHaveWrittenSource(%q) = %v, want %v (keys: %v)", tc.cmd, got, tc.want, keys)
			}
		})
	}
}

// A denial that answers the question has to be certain it understood the question. Anything
// with more than one plausible target falls back to the ordinary one-line pointer, which costs
// the model exactly what it costs today.
func TestProxyOnlyClaimsUnambiguousSingleFileReads(t *testing.T) {
	// A real tree, because the operand is now resolved against the PROJECT ROOT rather than
	// taken as typed -- the hook's own working directory is not the agent's, so an unresolved
	// relative path is not a target. See soleReadTarget.
	root := t.TempDir()
	for _, rel := range []string{"src/main.go", "pkg/app/server.go", "a.go", "b.go"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	abs := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"plain cat", `cat src/main.go`, abs("src/main.go")},
		{"sed window", `sed -n 1,20p src/main.go`, abs("src/main.go")},
		{"head with flags", `head -40 pkg/app/server.go`, abs("pkg/app/server.go")},
		{"quoted path", `cat "src/main.go"`, abs("src/main.go")},

		{"two files is ambiguous", `cat a.go b.go`, ""},
		{"a glob is ambiguous", `cat src/*.go`, ""},
		{"a pipeline is not a plain read", `cat src/main.go | head -5`, ""},
		{"a compound command is not a plain read", `cd src && cat main.go`, ""},
		{"no operand at all", `cat`, ""},
		{"a write is never proxied", `sed -i s/a/b/ src/main.go`, ""},
		{"a grep is not a read", `grep -n needle src/main.go`, ""},
		// The reason the resolution moved: the hook runs in the session directory while the
		// agent's shell may be anywhere, so a name that is not a file under the root is not a
		// file this process may answer about.
		{"a path that is not in the project", `cat nowhere/absent.go`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := soleReadTarget(tc.cmd, root).path; got != tc.want {
				t.Errorf("soleReadTarget(%q) = %q, want %q", tc.cmd, got, tc.want)
			}
		})
	}
}

// THE PROXY ANSWERS ABOUT THE FILE THE AGENT MEANT, OR ABOUT NOTHING.
//
// A hook runs in the session directory; the agent's Bash shell has a working directory of its
// own that persists across calls and that the hook cannot see. Resolving the operand as typed
// meant `cat util.go` from a subdirectory was answered with a DIFFERENT util.go at the
// repository root -- real content, no path named in the message, nothing for the model to
// notice. Resolution against the project root cannot invent that: either the name is a file
// under the root, or there is no proxy.
func TestProxyDoesNotAnswerWithASameNamedFileElsewhere(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "util.go"), []byte("package root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "pkg", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "util.go"), []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The agent typed `cat util.go` with its shell in pkg/sub. Root-relative, that names the
	// root file; the point is that it can never name anything else.
	if got := soleReadTarget("cat util.go", root).path; got != filepath.Join(root, "util.go") {
		t.Fatalf("soleReadTarget resolved to %q, want the root-relative file", got)
	}
	// And a root that cannot be determined at all proxies nothing rather than falling back to
	// the process working directory.
	if got := soleReadTarget("cat util.go", "").path; got != "" {
		t.Errorf("soleReadTarget with no project root = %q, want no target", got)
	}
}
