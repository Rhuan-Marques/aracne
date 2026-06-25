// EDGE: modern class features — instance field, static field, private #field,
// static initialization block, computed method name, async generator method,
// and a private method called intra-class.
// SHOULD: the class and its (named) methods are resources; private members and
// computed-name methods are handled without aborting the file's write, and
// reveal() `calls` the private #hidden method.
// ACTUAL (observe live): private #methods / computed-name methods may not be
// extracted; confirm whether reveal() -> #hidden resolves.
import { Circle } from "./shapes.js";

export class Featured extends Circle {
  label = "featured"; // instance field
  static kind = "circle"; // static field
  #secret = 99; // private field

  static {
    Featured.kind = "featured-circle"; // static initialization block
  }

  ["dynamic"]() {
    return this.#secret; // computed method name
  }

  async *stream(n) {
    for (let i = 0; i < n; i++) yield new Circle(i); // async generator method
  }

  #hidden() {
    return this.#secret; // private method
  }

  reveal() {
    return this.#hidden(); // intra-class call to a private method
  }
}
