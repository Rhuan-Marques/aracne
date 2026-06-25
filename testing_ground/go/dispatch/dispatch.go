// Package dispatch collects Go method-dispatch edge cases that probe the limits
// of static call resolution: a method VALUE (f := d.Sound), a method EXPRESSION
// (g := Dog.Sound), a TYPE ASSERTION (a.(Dog)), and a TYPE SWITCH
// (switch a.(type)). The scanner resolves direct method calls; whether it
// resolves calls through these indirections is what these cases probe.
package dispatch

// Animal is an interface with two implementers.
type Animal interface {
	Sound() string
}

// Dog implements Animal.
type Dog struct{ Name string }

// Sound makes Dog satisfy Animal.
func (d Dog) Sound() string { return "woof" }

// Cat implements Animal.
type Cat struct{ Name string }

// Sound makes Cat satisfy Animal.
func (c Cat) Sound() string { return "meow" }

// UseMethodValue binds a METHOD VALUE (d.Sound, receiver already bound) to a
// variable and calls it — probes whether the call resolves to Dog.Sound.
func UseMethodValue(d Dog) string {
	f := d.Sound // method value
	return f()
}

// UseMethodExpr binds a METHOD EXPRESSION (Dog.Sound, receiver passed
// explicitly) to a variable and calls it — probes whether the call resolves to
// Dog.Sound.
func UseMethodExpr() string {
	g := Dog.Sound // method expression
	return g(Dog{Name: "rex"})
}

// AssertAnimal uses a TYPE ASSERTION to recover the concrete Dog and call a
// method on it — probes whether the asserted variable's method call resolves.
func AssertAnimal(a Animal) string {
	if d, ok := a.(Dog); ok {
		return d.Sound()
	}
	return ""
}

// SwitchAnimal uses a TYPE SWITCH to dispatch on the concrete type and call a
// method per case — probes whether switch-bound variables resolve method calls.
func SwitchAnimal(a Animal) string {
	switch v := a.(type) {
	case Dog:
		return v.Sound()
	case Cat:
		return v.Sound()
	default:
		return "?"
	}
}
