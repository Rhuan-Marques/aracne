package jsscanner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// pathAliases is the module-name mapping a tsconfig.json / jsconfig.json declares through
// compilerOptions.baseUrl and compilerOptions.paths (`"@app/*": ["src/*"]`).
//
// Without it an aliased import (`import {Shape} from '@app/models/shape'`) looked like a
// package: the file got an imports_dependency edge to a dependency node that does not exist,
// and nothing it imported resolved.
type pathAliases struct {
	baseURL string // absolute; "" when the config sets none
	// pathsBase is the directory `paths` targets are relative to: baseUrl when set, else the
	// directory of the config that declares `paths`.
	pathsBase string
	paths     []aliasPattern
}

// aliasPattern is one `paths` key with at most one `*`, split around it.
type aliasPattern struct {
	prefix, suffix string
	wildcard       bool
	targets        []string
}

// resolve maps a bare specifier through the aliases to an absolute module path (extension
// and index resolution left to resolveSpecifier), or reports that it is not an alias.
//
// A mapping that lands on a source file of this project is that file. One into a place the
// scan does not index (node_modules, vendor, outside the root) is a package, as is anything
// only an exact-name redirect, a catch-all `"*"` pattern or baseUrl would map -- TypeScript
// falls back to node_modules there. A prefixed pattern (`@app/*`) whose target in the project
// is simply missing stays internal: it is a broken import of the project's own file, and must
// not surface as a package called `@app/models/shape`.
func (a *pathAliases) resolve(root, spec string) (string, bool) {
	if a == nil {
		return "", false
	}
	// TypeScript tries an exact key first, then the wildcard with the longest prefix.
	best, bestLen := -1, -1
	for i, p := range a.paths {
		if !p.wildcard && spec == p.prefix {
			best = i
			break
		}
		if p.wildcard && len(spec) >= len(p.prefix)+len(p.suffix) && strings.HasPrefix(spec, p.prefix) &&
			strings.HasSuffix(spec, p.suffix) && len(p.prefix) > bestLen {
			best, bestLen = i, len(p.prefix)
		}
	}
	if best >= 0 {
		p := a.paths[best]
		star := ""
		if p.wildcard {
			star = spec[len(p.prefix) : len(spec)-len(p.suffix)]
		}
		indexed := true
		for _, t := range p.targets {
			cand := filepath.Join(a.pathsBase, strings.Replace(t, "*", star, 1))
			if projectModuleExists(root, cand) {
				return cand, true
			}
			// A .ts name there would be scanned unless the place itself is excluded.
			if !helper.IsSourceFile(root, cand+".ts", "typescript") {
				indexed = false
			}
		}
		if indexed && p.wildcard && p.prefix != "" && len(p.targets) > 0 {
			return filepath.Join(a.pathsBase, strings.Replace(p.targets[0], "*", star, 1)), true
		}
	}
	if a.baseURL != "" && baseURLHasEntry(a.baseURL, spec) {
		if cand := filepath.Join(a.baseURL, spec); projectModuleExists(root, cand) {
			return cand, true
		}
	}
	return "", false
}

// baseDirs caches the entry names of each baseUrl directory, keyed by its path and valid
// while its mtime holds (adding or removing an entry moves it).
var baseDirs sync.Map // dir -> *dirListing

type dirListing struct {
	mtime time.Time
	names map[string]bool
}

