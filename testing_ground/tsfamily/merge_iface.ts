// EDGE / DUPLICATE-ID PROBE: TypeScript declaration merging — two interfaces
// with the same name merge, and a class + interface with the same name merge.
// Both reuse one resource ID; the second declaration can collide.
// SHOULD: a single merged Box2 interface and a single Combo (a class carrying
// the interface members), no abort. ACTUAL (observe live): possible duplicate-ID
// collision/abort, or one declaration silently overwriting the other.
export interface Box2 {
  width(): number;
}
export interface Box2 {
  height(): number; // merges into Box2
}

export class Combo {
  run(): number {
    return 1;
  }
}
export interface Combo {
  extra(): number; // merges members into class Combo
}
