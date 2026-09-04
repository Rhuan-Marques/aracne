// Package consumer wires together the geometry and shapes packages.
//
// It is the FLAGSHIP cross-package edge case: a function imports another
// package, receives the result of a function as a local variable, and then
// calls methods on that result. This forces cross-package return-type
// inference. It also exercises constructor-return inference, multi-value
// cross-package assignment, blank identifiers, and external-import calls
// (fmt) which must NOT produce topology edges.
package consumer

import (
	"fmt"

	"github.com/Rhuan-Marques/aracne/testing_ground/go/edge"
	"github.com/Rhuan-Marques/aracne/testing_ground/go/geometry"
	"github.com/Rhuan-Marques/aracne/testing_ground/go/shapes"
)

// Report receives the result of geometry.MakeCircle (a shapes.Circle) as a
// variable, then calls methods on it — cross-package return-type inference.
func Report() string {
	c := geometry.MakeCircle(3) // c is inferred as shapes.Circle
	area := c.Area()            // resolves to shapes.(Circle).Area
	desc := c.Describe()        // resolves to shapes.(Circle).Describe

	circ := shapes.NewCircle(1) // constructor returns *shapes.Circle
	_ = circ.Perimeter()        // method on the inferred *Circle

	rect := shapes.NewRectangle(2, 4)
	w, h := geometry.Bounds(rect) // multi-value cross-package assignment
	_ = w                         // blank usage of one return value

	// fmt is an external import: it must produce NO topology edges or warnings.
	return fmt.Sprintf("%s area=%.2f h=%.2f", desc, area, h)
}

// Total sums the areas of several shapes via the variadic geometry.SumAreas.
func Total() float64 {
	a := geometry.MakeCircle(1)                  // shapes.Circle (value)
	b := shapes.Triangle{Base: 3, Height: 4}     // shapes.Triangle (value)
	return geometry.SumAreas(a, b)               // both passed as shapes.Shape
}

// Decorated calls a package-level func-typed VAR in another package
// (edge.Colorize) — the cli/cli "utils.Yellow(s)" shape. EXPECTED: a
// uses_extvar edge to aracne/testing_ground/go/edge.Colorize, no uses_package
// edge for it (the extvar arm deliberately omits that, matching the
// incremental resolution path), and NO use_missing_node warning.
func Decorated(s string) string {
	return edge.Colorize(s)
}
