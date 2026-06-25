// EDGE: annotation-driven method resolution boundaries.
import { Circle, Rectangle } from "./shapes";

// Return-type annotation drives chaining across a call boundary.
function getCircle(): Circle {
  return new Circle(1);
}
export function viaReturn(): number {
  return getCircle().area(); // SHOULD: resolve via getCircle's return type -> Circle.area
}

// Union-typed param: only the first member is expected to resolve.
export function unionArea(s: Circle | Rectangle): number {
  return s.area(); // SHOULD resolve a shape's area; union likely resolves Circle only
}

// Intersection type.
type CircleLike = Circle & { tag: string };
export function intersectionArea(s: CircleLike): number {
  return s.area(); // does intersection resolve to Circle.area?
}

// `as` cast then method call.
export function castArea(x: unknown): number {
  return (x as Circle).area(); // does the cast drive resolution?
}

// Non-null assertion.
export function bangArea(c?: Circle): number {
  return c!.area(); // non-null assertion then call
}

// Type-alias indirection: a local alias of Circle.
type C = Circle;
export function aliasArea(c: C): number {
  return c.area(); // does alias C resolve through to Circle.area?
}

// Explicit `this` parameter type.
export function thisArea(this: Circle): number {
  return this.area(); // explicit this-param type -> Circle.area?
}
