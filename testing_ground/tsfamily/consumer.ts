// TypeScript consumer: a NAMESPACE block, a module-level const initialized by a
// cross-module call, an ALIASED re-export, and a DEFAULT export.
import { makeCircle, areaOf } from "./factory";
import { Shape } from "./models";
import { Circle } from "./shapes";

// Module-level const INITIALIZED BY A CROSS-MODULE CALL.
export const DEFAULT_AREA = areaOf(1);

// A namespace block with a nested exported function and const.
export namespace Geometry {
// Creates a circle with radius 1.
  export function unit(): Circle {
    return makeCircle(1);
  }
  export const NAME = "geometry";
}

// Returns a shape's description string.
function summarize(shape: Shape): string {
  return shape.describe();
}

// Aliased re-export of a local function.
export { summarize as describeShape };

// Computes the sum of a unit circle's area and a default area constant.
export default function main(): number {
  const u = Geometry.unit();
  return u.area() + DEFAULT_AREA;
}
