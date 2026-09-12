package goscanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// GO-04. An embedded interface (`type NamedShape interface { Shape; Named }`) was recorded as
// a METHOD named after the embedded interface, so satisfying it meant having a method called
// "Shape" -- which nothing has. Composed interfaces, the io.ReadWriter pattern, therefore had
// zero implementers, while docs/architecture.md §10 advertises interface<->struct matching.

func scanGoDir(t *testing.T, files map[string]string) *domain.Topology {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	topo, err := NewGoScanner().Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return topo
}

func implementsIDs(topo *domain.Topology, id string) []string {
	return topo.Resources[id].Connections["implements"]
}

func hasString(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}

func TestGO04_EmbeddedInterfacesAreExpandedForMatching(t *testing.T) {
	topo := scanGoDir(t, map[string]string{
		"go.mod": "module ex\n\ngo 1.21\n",
		"shapes/shapes.go": `package shapes

type Shape interface{ Area() int }

type Named interface{ Name() string }

type NamedShape interface {
	Shape
	Named
}

type Circle struct{}

func (Circle) Area() int    { return 1 }
func (Circle) Name() string { return "c" }

type Square struct{}

func (Square) Area() int { return 2 }
`,
	})

	circle := implementsIDs(topo, "ex/shapes.Circle")
	if !hasString(circle, "ex/shapes.NamedShape") {
		t.Errorf("Circle has Area and Name but does not implement the composed NamedShape: %v", circle)
	}
	// The parts still match on their own.
	if !hasString(circle, "ex/shapes.Shape") || !hasString(circle, "ex/shapes.Named") {
		t.Errorf("Circle lost an edge to a plain interface: %v", circle)
	}
	// And the counter-case: a type missing one half must NOT implement the composition.
	square := implementsIDs(topo, "ex/shapes.Square")
	if hasString(square, "ex/shapes.NamedShape") {
		t.Errorf("Square has no Name() yet implements NamedShape: %v", square)
	}
	if !hasString(square, "ex/shapes.Shape") {
		t.Errorf("Square implements Shape and should say so: %v", square)
	}
}

// An embed this scanner cannot read (an interface from another package it did not parse)
// leaves the requirement unknown, so no implements edge is claimed for it.
func TestGO04_AnUnresolvableEmbedClaimsNothing(t *testing.T) {
	topo := scanGoDir(t, map[string]string{
		"go.mod": "module ex\n\ngo 1.21\n",
		"temp/temp.go": `package temp

import "fmt"

type MyStringer interface {
	fmt.Stringer
}

type Celsius float64

func (c Celsius) String() string { return "c" }
`,
	})
	if hasString(implementsIDs(topo, "ex/temp.Celsius"), "ex/temp.MyStringer") {
		t.Error("claimed an implements edge for an interface whose embedded requirements were never read")
	}
}
