// EDGE: type predicate, assertion function, satisfies, as const, narrow-then-call.
import { Circle, Rectangle } from "./shapes";

// User-defined type guard (type predicate `s is Circle`).
export function isCircle(s: Circle | Rectangle): s is Circle {
  return (s as Circle).area !== undefined;
}

// Assertion function (`asserts s is Circle`).
export function assertCircle(s: unknown): asserts s is Circle {
  if (!s) throw new Error("not a circle");
}

// `satisfies` operator.
export const defaults = { r: 1 } satisfies { r: number };

// `as const`.
export const PALETTE = ["red", "green"] as const;

// Narrowing via the guard, then a method call.
export function areaIfCircle(s: Circle | Rectangle): number {
  if (isCircle(s)) return s.area(); // SHOULD: calls isCircle; narrowed -> Circle.area
  return 0;
}
