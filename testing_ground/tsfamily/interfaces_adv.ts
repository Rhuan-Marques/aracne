// EDGE: index / call / construct signatures, optional method, readonly property
// in interfaces; interface extends multiple; class implements multiple.
import { Circle } from "./shapes";

export interface Registry {
  [key: string]: unknown; // index signature
  find?(token: string): Circle; // optional method
  readonly size: number; // readonly property
}

export interface Builder {
  (token: string): Circle; // call signature
  new (r: number): Circle; // construct signature
}

export interface IA {
  a(): number;
}
export interface IB {
  b(): number;
}
// Interface extending MULTIPLE interfaces.
export interface IAB extends IA, IB {
  both(): number;
}

// Class implementing MULTIPLE interfaces.
export class Impl implements IA, IB {
  a(): number {
    return 1;
  }
  b(): number {
    return 2;
  }
}
