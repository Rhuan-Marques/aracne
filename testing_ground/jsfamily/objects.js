// EDGE: object-literal methods (shorthand + arrow property + getter) and the
// revealing-module pattern (a factory returning an object whose methods are
// never extracted as resources).
// SHOULD: geometryOps.makeCircle is a callable resource that uses shapes.Circle.
// ACTUAL (observe live): object-literal methods are not topology resources, so
// calls to them resolve to nothing.
import { Circle } from "./shapes.js";

// Object literal with method shorthand, an arrow property, and a getter.
export const geometryOps = {
  makeCircle(r) {
    return new Circle(r);
  },
  scale: (c, k) => {
    c.scale = k;
    return c;
  },
  get origin() {
    return { x: 0, y: 0 };
  },
};

// Revealing-module factory: returns an object with methods closing over state.
export function createCalculator() {
  let total = 0;
  return {
    add(c) {
      total += c.area();
      return this;
    },
    result() {
      return total;
    },
  };
}

// Consumer calling an object-literal method.
export function useOps() {
  const c = geometryOps.makeCircle(2); // EXPECTED gap: object method, unresolved
  return c ? 1 : 0;
}
