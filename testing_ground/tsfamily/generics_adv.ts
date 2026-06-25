// EDGE: generics — constraints, default type params, generic instantiation, and
// advanced type-level constructs (keyof / indexed access / mapped / conditional
// / infer) as parse stress.
import { Circle, Box } from "./shapes";

// Constrained generic: method call on a constrained type parameter.
export function firstArea<T extends { area(): number }>(items: T[]): number {
  return items[0].area(); // SHOULD: resolve area() via the constraint (hard)
}

// Default type parameter.
export function makeBox<T = Circle>(v: T): Box<T> {
  return new Box<T>(v);
}

// Generic class instantiation + chained get().area().
export function unwrap(): number {
  const b: Box<Circle> = new Box(new Circle(1));
  return b.get().area(); // SHOULD: Box<Circle>.get() -> Circle -> .area(); likely gap
}

// Type-level constructs (parse stress — must not crash the scanner).
export type Keys = keyof Circle;
export type AreaType = Circle["area"];
export type Maybe<T> = T extends null ? never : T;
export type ReadonlyShape = { readonly [K in keyof Circle]: Circle[K] };
export type ElementOf<T> = T extends (infer U)[] ? U : never;
