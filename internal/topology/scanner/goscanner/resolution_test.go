package goscanner

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// scanBoth scans dir cold, then re-parses every Go file of it through UpdateFile, and hands
// back both topologies: an edge the cold scan records has to survive the single-file path too.
func scanBoth(t *testing.T, dir string) map[string]*domain.Topology {
	t.Helper()
	s := NewGoScanner()
	cold, err := s.Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	updated, err := s.Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	updated.Root = dir
	var files []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".go") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	for _, f := range files {
		if _, err := s.UpdateFile(updated, f); err != nil {
			t.Fatalf("UpdateFile %s: %v", f, err)
		}
	}
	return map[string]*domain.Topology{"cold": cold, "update": updated}
}

func hasEdge(topo *domain.Topology, src, kind, dst string) bool {
	for _, c := range topo.Resources[src].Connections[kind] {
		if c == dst {
			return true
		}
	}
	return false
}

func wantEdge(t *testing.T, mode string, topo *domain.Topology, src, kind, dst string) {
	t.Helper()
	if _, ok := topo.Resources[src]; !ok {
		t.Fatalf("[%s] %s missing; ids: %v", mode, src, resourceIDs(topo))
	}
	if !hasEdge(topo, src, kind, dst) {
		t.Errorf("[%s] want %s -%s-> %s; have %v", mode, src, kind, dst, topo.Resources[src].Connections)
	}
}

func wantNoEdge(t *testing.T, mode string, topo *domain.Topology, src, kind, dst string) {
	t.Helper()
	if hasEdge(topo, src, kind, dst) {
		t.Errorf("[%s] unexpected %s -%s-> %s", mode, src, kind, dst)
	}
}

func wantNoWarningFrom(t *testing.T, mode string, topo *domain.Topology, src string) {
	t.Helper()
	for _, w := range topo.Warnings {
		if w.SourceID == src {
			t.Errorf("[%s] unexpected warning from %s: %s -> %s", mode, src, w.Kind, w.TargetID)
		}
	}
}

func wantWarning(t *testing.T, mode string, topo *domain.Topology, src string, kind domain.WarningKind, target string) {
	t.Helper()
	for _, w := range topo.Warnings {
		if w.SourceID == src && w.Kind == kind && w.TargetID == target {
			return
		}
	}
	t.Errorf("[%s] want warning %s from %s to %s; have %v", mode, kind, src, target, topo.Warnings)
}

// GO-1: the module directive is the path alone -- not its comment, not its quotes.
func TestParseModuleDirective(t *testing.T) {
	cases := []struct{ gomod, want string }{
		{"module example.com/c\n\ngo 1.22\n", "example.com/c"},
		{"module example.com/c // my module\n", "example.com/c"},
		{"// Deprecated: use example.com/d\nmodule example.com/c // Deprecated: use example.com/d\n", "example.com/c"},
		{"module \"example.com/q\"\n", "example.com/q"},
		{"module `example.com/q` // raw\n", "example.com/q"},
		{"module\texample.com/tab\n", "example.com/tab"},
		{"module (\n\t// the path\n\texample.com/block\n)\n", "example.com/block"},
		{"  module   example.com/spaced  \r\n", "example.com/spaced"},
		{"// module example.com/commented-out\n", ""},
		{"modules example.com/no\n", ""},
		{"go 1.22\n", ""},
	}
	for _, c := range cases {
		if got := parseModuleDirective([]byte(c.gomod)); got != c.want {
			t.Errorf("parseModuleDirective(%q) = %q, want %q", c.gomod, got, c.want)
		}
	}
}

// GO-1: a commented or quoted go.mod keeps normal IDs, and its internal imports stay internal.
func TestScanModuleLineWithCommentOrQuotes(t *testing.T) {
	for _, gomod := range []string{
		"module example.com/c // my module\n\ngo 1.22\n",
		"module \"example.com/c\"\n\ngo 1.22\n",
	} {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{
			"go.mod":  gomod,
			"p/p.go":  "package p\n\nfunc Long() int { return 1 }\n",
			"main.go": "package main\n\nimport \"example.com/c/p\"\n\nfunc main() { p.Long() }\n",
		})
		for mode, topo := range scanBoth(t, dir) {
			wantEdge(t, mode, topo, "example.com/c.main", "calls", "example.com/c/p.Long")
			wantEdge(t, mode, topo, filepath.Join(dir, "main.go"), "imports_package", "example.com/c/p")
			if _, ok := topo.Resources["example.com/c/p"]; !ok {
				t.Errorf("[%s] %q: package example.com/c/p missing; ids: %v", mode, gomod, resourceIDs(topo))
			}
		}
	}
}

