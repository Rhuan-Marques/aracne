// Package generics exercises Go generics: a constraint interface (type set),
// generic types with one and two type parameters, a generic constructor,
// generic functions, a function-typed parameter, and instantiation syntax.
package generics

// Number is a constraint interface using a type set (union of approximations).
type Number interface {
	~int | ~int64 | ~float64
}

// Stack is a generic type with a single type parameter.
type Stack[T any] struct {
	items []T
}

// NewStack is a generic constructor returning *Stack[T].
func NewStack[T any]() *Stack[T] {
	return &Stack[T]{}
}

// Appends a value to the top of the generic stack.
func (s *Stack[T]) Push(v T) { s.items = append(s.items, v) }

// Pop returns the top element and whether the stack was non-empty.
func (s *Stack[T]) Pop() (T, bool) {
	var zero T
	if len(s.items) == 0 {
		return zero, false
	}
	last := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return last, true
}

// Pair is a generic type with two constrained type parameters.
type Pair[K comparable, V any] struct {
	Key   K
	Value V
}

// Map applies fn to each element — a generic function with a FUNCTION-TYPED
// parameter and two type parameters.
func Map[T, U any](in []T, fn func(T) U) []U {
	out := make([]U, 0, len(in))
	for _, v := range in {
		out = append(out, fn(v))
	}
	return out
}

// Sum adds values using the Number constraint (variadic + constraint).
func Sum[T Number](values ...T) T {
	var total T
	for _, v := range values {
		total += v
	}
	return total
}

// Use instantiates the generics: Stack[int], Map[int,int], Sum[int].
func Use() int {
	s := NewStack[int]()
	s.Push(10)
	s.Push(20)
	v, _ := s.Pop()
	doubled := Map([]int{1, 2, 3}, func(n int) int { return n * 2 })
	return v + Sum(doubled...)
}
