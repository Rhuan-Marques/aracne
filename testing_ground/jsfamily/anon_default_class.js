// EDGE: anonymous default-exported class. SHOULD: a class resource exists and
// the module's default_export points at it. ACTUAL (observe live): an anonymous
// default class has no name identifier, so it is expected to be dropped or left
// nameless (and inheritance from shapes.Circle unresolved).
import { Circle } from "./shapes.js";

// Anonymous default-exported class extending an imported class.
export default class extends Circle {
  doubled() {
    return this.area() * 2;
  }
}
