// EDGE: nested namespaces, a namespace-scoped class used as a QUALIFIED type,
// and a qualified call into a nested namespace.
import { Circle } from "./shapes";

export namespace Geo {
  export class Point {
    constructor(public x: number, public y: number) {}
    dist(): number {
      return this.x;
    }
  }
  export function origin(): Point {
    return new Point(0, 0); // unqualified ref to a sibling class in the namespace
  }
}

// Qualified type annotation referencing a class inside a namespace.
export function useQualified(p: Geo.Point): number {
  return p.dist(); // SHOULD: Geo.Point qualified type resolves -> Point.dist; likely gap
}

// Nested namespaces with a qualified call into the inner namespace.
export namespace A {
  export namespace B {
    export function deep(): Circle {
      return new Circle(1);
    }
  }
  export function viaNested(): Circle {
    return B.deep(); // qualified call into the nested namespace
  }
}
