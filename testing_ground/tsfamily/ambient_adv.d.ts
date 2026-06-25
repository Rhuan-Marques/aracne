// EDGE: ambient MODULE declaration + GLOBAL augmentation.
// SHOULD: the ambient module's exported class/function are extracted (scoped to
// the module name) and the global augmentation does not crash the scanner.
declare module "virtual:shapes" {
  export class VirtualCircle {
    area(): number;
  }
  export function make(r: number): VirtualCircle;
}

declare global {
  interface Window {
    aracne: number;
  }
  function globalHelper(): number;
}

export {};