// GO-2: an import sharing the module path as a string prefix -- not a path prefix -- is a
// dependency; one under the module path is still internal.
func TestInternalImportNeedsPathBoundary(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":     "module example.com/app\n\ngo 1.22\n",
		"sub/sub.go": "package sub\n\nfunc S() {}\n",
		"u.go": `package app

import (
	"example.com/app/sub"
	"example.com/apputil/thing"
)

type Holder struct {
	T thing.T
}

func U() { sub.S(); thing.Do() }
`,
	})
	for mode, topo := range scanBoth(t, dir) {
		file := filepath.Join(dir, "u.go")
		wantEdge(t, mode, topo, file, "imports_dependency", "example.com/apputil/thing")
		wantNoEdge(t, mode, topo, file, "imports_package", "example.com/apputil/thing")
		wantEdge(t, mode, topo, file, "imports_package", "example.com/app/sub")
		wantEdge(t, mode, topo, "example.com/app.Holder", "uses_dependency", "example.com/apputil/thing")
		wantNoEdge(t, mode, topo, "example.com/app.Holder", "uses_package", "example.com/apputil/thing")
		if _, ok := topo.Resources["example.com/apputil/thing"]; !ok {
			t.Errorf("[%s] no dependency node for example.com/apputil/thing", mode)
		}
		wantEdge(t, mode, topo, "example.com/app.U", "calls", "example.com/app/sub.S")
	}
}

// GO-3: an explicit instantiation with ONE type argument is a call of the generic function,
// and a generic constructor's result carries the generic type's methods.
func TestGenericCallWithOneTypeArgument(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod": "module example.com/r\n\ngo 1.22\n",
		"g/g.go": `package g

func Map[T any](xs []T, f func(T) T) []T { return xs }
func Map2[T, U any](xs []T, f func(T) U) []U { return nil }
func Id[T any](x T) T { return x }

type Box[T any] struct{ v T }

func (b *Box[T]) Get() T { return b.v }
func Make[T any]() *Box[T] { return &Box[T]{} }

var Handlers = []func(int) int{}

func Use(xs []int) {
	Map[int](xs, nil)
	Map2[int, string](xs, nil)
	Handlers[0](1)
	local := []func(){}
	local[0]()
}
`,
		"gu/gu.go": `package gu

import "example.com/r/g"

func Call() {
	g.Id[int](1)
	b := g.Make[int]()
	b.Get()
	g.Handlers[0](2)
}
`,
	})
	for mode, topo := range scanBoth(t, dir) {
		wantEdge(t, mode, topo, "example.com/r/g.Use", "calls", "example.com/r/g.Map")
		wantEdge(t, mode, topo, "example.com/r/g.Use", "calls", "example.com/r/g.Map2")
		wantEdge(t, mode, topo, "example.com/r/gu.Call", "calls", "example.com/r/g.Id")
		wantEdge(t, mode, topo, "example.com/r/gu.Call", "calls", "example.com/r/g.Make")
		wantEdge(t, mode, topo, "example.com/r/gu.Call", "calls", "example.com/r/g.(Box).Get")
		// An element of a slice of functions is called with the same syntax; it is not a
		// generic call, and it is not a missing function either.
		wantEdge(t, mode, topo, "example.com/r/g.Use", "uses_extvar", "example.com/r/g.Handlers")
		wantNoWarningFrom(t, mode, topo, "example.com/r/g.Use")
		wantNoWarningFrom(t, mode, topo, "example.com/r/gu.Call")
	}
}

