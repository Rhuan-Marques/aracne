// EDGE / DUPLICATE-ID PROBE: declaration merging across kinds — function +
// namespace, enum + namespace, and class + namespace (all sharing one name/ID).
// SHOULD: each name yields a single primary resource plus the namespace members,
// no abort. ACTUAL (observe live): possible duplicate-ID collision/abort, or the
// namespace silently overwriting the function/enum/class (or vice versa).
import { Circle } from "./shapes";

// Function + namespace merge.
export function widget(): Circle {
  return new Circle(1);
}
export namespace widget {
  export const version = 1;
}

// Enum + namespace merge.
export enum Mode {
  On,
  Off,
}
export namespace Mode {
  export function toggle(): Mode {
    return Mode.On;
  }
}

// Class + namespace merge.
export class Holder {
  value(): number {
    return 1;
  }
}
export namespace Holder {
  export const tag = "holder";
}
