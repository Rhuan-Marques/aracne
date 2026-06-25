// EDGE: class / method / property / parameter decorators + decorator factories.
// SHOULD: decorators are captured as metadata without spurious edges; build()
// still resolves `new Circle`, and run()'s param-typed `dep.area()` resolves via
// the Circle annotation despite the parameter decorator.
import { Circle } from "./shapes";

function sealed(target: Function): void {}
function log(): MethodDecorator {
  return () => {};
}
function readonlyProp(target: object, key: string): void {}
function inject(target: object, key: string, index: number): void {}

@sealed
export class Service {
  @readonlyProp
  name = "svc";

  @log()
  build(r: number): Circle {
    return new Circle(r); // SHOULD: uses_class Circle despite the method decorator
  }

  run(@inject dep: Circle): number {
    return dep.area(); // SHOULD: param-type Circle drives -> Circle.area
  }
}
