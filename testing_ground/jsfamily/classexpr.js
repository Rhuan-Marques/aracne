// EDGE: class expressions (anonymous + named) assigned to consts, and a mixin
// chain built from two higher-order functions.
// SHOULD: Anon and Named are class resources extending shapes.Circle; Decorated
// extends the composed mixin chain rooted at Circle. ACTUAL (observe live):
// class expressions assigned to a const may not be extracted as classes, and
// the member-call base `extends Tagged(Timestamped(Circle))` likely does not
// resolve to shapes.Circle.
import { Circle } from "./shapes.js";

// Anonymous class expression assigned to a const.
export const Anon = class extends Circle {
  tag() {
    return "anon";
  }
};

// Named class expression (inner name visible only inside the expression).
export const Named = class NamedInner extends Circle {
  who() {
    return "named";
  }
};

// Two mixin HOFs composed into a single base expression.
const Timestamped = (Base) =>
  class extends Base {
    stamp() {
      return 0;
    }
  };
const Tagged = (Base) =>
  class extends Base {
    tagged() {
      return true;
    }
  };

export class Decorated extends Tagged(Timestamped(Circle)) {
  area() {
    return super.area();
  }
}
