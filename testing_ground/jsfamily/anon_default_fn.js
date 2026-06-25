// EDGE: anonymous default-exported function declaration. SHOULD: the module's
// default_export points at a resource for this function so default imports
// elsewhere resolve. ACTUAL (observe live): parseExport looks for a child
// identifier on the default export; an anonymous function has none, so the
// default export name is expected to be LOST.
import { Circle } from "./shapes.js";

// Anonymous default-exported function.
export default function (r) {
  return new Circle(r).area();
}