func TestStripTypeArgs(t *testing.T) {
	for in, want := range map[string]string{
		"Box[T]":              "Box",
		"g.Pair[K, V]":        "g.Pair",
		"Box[map[string]int]": "Box",
		"[]Box":               "[]Box",
		"map[string]Box[T]":   "map[string]Box[T]",
		"[4]int":              "[4]int",
		"Plain":               "Plain",
		"func(...)":           "func(...)",
	} {
		if got := stripTypeArgs(in); got != want {
			t.Errorf("stripTypeArgs(%q) = %q, want %q", in, got, want)
		}
	}
}

// GO-4: a selector's field name, a struct literal's key, a closure's parameter and a named
// result are not uses of the package-level names they spell. The real uses right next to them
// still are.
func TestIdentifiersResolvedInReferencePositionOnly(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod": "module example.com/r\n\ngo 1.22\n",
		"api/api.go": `package api

import (
	"net/http"
	"time"
)

type Client struct{ Name string }

var Timeout = 5

const Retries = 3

type Opts struct{ Timeout int }

type Registry map[int]string

func NewHTTP() *http.Client {
	return &http.Client{Timeout: time.Second}
}

func Read(o Opts) int { return o.Timeout }

func Named() {
	f := func(Client string) string { return Client }
	_ = f
}

func Result() (Timeout int) {
	Timeout = 3
	return
}

func LocalStruct() int {
	v := struct{ Timeout int }{Timeout: 1}
	return v.Timeout
}

func RealUses() int {
	c := Client{Name: "x"}
	_ = c
	return Timeout
}

func MapKey() { _ = map[int]string{Timeout: "t"} }

func ArrayKey() { _ = [...]string{Retries: "r"} }

func ElidedMapKey() { _ = []map[int]int{{Timeout: 1}} }

func NamedMapKey() { _ = Registry{Retries: "r"} }
`,
		"api/other.go": `package api

func ReadsFromOtherFile() int { return Timeout + Retries }
`,
	})
	for mode, topo := range scanBoth(t, dir) {
		for _, fn := range []string{"NewHTTP", "Read", "Named", "Result", "LocalStruct"} {
			src := "example.com/r/api." + fn
			wantNoEdge(t, mode, topo, src, "uses_extvar", "example.com/r/api.Timeout")
			wantNoEdge(t, mode, topo, src, "uses_struct", "example.com/r/api.Client")
		}
		wantEdge(t, mode, topo, "example.com/r/api.RealUses", "uses_struct", "example.com/r/api.Client")
		wantEdge(t, mode, topo, "example.com/r/api.RealUses", "uses_extvar", "example.com/r/api.Timeout")
		// A map or array literal's key is an expression -- spelled out, elided inside an
		// enclosing literal, or behind a named map type of this package.
		wantEdge(t, mode, topo, "example.com/r/api.MapKey", "uses_extvar", "example.com/r/api.Timeout")
		wantEdge(t, mode, topo, "example.com/r/api.ArrayKey", "uses_extvar", "example.com/r/api.Retries")
		wantEdge(t, mode, topo, "example.com/r/api.ElidedMapKey", "uses_extvar", "example.com/r/api.Timeout")
		wantEdge(t, mode, topo, "example.com/r/api.NamedMapKey", "uses_extvar", "example.com/r/api.Retries")
		wantEdge(t, mode, topo, "example.com/r/api.ReadsFromOtherFile", "uses_extvar", "example.com/r/api.Timeout")
		wantEdge(t, mode, topo, "example.com/r/api.ReadsFromOtherFile", "uses_extvar", "example.com/r/api.Retries")
	}
}

// GO-4: the key-position rule must not eat a real use -- a struct literal keyed with a field
// whose VALUE is the package var still records it, and the key alone does not.
func TestStructLiteralKeyIsNotAUse(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod": "module example.com/r\n\ngo 1.22\n",
		"p/p.go": `package p

var Timeout = 5

type Opts struct{ Timeout int }

func KeyOnly() Opts { return Opts{Timeout: 1} }

func KeyAndValue() Opts { return Opts{Timeout: Timeout} }
`,
	})
	for mode, topo := range scanBoth(t, dir) {
		wantNoEdge(t, mode, topo, "example.com/r/p.KeyOnly", "uses_extvar", "example.com/r/p.Timeout")
		wantEdge(t, mode, topo, "example.com/r/p.KeyAndValue", "uses_extvar", "example.com/r/p.Timeout")
		wantEdge(t, mode, topo, "example.com/r/p.KeyOnly", "uses_struct", "example.com/r/p.Opts")
	}
}

