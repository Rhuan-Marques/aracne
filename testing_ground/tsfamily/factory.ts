// TypeScript factory: cross-module return types and ANNOTATION-DRIVEN method
// resolution — the headline TS feature where a type annotation (param, local,
// or return) lets a method call resolve without a `new` expression.
import { Circle, Rectangle } from "./shapes";
import { Shape, Factory } from "./models";

// Creates and returns a new Circle with the specified radius.
export function makeCircle(radius: number): Circle {
  return new Circle(radius);
}

// Creates and returns a new Rectangle with the specified width and height.
export function makeRectangle(w: number, h: number): Rectangle {
  return new Rectangle(w, h);
}

// Returns a string description of the given shape by calling its describe method.
export function render(shape: Shape): string {
  return shape.describe();
}

// Creates a circle with given radius and returns its area.
export function areaOf(radius: number): number {
  const c: Circle = makeCircle(radius);
  return c.area();
}

// Factory function that creates a Circle instance with a given radius.
export const circleFactory: Factory<Circle> = (n) => new Circle(n);
