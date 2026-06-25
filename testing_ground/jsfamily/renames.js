// EDGE: export list with renames, including a rename TO `default` of a local
// declaration. SHOULD: helper, CONSTANT, and the module default all resolve to
// their local declarations (localHelper, LOCAL, Widget). ACTUAL (observe live):
// export-clause aliases are recorded in pr.Exports; the default-from-local
// rename (`Widget as default`) is the interesting case to confirm.
import { Circle } from "./shapes.js";

// Local function re-exported under a different name.
function localHelper(r) {
  return new Circle(r);
}

const LOCAL = 42;

// Local class exported AS the module default via a rename.
class Widget {
  build() {
    return new Circle(1);
  }
}

export { localHelper as helper, LOCAL as CONSTANT, Widget as default };