// GO-5: a parameter or local named like an imported package is the variable, not the package.
// Without such a local, the same selector still goes through the import.
func TestLocalShadowsImportedPackage(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":       "module example.com/r\n\ngo 1.22\n",
		"user/user.go": "package user\n\ntype User struct{ N string }\n\nfunc (u *User) Save() error { return nil }\n\nfunc New() *User { return &User{} }\n\nfunc Lookup() int { return 0 }\n",
		"h/h.go": `package h

import "example.com/r/user"

func Handle(user *user.User) error { return user.Save() }

func Local() {
	user := load()
	user.Lookup()
}

func load() interface{ Lookup() } { return nil }

func ViaPackage() error {
	u := user.New()
	return u.Save()
}
`,
	})
	for mode, topo := range scanBoth(t, dir) {
		wantEdge(t, mode, topo, "example.com/r/h.Handle", "calls", "example.com/r/user.(User).Save")
		wantNoWarningFrom(t, mode, topo, "example.com/r/h.Handle")
		wantNoWarningFrom(t, mode, topo, "example.com/r/h.Local")
		wantNoEdge(t, mode, topo, "example.com/r/h.Local", "calls", "example.com/r/user.Lookup")
		wantEdge(t, mode, topo, "example.com/r/h.ViaPackage", "calls", "example.com/r/user.New")
		wantEdge(t, mode, topo, "example.com/r/h.ViaPackage", "calls", "example.com/r/user.(User).Save")
	}
}

// GO-6: an unaliased import binds the package's declared name, which a versioned or hyphenated
// directory does not spell. An explicit alias still wins.
func TestImportBindsDeclaredPackageName(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":          "module example.com/r\n\ngo 1.22\n",
		"hyphen-pkg/h.go": "package hyphen\n\ntype Greeter struct{}\n\nfunc Hello() {}\n",
		"v/v2/v.go":       "package api\n\nfunc V2() {}\n",
		"v/v2/gen.go":     "//go:build ignore\n\npackage main\n\nfunc main() {}\n",
		"aliased/a.go":    "package aliased\n\nfunc A() {}\n",
		"plain/plain.go":  "package plain\n\nfunc P() {}\n",
		"c/c.go": `package c

import (
	"example.com/r/hyphen-pkg"
	"example.com/r/v/v2"
	other "example.com/r/aliased"
	"example.com/r/plain"
)

type Holder struct{ G hyphen.Greeter }

func Calls() {
	hyphen.Hello()
	api.V2()
	other.A()
	plain.P()
}
`,
	})
	for mode, topo := range scanBoth(t, dir) {
		wantEdge(t, mode, topo, "example.com/r/c.Calls", "calls", "example.com/r/hyphen-pkg.Hello")
		wantEdge(t, mode, topo, "example.com/r/c.Calls", "calls", "example.com/r/v/v2.V2")
		wantEdge(t, mode, topo, "example.com/r/c.Calls", "calls", "example.com/r/aliased.A")
		wantEdge(t, mode, topo, "example.com/r/c.Calls", "calls", "example.com/r/plain.P")
		wantEdge(t, mode, topo, "example.com/r/c.Holder", "uses_package", "example.com/r/hyphen-pkg")
		wantNoWarningFrom(t, mode, topo, "example.com/r/c.Calls")
	}
}

// GO-8: converting to an interface of this package is a use of it, not a call to a missing
// function. A name that is really missing still warns.
func TestSamePackageInterfaceConversion(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod": "module example.com/r\n\ngo 1.22\n",
		"api/api.go": `package api

type Shaper interface{ Area() float64 }

type Sq struct{}

func (Sq) Area() float64 { return 1 }

func Convert(s Sq) Shaper { return Shaper(s) }

func Broken() { missing() }
`,
	})
	for mode, topo := range scanBoth(t, dir) {
		wantEdge(t, mode, topo, "example.com/r/api.Convert", "uses_interface", "example.com/r/api.Shaper")
		wantNoWarningFrom(t, mode, topo, "example.com/r/api.Convert")
		wantWarning(t, mode, topo, "example.com/r/api.Broken", domain.WarnUseMissingNode, "example.com/r/api.missing")
	}
}

