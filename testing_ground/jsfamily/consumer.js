// ES module exercising every import flavour: default, named, ALIASED named,
// NAMESPACE, and a SIDE-EFFECT import.
import makeSquare, { Circle as Disc, Rectangle } from "./shapes.js";
import * as factory from "./factory.js";
import "./shapes.js"; // side-effect import (no bindings)

// `new Disc(...)` resolves through the import alias back to Circle, so the
// method call resolves to Circle.area.
export function report() {
  const c = new Disc(2);
  const a = c.area();
  const square = makeSquare(4); // default-import call
  const made = factory.makeCircle(3); // namespace-import member call
  const rect = new Rectangle(2, 3);
  return a + rect.area() + (made ? 1 : 0) + (square ? 1 : 0);
}
