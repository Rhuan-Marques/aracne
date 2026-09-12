package topogrep

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// THE ONLY TREES A SEARCH PRUNES ARE THE ONES RIPGREP PRUNES.
//
// aracne used to carry its own list -- node_modules, vendor, .venv, the build and cache
// directories -- plus the project's scan.ignore, and it printed a line naming what it had
// skipped. Both halves were wrong. The list pruned `vendor/`, which in Go is compiled
// first-party source somebody greps on purpose, and it was a hardcoded map with no way to
// switch off; scan.ignore is the SCANNER's rule, about what earns a place in the topology,
// and borrowing it for grep meant a directory deliberately left out of the graph could not
// be searched even though its files were right there on disk. The note then spent a line of
// every result explaining a decision the caller had not asked for.
//
// What replaces them is what ripgrep does and what every developer already expects from a
// search in a repository: the .gitignore hierarchy decides. It needs no configuration key
// because the project already wrote one, it prunes node_modules exactly when the project
// ignores node_modules, and a tree the project tracks is searched however large it is.
//
// ONE RULE OF RIPGREP'S IS DELIBERATELY NOT ADOPTED: hidden files. `rg` skips every
// dot-prefixed name, which costs `.github` -- and a `grep -rn runs-on .` that comes back
// empty reads as "this project has no CI". aracne prunes a strict subset: the version
// control metadata and its own store (see prunedDirs), all of which ripgrep skips too.

// gitRule is one compiled .gitignore line.
type gitRule struct {
	re *regexp.Regexp
	// dirOnly is set by a trailing "/": the pattern then matches a directory, and
	// everything under it, but never a file by its own name.
	dirOnly bool
	// anchored is set by a leading or internal "/": the pattern is resolved from the
	// directory holding the ignore file instead of floating to any depth.
	anchored bool
	// negate is set by a leading "!": a later match re-includes what an earlier one
	// excluded. This is the rule domain.IgnoreMatcher does not implement, and the reason
	// this file compiles its own instead of reusing it -- scan.ignore has no negation
	// because a config key is a list the author controls, while a .gitignore is a
	// hierarchy in which re-inclusion is the only way to carve an exception.
	negate bool
}

// gitIgnoreFile is one ignore file: its rules, and the directory they resolve against.
type gitIgnoreFile struct {
	base  string
	rules []gitRule
}

// ignoreFileNames are the per-directory ignore files, in ASCENDING precedence: a rule in
// .rgignore beats one in .ignore, which beats one in .gitignore, which is ripgrep's order.
//
// .gitignore is conditional on there being a git repository to ignore FOR. That is ripgrep's
// rule (`--no-require-git` turns it off) and it is not a quibble: a .gitignore in a directory
// git does not track is a file somebody copied in, and honouring it would prune a tree on the
// strength of a rule nothing is enforcing. .ignore and .rgignore are unconditional -- they
// exist to tell a SEARCH what to skip, which is a thing to say with or without a repository.
var ignoreFileNames = []string{".gitignore", ".ignore", ".rgignore"}

// gitIgnores answers "would git ignore this path" for one search, loading each directory's
// ignore files the first time a path under it is tested.
//
// It resolves by walking UP from the path rather than by keeping a stack down the walk:
// filepath.WalkDir reports no directory-exit event, so a stack pushed on entry has nothing to
// pop it on the way back out and a sibling branch inherits the last branch's rules.
type gitIgnores struct {
	// root bounds the search upward: no ignore file above it is read. It is the enclosing
	// repository when there is one, so that a repo-root .gitignore governs a search started
	// three directories down, exactly as git does.
	root  string
	files map[string]*gitIgnoreFile // by directory, nil value = no ignore file there
	// repo holds .git/info/exclude and core.excludesFile, which git consults after every
	// per-directory file and which therefore lose to all of them.
	repo []*gitIgnoreFile
	// inRepo records that root is a real git repository, gating the .gitignore family.
	inRepo bool
	// named is the root the caller spelled out, when they spelled one out.
	//
	// A PATH NAMED ON THE COMMAND LINE IS SEARCHED EVEN WHEN IT IS IGNORED. `rg needle
	// build/` prints build/out.go though `build/` is in .gitignore, because asking for a
	// directory by name and being told nothing is in it answers a question nobody asked.
	// What the exemption covers is exactly the named directory: a rule that reaches a file
	// only THROUGH it stops applying, while a rule matching the file itself, or a directory
	// inside it, applies as it always did -- which is why the same `rg` still skips
	// keep/x.tmp under `*.tmp`, and skips build/inner when build/.gitignore names it.
	named string
}

// newGitIgnores prepares the matcher for a search rooted at dir.
func newGitIgnores(dir string) *gitIgnores {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	root, inRepo := repoRoot(abs)
	g := &gitIgnores{root: root, inRepo: inRepo, files: map[string]*gitIgnoreFile{}}
	if inRepo {
		g.loadRepoWide()
	}
	return g
}