// baseURLHasEntry is a one-stat prefilter for baseUrl resolution: every package import of a
// project with a baseUrl is tried against it, and probing each one for a dozen candidate files
// would cost that many stats per import. Only a specifier whose first segment names an entry
// of the directory (`models` for `models/shape`, `utils.ts` for `utils`) can resolve there.
func baseURLHasEntry(dir, spec string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	var listing *dirListing
	if v, ok := baseDirs.Load(dir); ok && v.(*dirListing).mtime.Equal(info.ModTime()) {
		listing = v.(*dirListing)
	} else {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		listing = &dirListing{mtime: info.ModTime(), names: make(map[string]bool, len(entries))}
		for _, e := range entries {
			name := e.Name()
			listing.names[name] = true
			for i := 1; i < len(name); i++ {
				if name[i] == '.' {
					listing.names[name[:i]] = true // `utils` for utils.ts, `a.b` for a.b.ts
				}
			}
		}
		baseDirs.Store(dir, listing)
	}
	first := spec
	if i := strings.IndexByte(spec, '/'); i >= 0 {
		first = spec[:i]
	}
	return listing.names[first]
}

// projectModuleExists reports whether an extension-less module path names a JavaScript or
// TypeScript source file of the project, the way resolveSpecifier would find it.
func projectModuleExists(root, base string) bool {
	return moduleExists(base, func(p string) bool {
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		return helper.IsSourceFile(root, p, "typescript") || helper.IsSourceFile(root, p, "javascript")
	})
}

// moduleExists probes an extension-less module path the way resolveSpecifier does -- as
// written, with each extension, then as a directory index -- and reports whether any
// candidate satisfies ok.
func moduleExists(base string, ok func(string) bool) bool {
	if ok(base) {
		return true
	}
	for _, e := range moduleExtOrder {
		if ok(base+e) || ok(filepath.Join(base, "index"+e)) {
			return true
		}
	}
	return false
}

// aliasCache memoises parsed configs across the files of a scan. An entry is reused only
// while every file of its `extends` chain is unchanged, because `arac serve` and the watcher
// keep one process alive across edits to the config itself.
var aliasCache sync.Map // config path -> *aliasEntry

type aliasEntry struct {
	stamps  map[string]time.Time
	aliases *pathAliases
}

