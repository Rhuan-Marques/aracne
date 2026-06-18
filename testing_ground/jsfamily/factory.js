// ES module mixing a DEFAULT import with NAMED imports, and exercising the four
// function flavours: declaration, arrow, async, and generator. It also shows
// `new`-based method resolution (works) vs return-value chaining (not tracked).
import makeSquare, { Circle, Rectangle } from "./shapes.js";

// Function declaration that returns `new Circle(...)`.
export function makeCircle(radius) {
  return new Circle(radius);
}

// Arrow function bound to a const (parsed as a function with Kind "arrow").
export const makeRect = (w, h) => new Rectangle(w, h);

// Async function.
export async function loadCircle(radius) {
  return new Circle(radius);
}

// Generator function that calls an internal function.
export function* shapeStream(count) {
  for (let i = 0; i < count; i++) {
    yield makeCircle(i);
  }
}

// `new X()` makes the local var's class known, so the method call RESOLVES.
export function circleArea(radius) {
  const c = new Circle(radius);
  return c.area();
}

// makeSquare's return type is NOT tracked, so `sq.area()` does NOT resolve.
// (Documented JS limitation: plain return values are not type-inferred.)
export function squareArea(side) {
  const sq = makeSquare(side);
  return sq.area();
}
