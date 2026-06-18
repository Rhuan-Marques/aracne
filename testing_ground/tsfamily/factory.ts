// TypeScript factory: cross-module return types and ANNOTATION-DRIVEN method
// resolution — the headline TS feature where a type annotation (param, local,
// or return) lets a method call resolve without a `new` expression.
import { Circle, Rectangle } from "./shapes";
import { Shape, Factory } from "./models";

// Return annotation is a class from another module (cross-module return type).
export function makeCircle(radius: number): Circle {
  return new Circle(radius);
}

export function makeRectangle(w: number, h: number): Rectangle {
  return new Rectangle(w, h);
}

// The parameter is typed as the Shape INTERFACE, so `shape.describe()` resolves
// via the annotation — no `new` required.
export function render(shape: Shape): string {
  return shape.describe();
}

// A LOCAL annotation (`const c: Circle`) drives resolution of `c.area()`.
export function areaOf(radius: number): number {
  const c: Circle = makeCircle(radius);
  return c.area();
}

// A const typed with a generic type alias (Factory<Circle>).
export const circleFactory: Factory<Circle> = (n) => new Circle(n);
