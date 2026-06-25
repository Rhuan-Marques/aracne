// EDGE: the other half of the import cycle (see circular_a.js).
// SHOULD: fromB() `calls` circular_a.fromA; loopback() `calls` circular_b.fromB.
import { fromA } from "./circular_a.js";

// Calls back into the first half of the cycle.
export function fromB(r) {
  return fromA(r); // SHOULD: calls circular_a.fromA
}

// Local self-call.
export function loopback() {
  return fromB(1); // SHOULD: calls circular_b.fromB
}
