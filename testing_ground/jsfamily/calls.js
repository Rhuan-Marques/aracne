// EDGE: intra-class this.method(), super()/super.method(), optional-chaining
// calls, a chained call on a `new` expression, a function passed by reference,
// and a default parameter initialized by a call.
import { Circle } from "./shapes.js";
import { makeCircle } from "./factory.js";

// this.method() intra-class call. SHOULD: run() `calls` Helper.compute.
export class Helper {
  compute(r) {
    return new Circle(r).area();
  }
  run(r) {
    return this.compute(r); // intra-class `this` call -> calls Helper.compute?
  }
}

// super() constructor call + super.method() call.
export class LoudCircle extends Circle {
  constructor(r) {
    super(r); // super() -> shapes.Circle.constructor?
  }
  area() {
    return super.area() * 2; // super.area() -> shapes.Circle.area?
  }
}

// Optional-chaining method call.
export function maybeArea(c) {
  return c?.area?.(); // optional-chaining call
}

// Chained call directly on a `new` expression (no intermediate variable).
export function inlineArea(r) {
  return new Circle(r).area(); // SHOULD: uses_class + calls shapes.Circle.area
}

// Function passed by reference (not called here), then chained.
export function indirect(rs) {
  return rs.map(makeCircle).map((c) => c.area()); // makeCircle passed by reference
}

// Default parameter initialized by a function call.
export function withDefault(c = makeCircle(1)) {
  return c.area(); // does the default-value call to makeCircle get tracked?
}
