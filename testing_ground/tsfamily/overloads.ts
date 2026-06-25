// EDGE / DUPLICATE-ID PROBE: function overloads (2 signatures + 1 impl, all
// named `area`) and method overloads in a class. All overload nodes share one
// ID, which can produce a duplicate has_function / has_method connection that
// violates UNIQUE(source_id, conn_type, target_id) and aborts this file's
// topology write.
// SHOULD: one `area` function resource (+ one Calc.add method), no abort.
// ACTUAL (observe live): possible duplicate-ID abort or a doubled connection.
import { Circle } from "./shapes";

export function area(c: Circle): number;
export function area(n: number): number;
export function area(x: Circle | number): number {
  return typeof x === "number" ? x : x.area();
}

export class Calc {
  add(c: Circle): number;
  add(n: number): number;
  add(x: Circle | number): number {
    return typeof x === "number" ? x : x.area();
  }
}
