// EDGE: nested function declarations, returned closures, an IIFE, a
// function-expression assigned to a const, and a top-level `new` statement.
// SHOULD: `inner` is its own function resource; the `new Circle` inside it is
// attributed to a function; the top-level `new Circle` attaches somewhere.
// ACTUAL (observe live): nested functions are not extracted, and collectBody
// does not recurse into nested function bodies, so inner's edge is likely
// dropped and not attributed to outer either.
import { Circle } from "./shapes.js";

// Top-level statement constructing a Circle (no enclosing function).
const bootstrap = new Circle(1); // EXPECTED: uses_class edge attaches to module?

// Outer function with a NESTED function declaration.
export function outer(n) {
  function inner(x) {
    return new Circle(x); // nested-body `new` -> inner? outer? dropped?
  }
  return inner(n).area();
}

// Returned closure (arrow) capturing a param.
export function makeAdder(base) {
  return (x) => new Circle(base + x);
}

// IIFE evaluated at module load.
export const computed = (function () {
  return new Circle(7).area();
})();

// Function expression assigned to a const, then called (return-value chaining).
const localFactory = function (r) {
  return new Circle(r);
};
export function useLocalFactory(r) {
  return localFactory(r).area(); // local return-chaining -> does .area() resolve?
}
