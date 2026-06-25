// EDGE: circular ES module imports (a <-> b) with cross-module calls each way.
// SHOULD: useB() `calls` circular_b.fromB; the import cycle resolves both ways
// without infinite recursion in the scanner.
import { fromB } from "./circular_b.js";
import { Circle } from "./shapes.js";

// Local constructor.
export function fromA(r) {
  return new Circle(r);
}

// Calls into the other half of the cycle.
export function useB() {
  return fromB(2); // SHOULD: calls circular_b.fromB
}
