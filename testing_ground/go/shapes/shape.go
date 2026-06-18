// Package shapes defines geometric interfaces and their implementations.
//
// It is the root of the Go testing-ground graph and exercises: an interface
// with MULTIPLE implementations, an interface with a SINGLE implementation,
// an embedded interface, an empty-interface field, value vs pointer receivers,
// embedded structs, constructors (pointer / value / multi-return), an iota
// const block, package-level vars/consts, and named return values.
package shapes

import (
	"fmt"
	"math"
)

// Kind enumerates shape categories using an iota const block.
type Kind int

const (
	KindUnknown Kind = iota
	KindCircle
	KindRectangle
	KindTriangle
)

// Pi is a package-level variable initialized from an external (math) symbol.
var Pi = math.Pi

// DefaultName is an untyped package-level constant.
const DefaultName = "shape"

// Shape is an interface with MULTIPLE implementations (Circle, Rectangle, Triangle).
type Shape interface {
	Area() float64
	Perimeter() float64
	Describe() string
}

// Drawable is an interface with a SINGLE implementation (Canvas).
type Drawable interface {
	Draw() string
}

// Solid EMBEDS the Shape interface and adds a method. Embedded interfaces are a
// known parser edge case: the embedded interface is recorded as a pseudo-method
// rather than being flattened into Shape's methods.
type Solid interface {
	Shape
	Volume() float64
}

// Container has an EMPTY-INTERFACE field. Empty interfaces never receive an
// implemented_by edge.
type Container struct {
	Any   interface{}
	Items []any
}

// Circle implements Shape with VALUE receivers.
type Circle struct {
	Radius float64
	Kind   Kind
}

// NewCircle is a constructor returning *Circle.
func NewCircle(r float64) *Circle {
	return &Circle{Radius: r, Kind: KindCircle}
}

func (c Circle) Area() float64      { return Pi * c.Radius * c.Radius }
func (c Circle) Perimeter() float64 { return 2 * Pi * c.Radius }
func (c Circle) Describe() string   { return fmt.Sprintf("circle r=%.2f", c.Radius) }

// Rectangle implements Shape with POINTER receivers.
type Rectangle struct {
	Width  float64
	Height float64
}

// NewRectangle is a constructor returning a value (not a pointer).
func NewRectangle(w, h float64) Rectangle {
	return Rectangle{Width: w, Height: h}
}

func (r *Rectangle) Area() float64      { return r.Width * r.Height }
func (r *Rectangle) Perimeter() float64 { return 2 * (r.Width + r.Height) }
func (r *Rectangle) Describe() string   { return fmt.Sprintf("rect %gx%g", r.Width, r.Height) }

// Square EMBEDS Rectangle (embedded struct) and reuses its promoted methods.
type Square struct {
	Rectangle
	Label string
}

// Triangle implements Shape and uses a NAMED return value in one method.
type Triangle struct {
	Base   float64
	Height float64
	SideA  float64
	SideB  float64
}

// NewTriangle is a MULTI-RETURN constructor (value + error).
func NewTriangle(base, height float64) (Triangle, error) {
	if base <= 0 || height <= 0 {
		return Triangle{}, fmt.Errorf("invalid triangle: base=%g height=%g", base, height)
	}
	return Triangle{Base: base, Height: height}, nil
}

func (t Triangle) Area() float64 { return 0.5 * t.Base * t.Height }

func (t Triangle) Perimeter() (total float64) { // named return value
	total = t.Base + t.SideA + t.SideB
	return
}

func (t Triangle) Describe() string { return "triangle" }

// Canvas is the SINGLE implementation of Drawable.
type Canvas struct {
	Shapes []Shape
}

func (cv *Canvas) Draw() string { return fmt.Sprintf("canvas with %d shapes", len(cv.Shapes)) }