// loadPathAliases returns the aliases of the nearest tsconfig.json or jsconfig.json at or
// above dir, stopping at the project root, or nil when there is none or it declares none.
func loadPathAliases(root, dir string) *pathAliases {
	for {
		for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
			cfg := filepath.Join(dir, name)
			if _, err := os.Stat(cfg); err == nil {
				return cachedAliases(cfg)
			}
		}
		if dir == root || !strings.HasPrefix(dir, root) {
			return nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func cachedAliases(cfg string) *pathAliases {
	if v, ok := aliasCache.Load(cfg); ok {
		e := v.(*aliasEntry)
		fresh := true
		for p, t := range e.stamps {
			if info, err := os.Stat(p); err != nil || !info.ModTime().Equal(t) {
				fresh = false
				break
			}
		}
		if fresh {
			return e.aliases
		}
	}
	stamps := make(map[string]time.Time)
	aliases := readAliases(cfg, stamps, 0)
	aliasCache.Store(cfg, &aliasEntry{stamps: stamps, aliases: aliases})
	return aliases
}

// tsconfigFile is the part of a tsconfig/jsconfig this reads.
type tsconfigFile struct {
	Extends         json.RawMessage `json:"extends"`
	CompilerOptions struct {
		BaseURL *string             `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	} `json:"compilerOptions"`
}

// readAliases reads cfg and, through a relative `extends`, the configs it inherits from: a
// key the child sets replaces the parent's. Bounded, because a cycle must not hang a scan.
func readAliases(cfg string, stamps map[string]time.Time, depth int) *pathAliases {
	if depth > 8 {
		return nil
	}
	info, err := os.Stat(cfg)
	if err != nil {
		return nil
	}
	stamps[cfg] = info.ModTime()
	data, err := os.ReadFile(cfg)
	if err != nil {
		return nil
	}
	var file tsconfigFile
	if json.Unmarshal(stripJSONC(data), &file) != nil {
		return nil
	}
	dir := filepath.Dir(cfg)

	var out pathAliases
	// `extends` is one config or (TypeScript 5) a list applied in order. A package name
	// (`@tsconfig/node16`) lives in node_modules and is skipped.
	var parents []string
	if json.Unmarshal(file.Extends, &parents) != nil {
		var one string
		if json.Unmarshal(file.Extends, &one) == nil {
			parents = []string{one}
		}
	}
	for _, ext := range parents {
		if !isRelativeSpecifier(ext) {
			continue
		}
		parent := filepath.Join(dir, ext)
		if filepath.Ext(parent) != ".json" {
			parent += ".json"
		}
		if inherited := readAliases(parent, stamps, depth+1); inherited != nil {
			if inherited.baseURL != "" {
				out.baseURL = inherited.baseURL
			}
			if inherited.paths != nil {
				out.paths, out.pathsBase = inherited.paths, inherited.pathsBase
			}
		}
	}
	if b := file.CompilerOptions.BaseURL; b != nil {
		out.baseURL = *b
		if !filepath.IsAbs(out.baseURL) {
			out.baseURL = filepath.Join(dir, out.baseURL)
		}
		if out.pathsBase != "" && file.CompilerOptions.Paths == nil {
			out.pathsBase = out.baseURL // inherited paths now resolve against the new baseUrl
		}
	}
	if file.CompilerOptions.Paths != nil {
		out.pathsBase = dir
		if out.baseURL != "" {
			out.pathsBase = out.baseURL
		}
		keys := make([]string, 0, len(file.CompilerOptions.Paths))
		for key := range file.CompilerOptions.Paths {
			keys = append(keys, key)
		}
		sort.Strings(keys) // a tie between two patterns must not depend on map order
		out.paths = []aliasPattern{}
		for _, key := range keys {
			targets := file.CompilerOptions.Paths[key]
			p := aliasPattern{prefix: key, targets: targets}
			if i := strings.IndexByte(key, '*'); i >= 0 {
				p = aliasPattern{prefix: key[:i], suffix: key[i+1:], wildcard: true, targets: targets}
			}
			out.paths = append(out.paths, p)
		}
	}
	if out.baseURL == "" && len(out.paths) == 0 {
		return nil
	}
	return &out
}

// stripJSONC turns tsconfig's JSON-with-comments into JSON: `//` and `/* */` comments
// outside strings are blanked and trailing commas before `}` / `]` dropped.
func stripJSONC(data []byte) []byte {
	// walk calls emit for every byte outside a string and copies strings verbatim.
	walk := func(in []byte, emit func(in []byte, i int, out []byte) ([]byte, int)) []byte {
		out := make([]byte, 0, len(in))
		for i := 0; i < len(in); i++ {
			if in[i] != '"' {
				out, i = emit(in, i, out)
				continue
			}
			out = append(out, '"')
			for i++; i < len(in); i++ {
				out = append(out, in[i])
				if in[i] == '\\' && i+1 < len(in) {
					i++
					out = append(out, in[i])
				} else if in[i] == '"' {
					break
				}
			}
		}
		return out
	}
	noComments := walk(data, func(in []byte, i int, out []byte) ([]byte, int) {
		switch {
		case in[i] == '/' && i+1 < len(in) && in[i+1] == '/':
			for i < len(in) && in[i] != '\n' {
				i++
			}
			return append(out, '\n'), i
		case in[i] == '/' && i+1 < len(in) && in[i+1] == '*':
			for i += 2; i+1 < len(in) && !(in[i] == '*' && in[i+1] == '/'); i++ {
			}
			return append(out, ' '), i + 1
		}
		return append(out, in[i]), i
	})
	return walk(noComments, func(in []byte, i int, out []byte) ([]byte, int) {
		if in[i] == ',' {
			j := i + 1
			for j < len(in) && strings.IndexByte(" \t\r\n", in[j]) >= 0 {
				j++
			}
			if j < len(in) && (in[j] == '}' || in[j] == ']') {
				return out, i // a trailing comma
			}
		}
		return append(out, in[i]), i
	})
}
