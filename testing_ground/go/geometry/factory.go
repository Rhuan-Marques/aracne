// Package geometry builds and combines shapes from the shapes package.
//
// It exercises CROSS-PACKAGE return types (functions that return a type owned
// by another package), variadic parameters over an interface, multiple return
// values, and interface-method fan-out.
package geometry

import "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes"

// MakeCircle returns a shapes.Circle BY VALUE — a cross-package return type.
// This is the "Package2.FunctionA returns Package1.StructA" edge case.
func MakeCircle(r float64) shapes.Circle {
	return shapes.Circle{Radius: r, Kind: shapes.KindCircle}
}

// BuildRectangle returns a *shapes.Rectangle via the shapes constructor.
func BuildRectangle(w, h float64) *shapes.Rectangle {
	rect := shapes.NewRectangle(w, h)
	return &rect
}

// SumAreas is VARIADIC over the shapes.Shape interface and calls Area() on each.
func SumAreas(items ...shapes.Shape) float64 {
	var total float64
	for _, item := range items {
		total += item.Area() // interface method -> fan-out to every implementer
	}
	return total
}

// Bounds returns MULTIPLE values describing a rectangle.
func Bounds(r shapes.Rectangle) (width, height float64) {
	width = r.Width
	height = r.Height
	return
}

// DescribeAll calls Describe on a slice of shapes (interface method fan-out).
func DescribeAll(list []shapes.Shape) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.Describe())
	}
	return out
}
