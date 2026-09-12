package topology_test

import (
	"sort"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// NEW-1: a signature_changed warning must name a caller that still has something to verify.
//
// The warning is raised against whoever called the callee before the update (goscanner's
// reverse-caller index) or references it after (the fan-out every other scanner goes through),
// and the update that changed the callee can also have changed the callers. Moving the call
// into a helper written in the same edit named the caller that no longer made it; the helper,
// written against the new signature, was named instead on the other path. A cold scan warns
// about neither. What must survive is the point of the warning: a caller nobody touched, of a
// callee whose signature moved -- even when only its return type did.

type staleCallerCase struct {
	name    string
	files   map[string]string
	edits   map[string]string // applied in one update
	partial bool              // the update must take the single-file fast path
	warned  []string          // callers that must be warned, exactly
}

func staleCallerCases() []staleCallerCase {
	const goMod = "module example.com/wm\n\ngo 1.21\n"
	return []staleCallerCase{
		{
			name: "go: the call moves into a new helper in the same file",
			files: map[string]string{
				"go.mod":   goMod,
				"pkg/x.go": "package pkg\n\nfunc helper(n int) int { return n }\n\nfunc A() int { return helper(1) }\n",
			},
			edits: map[string]string{
				"pkg/x.go": "package pkg\n\nfunc helper(n int) (int, error) { return n, nil }\n\n" +
					"func B() int { v, _ := helper(1); return v }\n\nfunc A() int { return B() }\n",
			},
			partial: true,
		},
		{
			name: "go: the call moves into a new helper in another file",
			files: map[string]string{
				"go.mod":   goMod,
				"pkg/h.go": "package pkg\n\nfunc Helper(n int) int { return n }\n",
				"pkg/a.go": "package pkg\n\nfunc A() int { return Helper(1) }\n",
			},
			edits: map[string]string{
				"pkg/h.go": "package pkg\n\nfunc Helper(n int) (int, error) { return n, nil }\n",
				"pkg/a.go": "package pkg\n\nfunc B() int { v, _ := Helper(1); return v }\n\nfunc A() int { return B() }\n",
			},
		},
		{
			name: "go: the caller is fixed in the same edit as the callee",
			files: map[string]string{
				"go.mod":   goMod,
				"pkg/x.go": "package pkg\n\nfunc helper(n int) int { return n }\n\nfunc A() int { return helper(1) }\n",
			},
			edits: map[string]string{
				"pkg/x.go": "package pkg\n\nfunc helper(n int) (int, error) { return n, nil }\n\n" +
					"func A() int { v, _ := helper(1); return v }\n",
			},
			partial: true,
		},
		{
			name: "go: the caller is rewritten in the same edit but its call still does not fit",
			files: map[string]string{
				"go.mod":   goMod,
				"pkg/x.go": "package pkg\n\nfunc helper(n int) int { return n }\n\nfunc A() int { return helper(1) }\n",
			},
			edits: map[string]string{
				"pkg/x.go": "package pkg\n\nfunc helper(n int, m int) int { return n + m }\n\n" +
					"func A() int { x := 2; return helper(x) }\n",
			},
			partial: true,
			warned:  []string{"example.com/wm/pkg.A"},
		},
		{
			name: "go: a comment in the caller is not a rewrite",
			files: map[string]string{
				"go.mod":   goMod,
				"pkg/x.go": "package pkg\n\nfunc helper(n int) int { return n }\n\nfunc A() int {\n\treturn helper(1)\n}\n",
			},
			edits: map[string]string{
				"pkg/x.go": "package pkg\n\nfunc helper(n int) (int, error) { return n, nil }\n\n" +
					"func A() int {\n\t// still the old call\n\treturn helper(1)\n}\n",
			},
			partial: true,
			warned:  []string{"example.com/wm/pkg.A"},
		},
		{
			name: "go: an untouched caller in another file keeps its warning",
			files: map[string]string{
				"go.mod":   goMod,
				"pkg/h.go": "package pkg\n\nfunc Helper(n int) int { return n }\n",
				"pkg/a.go": "package pkg\n\nfunc A() int { return Helper(1) }\n",
			},
			edits: map[string]string{
				"pkg/h.go": "package pkg\n\nfunc Helper(n int) (int, error) { return n, nil }\n",
			},
			warned: []string{"example.com/wm/pkg.A"},
		},
		{
			name: "python: a new helper is not warned, an untouched caller is",
			files: map[string]string{
				"x.py": "def helper(n) -> int:\n    return n\n\n\ndef a():\n    return helper(1)\n",
				"y.py": "from x import helper\n\n\ndef c():\n    return helper(2)\n",
			},
			edits: map[string]string{
				"x.py": "def helper(n) -> str:\n    return str(n)\n\n\ndef b():\n    return int(helper(1))\n\n\ndef a():\n    return b()\n",
			},
			warned: []string{"y.c"},
		},
		{
			name: "typescript: a new helper is not warned, an untouched caller is",
			files: map[string]string{
				"tsconfig.json": "{}",
				"x.ts":          "export function helper(n: number): number { return n; }\nexport function a(): number { return helper(1); }\n",
				"y.ts":          "import { helper } from './x';\nexport function c(): number { return helper(2); }\n",
			},
			edits: map[string]string{
				"x.ts": "export function helper(n: number): string { return String(n); }\n" +
					"export function b(): number { return Number(helper(1)); }\nexport function a(): number { return b(); }\n",
			},
			warned: []string{"y.c"},
		},
	}
}

func TestSignatureWarningNamesOnlyCallersLeftToVerify(t *testing.T) {
	for _, c := range staleCallerCases() {
		t.Run(c.name, func(t *testing.T) {
			p := newLiveProj(t, c.files)
			mtime := time.Now().Add(3 * time.Second)
			for rel, content := range c.edits {
				p.put(rel, content, mtime)
			}
			before := topology.PartialIncrementalCount()
			reported, err := p.mgr.IncrementalScan(p.dir, p.reg)
			if err != nil {
				t.Fatalf("IncrementalScan: %v", err)
			}
			if c.partial && topology.PartialIncrementalCount() == before {
				t.Fatal("the update did not take the partial path, so this case does not cover it")
			}

			var stored []string
			for _, w := range p.stored(domain.WarnSignatureChanged, "", "") {
				stored = append(stored, w.TargetID)
			}
			var shown []string
			for _, w := range reported {
				if w.Kind == domain.WarnSignatureChanged {
					shown = append(shown, w.TargetID)
				}
			}
			sort.Strings(stored)
			sort.Strings(shown)
			if !sameStrings(stored, c.warned) {
				t.Errorf("stored signature warnings name %v, want %v", stored, c.warned)
			}
			if !sameStrings(shown, c.warned) {
				t.Errorf("the scan reported signature warnings for %v, want %v", shown, c.warned)
			}
		})
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
