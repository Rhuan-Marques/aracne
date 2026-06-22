// ES module mixing a DEFAULT import with NAMED imports, and exercising the four
// function flavours: declaration, arrow, async, and generator. It also shows
// `new`-based method resolution (works) vs return-value chaining (not tracked).
import makeSquare, { Circle, Rectangle } from "./shapes.js";

// Constructs and returns a Circle instance with the given radius
export function makeCircle(radius) {
  return new Circle(radius);
}

// Creates and returns a Rectangle instance with given width and height.
export const makeRect = (w, h) => new Rectangle(w, h);

// Asynchronously creates and returns a Circle with the given radius
export async function loadCircle(radius) {
  return new Circle(radius);
}

// Generator that yields Circle instances sequentially, creating one for each iteration up to count.
export function* shapeStream(count) {
  for (let i = 0; i < count; i++) {
    yield makeCircle(i);
  }
}

// Creates a Circle with the given radius and returns its area
export function circleArea(radius) {
  const c = new Circle(radius);
  return c.area();
}

// Creates a square with given side length and returns its area.
export function squareArea(side) {
  const sq = makeSquare(side);
  return sq.area();
}
