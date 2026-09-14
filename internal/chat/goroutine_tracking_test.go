package chat

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryBackgroundGoroutineIsTracked is the guard that outlives the bug.
//
// Manager.Close can only promise that the store is quiet if it KNOWS about the work: a
// `go func()` written straight into this package is a run nothing waits for, and the way that
// showed up was a test whose temp directory was removed while a workflow was still writing into
// it -- one failure in twenty, on whichever machine was busiest. The behavioural tests next to
// this one pin what Close does with the runs it knows about; nothing they can observe would
// notice a NEW run that never told it.
//
// So this reads the package instead. Every `go` statement outside goBackground is a run the
// manager cannot wait for, and it fails here rather than as somebody's flake months later.
//
// An intentionally detached goroutine is still possible: say so on the line, with
// `chat:untracked` and a reason. That keeps it a decision with a name on it rather than a
// habit.
func TestEveryBackgroundGoroutineIsTracked(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(src), "\n")
		checked++

		var enclosing string
		ast.Inspect(file, func(n ast.Node) bool {
			if fn, ok := n.(*ast.FuncDecl); ok {
				enclosing = fn.Name.Name
			}
			stmt, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			if enclosing == "goBackground" {
				return true // the helper's own goroutine: this IS the tracking
			}
			pos := fset.Position(stmt.Pos())
			// The marker may sit on the `go` line itself or on the line above it, which is
			// where a reason long enough to be worth reading ends up.
			for _, i := range []int{pos.Line - 1, pos.Line - 2} {
				if i >= 0 && i < len(lines) && strings.Contains(lines[i], "chat:untracked") {
					return true
				}
			}
			t.Errorf("%s:%d: `go` statement in %s() is a background run Close cannot wait for.\n"+
				"\tUse m.goBackground(func() { ... }), or mark the line `chat:untracked` with a reason.",
				filepath.Base(pos.Filename), pos.Line, enclosing)
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no source files were read, so this proves nothing")
	}
}