// exempt marks dir as named on the command line; see gitIgnores.named.
func (g *gitIgnores) exempt(dir string) {
	if abs, err := filepath.Abs(dir); err == nil {
		g.named = filepath.Clean(abs)
	}
}

// shields reports whether ancestorDir is the named root or lies above it, in which case a
// rule matching it must not be applied to anything the caller asked for by name.
func (g *gitIgnores) shields(ancestorDir string) bool {
	if g.named == "" {
		return false
	}
	return g.named == ancestorDir || strings.HasPrefix(g.named, ancestorDir+string(filepath.Separator))
}

// repoRoot walks up from dir to the enclosing repository. Without one it reports dir itself
// and false: the upward search stops there, and only .ignore/.rgignore are read.
//
// A .git ENTRY IS A FILE AS OFTEN AS IT IS A DIRECTORY -- that is what a worktree and a
// submodule leave behind -- so testing for a directory would put every worktree checkout on
// the no-repository path and silently stop honouring its .gitignore.
func repoRoot(dir string) (string, bool) {
	for cur := dir; ; {
		if st, err := os.Stat(filepath.Join(cur, ".git")); err == nil && (st.IsDir() || st.Mode().IsRegular()) {
			return cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return dir, false
		}
		cur = parent
	}
}

// loadRepoWide reads the two ignore sources that belong to the repository rather than to a
// directory in it: .git/info/exclude, and the user's core.excludesFile.
func (g *gitIgnores) loadRepoWide() {
	if f := parseIgnoreFile(filepath.Join(g.root, ".git", "info", "exclude"), g.root); f != nil {
		g.repo = append(g.repo, f)
	}
	if path := globalExcludesPath(); path != "" {
		if f := parseIgnoreFile(path, g.root); f != nil {
			g.repo = append(g.repo, f)
		}
	}
}

// globalExcludesOnce caches the user's core.excludesFile for the process.
//
// FINDING IT COSTS A SUBPROCESS, AND A SEARCH IS NOT A RARE EVENT. Every intercepted grep
// builds a matcher per root, and shelling out to `git config` on each one puts a fork in the
// path of a command whose whole point is to be cheaper than reading the files. The setting is
// a user-level preference; it does not change under a running process.
var globalExcludesOnce struct {
	sync.Once
	path string
}

// globalExcludesPath returns the user's core.excludesFile, or "" when there is none.
//
// git is ASKED rather than ~/.config/git/ignore assumed: the path is configurable, and a
// wrong guess silently searches a tree the user excluded globally. A missing git, or no
// setting, is not an error -- it is simply the documented default location, which is what git
// itself falls back to.
func globalExcludesPath() string {
	globalExcludesOnce.Do(func() {
		out, err := exec.Command("git", "config", "--get", "core.excludesFile").Output()
		path := strings.TrimSpace(string(out))
		if err != nil || path == "" {
			if home, herr := os.UserHomeDir(); herr == nil {
				globalExcludesOnce.path = filepath.Join(home, ".config", "git", "ignore")
			}
			return
		}
		if strings.HasPrefix(path, "~/") {
			home, herr := os.UserHomeDir()
			if herr != nil {
				return
			}
			path = filepath.Join(home, path[2:])
		}
		globalExcludesOnce.path = path
	})
	return globalExcludesOnce.path
}

// fileFor returns the merged ignore rules written in dir, reading them once.
func (g *gitIgnores) fileFor(dir string) *gitIgnoreFile {
	if f, loaded := g.files[dir]; loaded {
		return f
	}
	var merged *gitIgnoreFile
	for _, name := range ignoreFileNames {
		if name == ".gitignore" && !g.inRepo {
			continue
		}
		f := parseIgnoreFile(filepath.Join(dir, name), dir)
		if f == nil {
			continue
		}
		if merged == nil {
			merged = &gitIgnoreFile{base: dir}
		}
		merged.rules = append(merged.rules, f.rules...)
	}
	g.files[dir] = merged
	return merged
}

