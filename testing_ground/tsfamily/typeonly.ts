// EDGE: type-only imports / exports.
// SHOULD: a type-only import still lets an annotation drive resolution
// (describe(s: Shape) -> Shape.describe). ACTUAL (observe live): if type-only
// imports are skipped, the annotation will not resolve.
import type { Shape } from "./models";
import { type Box, Circle } from "./shapes"; // mixed: `type Box` + value Circle
export type { Shape } from "./models"; // type-only re-export

// Type-only import used as an annotation that drives method resolution.
export function describe(s: Shape): string {
  return s.describe(); // SHOULD: resolve to models.Shape (interface) describe()
}

// Value import used normally.
export function build(): Circle {
  return new Circle(1);
}

// Box referenced only as a type (type-only binding).
export function boxed(b: Box<Circle>): Circle {
  return b.get();
}
