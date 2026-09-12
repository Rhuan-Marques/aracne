package topology_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// coldAndIncremental full-scans files, applies edit (writes) and removes the paths in
// remove, runs an incremental scan, and returns it next to a cold scan of the result.
func coldAndIncremental(t *testing.T, files, edit map[string]string, remove []string) (dir string, incremental, cold *domain.Topology) {
	t.Helper()
	dir = t.TempDir()
	additionWrite(t, dir, files, time.Time{})
	reg := contractRegistry()
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}
	additionWrite(t, dir, edit, time.Now().Add(3*time.Second))
	for _, rel := range remove {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("IncrementalScan: %v", err)
	}
	var err error
	if incremental, err = mgr.ReadAll(); err != nil {
		t.Fatal(err)
	}
	c := topology.New()
	c.Load(filepath.Join(t.TempDir(), "cold.db"))
	if err := c.FullScan(dir, reg); err != nil {
		t.Fatalf("cold FullScan: %v", err)
	}
	if cold, err = c.ReadAll(); err != nil {
		t.Fatal(err)
	}
	return dir, incremental, cold
}

func requireSameGraph(t *testing.T, incremental, cold *domain.Topology, want []string) {
	t.Helper()
	coldEdges := additionEdges(cold)
	for _, edge := range want {
		if !coldEdges[edge] {
			t.Fatalf("the cold scan has no %q, so this case proves nothing", edge)
		}
	}
	if diff := additionDiff(incremental, cold); len(diff) > 0 {
		t.Errorf("incremental scan differs from a cold scan (- incremental only, + cold only):\n  %s",
			strings.Join(diff, "\n  "))
	}
}

// A method added to a grandparent binds an unqualified call two classes down, in a file with
// no edge to the grandparent's file: the change reaches it through the class in between.
func TestIncrementalScanFollowsInheritanceToAGrandchild(t *testing.T) {
	_, inc, cold := coldAndIncremental(t, map[string]string{
		"pom.xml":                         additionPom,
		"src/main/java/com/ex/Base.java":  "package com.ex;\n\npublic class Base {\n}\n",
		"src/main/java/com/ex/Mid.java":   "package com.ex;\n\npublic class Mid extends Base {\n}\n",
		"src/main/java/com/ex/Leaf.java":  "package com.ex;\n\npublic class Leaf extends Mid {\n    public void go() {\n        fresh();\n    }\n}\n",
		"src/main/java/com/ex/Other.java": "package com.ex;\n\npublic class Other {\n}\n",
	}, map[string]string{
		"src/main/java/com/ex/Base.java": "package com.ex;\n\npublic class Base {\n    public void fresh() {\n    }\n}\n",
	}, nil)
	requireSameGraph(t, inc, cold, []string{"com.ex.Leaf.go() -calls-> com.ex.Base.fresh()"})
}

// One FQN in two source sets: deleting the copy the graph happened to hold must leave the
// other file's copy in place, as a cold scan of what remains does.
func TestIncrementalScanKeepsATypeItsOtherFileStillDeclares(t *testing.T) {
	files := map[string]string{
		"pom.xml": additionPom,
		"src/main/java/com/ex/a/A.java": "package com.ex.a;\n\npublic class A {\n" +
			"    public int m() { return 1; }\n    public int onlyMain() { return 1; }\n}\n",
		"src/main/java11/com/ex/a/A.java": "package com.ex.a;\n\n// the java 11 variant\npublic class A {\n" +
			"    public int m() { return 11; }\n    public int only11() { return 11; }\n}\n",
		"src/main/java/com/ex/b/U.java": "package com.ex.b;\n\nimport com.ex.a.A;\n\npublic class U {\n" +
			"    public int use(A a) { return a.m(); }\n}\n",
	}
	for _, gone := range []string{"src/main/java11/com/ex/a/A.java", "src/main/java/com/ex/a/A.java"} {
		t.Run(gone, func(t *testing.T) {
			dir, inc, cold := coldAndIncremental(t, files, nil, []string{gone})
			requireSameGraph(t, inc, cold, []string{"com.ex.b.U.use(A) -calls-> com.ex.a.A.m()"})
			a, ok := inc.Resources["com.ex.a.A"]
			if !ok {
				t.Fatal("com.ex.a.A was removed with one of its two files")
			}
			if a.Location.Path == filepath.Join(dir, filepath.FromSlash(gone)) {
				t.Errorf("com.ex.a.A still located in the deleted %s", gone)
			}
		})
	}
	// Editing either copy leaves the graph where a cold scan has it.
	for _, edited := range []string{"src/main/java11/com/ex/a/A.java", "src/main/java/com/ex/a/A.java"} {
		t.Run("edit "+edited, func(t *testing.T) {
			_, inc, cold := coldAndIncremental(t, files, map[string]string{
				edited: strings.Replace(files[edited], "return 1", "return 2", 1) + "\n",
			}, nil)
			requireSameGraph(t, inc, cold, nil)
		})
	}
}