// GO-9: a local hides a package function only inside its own scope.
func TestLocalShadowsOnlyInItsScope(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod": "module example.com/r\n\ngo 1.22\n",
		"k/k.go": `package k

func process() {}

func Run(hs []func()) {
	process()
	for _, process := range hs {
		process()
	}
}

func InnerBlock(ok bool) {
	if ok {
		helper := func() {}
		helper()
	}
	helper()
}

func Shadowed(process func()) { process() }

func AfterClosure() {
	f := func() {
		cleanup := 1
		_ = cleanup
	}
	f()
	cleanup()
}
`,
		"k/other.go": "package k\n\nfunc helper() {}\n\nfunc cleanup() {}\n",
	})
	for mode, topo := range scanBoth(t, dir) {
		wantEdge(t, mode, topo, "example.com/r/k.Run", "calls", "example.com/r/k.process")
		wantEdge(t, mode, topo, "example.com/r/k.InnerBlock", "calls", "example.com/r/k.helper")
		wantEdge(t, mode, topo, "example.com/r/k.AfterClosure", "calls", "example.com/r/k.cleanup")
		// A parameter shadows for the whole body.
		wantNoEdge(t, mode, topo, "example.com/r/k.Shadowed", "calls", "example.com/r/k.process")
		for _, fn := range []string{"Run", "InnerBlock", "Shadowed", "AfterClosure"} {
			wantNoWarningFrom(t, mode, topo, "example.com/r/k."+fn)
		}
	}
}

// WN-7: a call to a method promoted from an embedded struct -- by value, by pointer, through
// two levels, from another package -- is a call of that method. A field of the same name at a
// shallower depth hides it, as in Go.
func TestPromotedMethodCalls(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod": "module example.com/r\n\ngo 1.22\n",
		"base/base.go": `package base

type Base struct{}

func (b *Base) Hello(n int) {}
`,
		"emb/emb.go": `package emb

import "example.com/r/base"

type Local struct{}

func (Local) Own() {}

type Outer struct{ Local }

type PtrOuter struct{ *Local }

type Cross struct{ base.Base }

type Deep struct{ Cross }

type Hidden struct {
	Cross
	Hello func(int)
}

func Use() {
	o := Outer{}
	o.Own()
	var p PtrOuter
	p.Own()
}

func UseCross(c *Cross) { c.Hello(1) }

func UseDeep(d Deep) { d.Hello(2) }

func UseHidden(h Hidden) { h.Hello(3) }
`,
	})
	for mode, topo := range scanBoth(t, dir) {
		wantEdge(t, mode, topo, "example.com/r/emb.Use", "calls", "example.com/r/emb.(Local).Own")
		wantEdge(t, mode, topo, "example.com/r/emb.Use", "uses_struct", "example.com/r/emb.Outer")
		wantEdge(t, mode, topo, "example.com/r/emb.Use", "uses_struct", "example.com/r/emb.PtrOuter")
		wantEdge(t, mode, topo, "example.com/r/emb.UseCross", "calls", "example.com/r/base.(Base).Hello")
		wantEdge(t, mode, topo, "example.com/r/emb.UseCross", "uses_struct", "example.com/r/emb.Cross")
		wantEdge(t, mode, topo, "example.com/r/emb.UseDeep", "calls", "example.com/r/base.(Base).Hello")
		wantEdge(t, mode, topo, "example.com/r/emb.UseDeep", "uses_struct", "example.com/r/emb.Deep")
		// h.Hello is the func-typed field, not the promoted method.
		wantNoEdge(t, mode, topo, "example.com/r/emb.UseHidden", "calls", "example.com/r/base.(Base).Hello")
		for _, fn := range []string{"Use", "UseCross", "UseDeep", "UseHidden"} {
			wantNoWarningFrom(t, mode, topo, "example.com/r/emb."+fn)
		}
		if _, ok := topo.Resources["example.com/r/emb.(Outer).Own"]; ok {
			t.Errorf("[%s] a promoted method must not become a resource of its own", mode)
		}
	}
}
