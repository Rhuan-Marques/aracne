// EDGE: consumes bindings THROUGH the barrel (re-export chain resolution).
// SHOULD: `new Disc(2)` -> uses_class shapes.Circle (+ constructor call);
// makeSquare(4) -> calls shapes.makeSquare; factoryNS.makeCircle(3) -> calls
// factory.makeCircle. ACTUAL (observe live): unresolved if barrel.js drops its
// re-export sources (re-export chain not followed).
import { Disc, makeSquare, factoryNS } from "./barrel.js";

// Sums areas obtained through re-exported bindings.
export function useBarrel() {
  const c = new Disc(2); // EXPECTED: uses_class shapes.Circle
  const sq = makeSquare(4); // EXPECTED: calls shapes.makeSquare
  const made = factoryNS.makeCircle(3); // EXPECTED: calls factory.makeCircle
  return c.area() + (sq ? 1 : 0) + (made ? 1 : 0);
}
