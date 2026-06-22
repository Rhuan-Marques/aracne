// TypeScript type-level constructs: interfaces with single and multiple
// implementers, interface-extends-interface, type aliases (union, object,
// intersection, generic function type), and enums (bare + assigned members).

// Interface defining methods for geometric shapes: area() and describe().
export interface Shape {
  area(): number;
  describe(): string;
}

// Interface defining a draw method that returns a string representation.
export interface Drawable {
  draw(): string;
}

// Interface extending Shape to add volume() method for 3D solids.
export interface Solid extends Shape {
  volume(): number;
}

export type ID = string | number; // union type alias
export type Labeled = { label: string }; // object type alias
export type Tagged = Shape & Labeled; // intersection type alias
export type Factory<T> = (value: number) => T; // generic (function) type alias

// Enum with one assigned member; the rest are auto-numbered.
export enum Color {
  Red,
  Green = 2,
  Blue,
}

// Bare enum.
export enum Direction {
  Up,
  Down,
}
