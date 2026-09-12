package universaltools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
)

// The RD-4 fixture: two methods of one type, so a span that drifted onto the neighbouring
// declaration is visible as the wrong method's source.
const shapesSource = `package shapes

// Circle is a round shape.
type Circle struct {
	R float64
}

// Area returns the circle's area.
func (c Circle) Area() float64 {
	return 3.14159 * c.R * c.R
}

// Perimeter returns the circle's perimeter.
func (c Circle) Perimeter() float64 {
	return 2 * 3.14159 * c.R
}
`

const (
	areaID      = "example.com/p/shapes.(Circle).Area"
	perimeterID = "example.com/p/shapes.(Circle).Perimeter"
	areaSig     = "func (c Circle) Area() float64 {"
	perimSig    = "func (c Circle) Perimeter() float64 {"
	areaBody    = "return 3.14159 * c.R * c.R"
)

// rewriteOutsideSession replaces a file's content the way an editor or a checkout would --
// without telling the topology -- and moves its mtime clearly past the manifest's stamp, so the
// test does not depend on the filesystem's timestamp granularity.
func rewriteOutsideSession(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func scanShapes(t *testing.T) (*universaltools.Read, string) {
	t.Helper()
	mgr, reg, dir := scanFiles(t, map[string]string{
		"go.mod":           "module example.com/p\n\ngo 1.25\n",
		"shapes/shapes.go": shapesSource,
	})
	return universaltools.NewRead(mgr, helper.DefaultConfig(), false, reg), filepath.Join(dir, "shapes", "shapes.go")
}

// shifted is the fixture with three comment lines inserted near the top: every span below them
// moves down by three, and the old spans still fit inside the file.
func shifted() string {
	return strings.Replace(shapesSource, "package shapes\n\n", "package shapes\n\n// one\n// two\n// three\n", 1)
}

// swapped keeps the line count and swaps the two methods, so each old span now holds the
// other method.
func swapped() string {
	area := "// Area returns the circle's area.\nfunc (c Circle) Area() float64 {\n\treturn 3.14159 * c.R * c.R\n}\n"
	perim := "// Perimeter returns the circle's perimeter.\nfunc (c Circle) Perimeter() float64 {\n\treturn 2 * 3.14159 * c.R\n}\n"
	out := strings.Replace(shapesSource, area, "@@AREA@@", 1)
	out = strings.Replace(out, perim, area, 1)
	return strings.Replace(out, "@@AREA@@", perim, 1)
}

// TestReadRefreshesAFileChangedOutsideTheSession pins RD-4 on the read tool itself -- the MCP
// tool and `arac read` share this path.
//
// Nothing refreshed the topology before a read, and Cut raised StaleIndexError only for a span
// running past the end of the file. A span that still fit was served as whatever lines sat
// there now: the Area read came back as the tail of the type above it, and with the methods
// swapped each read returned the other method.
func TestReadRefreshesAFileChangedOutsideTheSession(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content func() string
	}{
		{"lines shifted", shifted},
		{"same line count", swapped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rd, file := scanShapes(t)
			rewriteOutsideSession(t, file, tc.content())

			out, err := rd.ReadIDs([]string{areaID}, universaltools.ReadIDsOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, areaSig+"\n\t"+areaBody+"\n}") {
				t.Fatalf("the read must return Area as it is on disk now:\n%s", out)
			}
			if strings.Contains(out, perimSig) || strings.Contains(out, "// three") {
				t.Fatalf("the read served the lines at Area's OLD span:\n%s", out)
			}

			out, err = rd.ReadIDs([]string{perimeterID}, universaltools.ReadIDsOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, perimSig+"\n\treturn 2 * 3.14159 * c.R\n}") || strings.Contains(out, areaSig) {
				t.Fatalf("the read must return Perimeter as it is on disk now:\n%s", out)
			}
		})
	}
}

// TestReadRefreshesAChangedNeighbourFile covers the other files a read cuts from: under
// read.context_filter "full" a callee's whole body is inlined into the caller's context, cut
// at the callee's recorded span in ITS file.
func TestReadRefreshesAChangedNeighbourFile(t *testing.T) {
	mgr, reg, dir := scanFiles(t, map[string]string{
		"go.mod":  "module probe\n\ngo 1.25\n",
		"dep.go":  "package probe\n\n// Helper does a helpful thing.\nfunc Helper() int { return 7 }\n",
		"main.go": "package probe\n\nfunc Entry() int { return Helper() }\n",
	})
	rewriteOutsideSession(t, filepath.Join(dir, "dep.go"),
		"package probe\n\n// one\n// two\n// three\n\n// Helper does a helpful thing.\nfunc Helper() int { return 7 }\n")

	cfg := helper.DefaultConfig()
	cfg.Read.ContextFilter = helper.ContextFilterFull
	out, err := universaltools.NewRead(mgr, cfg, false, reg).
		ReadIDs([]string{"probe.Entry"}, universaltools.ReadIDsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "func Helper() int { return 7 }") {
		t.Fatalf("the callee inlined into the context must be its current source:\n%s", out)
	}
}

// TestShellReadEntrancesRefreshAChangedFile covers the intercepted shell read and the guard's
// proxied read, which reach the topology through BodyBounds / ReadSlice / ReadWindow rather
// than ReadIDs -- and pick their lines from recorded spans before any cut happens.
func TestShellReadEntrancesRefreshAChangedFile(t *testing.T) {
	lineOf := func(content, needle string) int {
		for i, line := range strings.Split(content, "\n") {
			if strings.Contains(line, needle) {
				return i + 1
			}
		}
		t.Fatalf("%q not in fixture", needle)
		return 0
	}
	now := shifted()

	t.Run("BodyBounds", func(t *testing.T) {
		rd, file := scanShapes(t)
		rewriteOutsideSession(t, file, now)
		path, from, to, err := rd.BodyBounds(areaID)
		if err != nil {
			t.Fatal(err)
		}
		if path != file || from != lineOf(now, areaBody) || to != lineOf(now, areaBody)+1 {
			t.Fatalf("BodyBounds(Area) = %s:%d-%d, want the body at %d-%d",
				path, from, to, lineOf(now, areaBody), lineOf(now, areaBody)+1)
		}
	})

	t.Run("ReadSlice", func(t *testing.T) {
		rd, file := scanShapes(t)
		rewriteOutsideSession(t, file, now)
		line := lineOf(now, areaBody)
		out, err := rd.ReadSlice(file, line, line)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, areaSig) || strings.Contains(out, perimSig) {
			t.Fatalf("the window must be framed by the declaration it sits in now:\n%s", out)
		}
	})

	t.Run("ReadWindow", func(t *testing.T) {
		rd, file := scanShapes(t)
		rewriteOutsideSession(t, file, now)
		out, err := rd.ReadWindow(file, lineOf(now, areaSig), lineOf(now, areaBody)+1, universaltools.ReadIDsOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, areaSig) || strings.Contains(out, perimSig) {
			t.Fatalf("a window over Area must answer with Area:\n%s", out)
		}
	})
}