// Match reports whether p is ignored. isDir selects the rules a trailing "/" restricts.
//
// PRECEDENCE IS GIT'S: the deepest ignore file that has anything to say decides, and within
// one file the LAST matching line wins -- which is what makes `!keep.go` under `*.go` mean
// what it reads as. Everything is therefore scanned from the bottom up, and the first match
// found is the last one git would have applied.
func (g *gitIgnores) Match(p string, isDir bool) bool {
	if g == nil {
		return false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	for dir := filepath.Dir(abs); ; {
		if f := g.fileFor(dir); f != nil {
			if ignored, ok := f.match(abs, isDir, g); ok {
				return ignored
			}
		}
		if dir == g.root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	for i := len(g.repo) - 1; i >= 0; i-- {
		if ignored, ok := g.repo[i].match(abs, isDir, g); ok {
			return ignored
		}
	}
	return false
}

// match returns this file's verdict on abs, and whether it had one at all. A file that
// matches nothing must say so rather than return "not ignored", or it would settle a path a
// shallower file -- or the repo-wide excludes -- still has a rule for.
func (f *gitIgnoreFile) match(abs string, isDir bool, g *gitIgnores) (ignored, decided bool) {
	rel, err := filepath.Rel(f.base, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false, false
	}
	segs := strings.Split(filepath.ToSlash(rel), "/")
	// Ancestors at or above a named root are not the caller's problem; see gitIgnores.named.
	ancestors := len(segs) - 1
	for ancestors > 0 && g.shields(filepath.Join(f.base, filepath.FromSlash(strings.Join(segs[:ancestors], "/")))) {
		ancestors--
	}
	for i := len(f.rules) - 1; i >= 0; i-- {
		if f.rules[i].matches(segs, isDir, ancestors) {
			return !f.rules[i].negate, true
		}
	}
	return false, false
}

// matches reports whether the rule covers the path given as segments, either by matching the
// entry itself or by matching one of the directories above it -- an ignored directory takes
// its whole subtree with it.
func (r gitRule) matches(segs []string, isDir bool, ancestors int) bool {
	last := len(segs) - 1
	// A dir-only rule may match the entry's own name only when the entry is a directory;
	// on an ancestor it always may, since an ancestor is a directory by construction.
	if r.dirOnly && !isDir {
		return r.matchesAncestor(segs, ancestors)
	}
	if r.anchored {
		if r.re.MatchString(strings.Join(segs, "/")) {
			return true
		}
	} else if r.re.MatchString(segs[last]) {
		return true
	}
	return r.matchesAncestor(segs, ancestors)
}

// matchesAncestor reports whether the rule matches one of the entry's first `ancestors`
// parent directories -- the ones a named root does not shield.
func (r gitRule) matchesAncestor(segs []string, ancestors int) bool {
	for i := 0; i < ancestors; i++ {
		if r.anchored {
			if r.re.MatchString(strings.Join(segs[:i+1], "/")) {
				return true
			}
		} else if r.re.MatchString(segs[i]) {
			return true
		}
	}
	return false
}

// parseIgnoreFile reads one ignore file into rules resolved against base, returning nil when
// the file is absent or holds nothing but blanks and comments.
func parseIgnoreFile(path, base string) *gitIgnoreFile {
	fh, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer fh.Close()

	out := &gitIgnoreFile{base: base}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if r, ok := compileGitRule(sc.Text()); ok {
			out.rules = append(out.rules, r)
		}
	}
	if len(out.rules) == 0 {
		return nil
	}
	return out
}

// compileGitRule turns one .gitignore line into a rule, reporting false for a line that
// carries none: a blank, or a comment.
func compileGitRule(line string) (gitRule, bool) {
	p := strings.TrimLeft(line, " \t")
	if p == "" || strings.HasPrefix(p, "#") {
		return gitRule{}, false
	}
	// Trailing whitespace is not part of the pattern unless a backslash kept it. Counting
	// the backslashes matters: `foo\\ ` ends in an escaped backslash, so the space is
	// still trailing.
	for strings.HasSuffix(p, " ") || strings.HasSuffix(p, "\t") {
		trimmed := strings.TrimRight(p, " \t")
		if bs := len(trimmed) - len(strings.TrimRight(trimmed, `\`)); bs%2 == 1 {
			break
		}
		p = trimmed
	}
	negate := false
	if strings.HasPrefix(p, "!") {
		negate, p = true, p[1:]
	} else if strings.HasPrefix(p, `\!`) || strings.HasPrefix(p, `\#`) {
		p = p[1:]
	}
	if p == "" {
		return gitRule{}, false
	}

	p = filepath.ToSlash(p)
	dirOnly, anchored := false, false
	// `X/**` is "everything under X", which is what a directory rule already means; left as
	// a regex it cannot prune X itself, so a walk descends into an ignored tree only to
	// reject it file by file. The slash before `**` is an INTERNAL slash and anchors the
	// pattern, so it has to be counted before it is stripped.
	if strings.HasSuffix(p, "/**") {
		dirOnly, anchored = true, true
		p = strings.TrimSuffix(p, "/**")
	}
	if strings.HasSuffix(p, "/") {
		dirOnly = true
		p = strings.TrimRight(p, "/")
	}
	if strings.HasPrefix(p, "/") {
		anchored = true
		p = strings.TrimPrefix(p, "/")
	}
	if strings.Contains(p, "/") {
		anchored = true
	}
	if p == "" {
		return gitRule{}, false
	}
	// The glob dialect is shared with scan.ignore -- `**` across segments, `*`, `?` and
	// bracket classes within one, `\` escapes -- so it is compiled by the same function
	// rather than by a second copy that would drift from it.
	re, err := domain.GlobToRegexp(p)
	if err != nil {
		return gitRule{}, false
	}
	return gitRule{re: re, dirOnly: dirOnly, anchored: anchored, negate: negate}, true
}
