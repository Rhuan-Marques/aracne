// TypeScript type-level constructs: interfaces with single and multiple
// implementers, interface-extends-interface, type aliases (union, object,
// intersection, generic function type), and enums (bare + assigned members).

// Shape has MULTIPLE implementers (Base/Circle and Rectangle in shapes.ts).
export interface Shape {
  area(): number;
  describe(): string;
}

// Drawable has a SINGLE implementer (Canvas in shapes.ts).
export interface Drawable {
  draw(): string;
}

// Solid EXTENDS the Shape interface (interface-extends-interface).
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
