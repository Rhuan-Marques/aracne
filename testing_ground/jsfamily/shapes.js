// ES module: a class hierarchy exercising `extends`, a getter, a setter, a
// static method, a default export, and named exports.

export class Shape {
  constructor(name) {
    this.name = name;
  }
  area() {
    return 0;
  }
  describe() {
    return `shape ${this.name}`;
  }
}

// Circle EXTENDS Shape and adds a getter, a setter, and a static method.
//
// NOTE: the getter and setter use DIFFERENT property names on purpose. A getter
// and setter sharing one name (e.g. `get diameter` + `set diameter`) currently
// crashes the aracne write with a duplicate-connection error; see the
// testing-ground README and the filed bug report.
export class Circle extends Shape {
  constructor(radius) {
    super("circle");
    this.radius = radius;
  }
  area() {
    return Math.PI * this.radius * this.radius;
  }
  get diameter() {
    return this.radius * 2;
  }
  set scale(factor) {
    this.radius *= factor;
  }
  static unit() {
    return new Circle(1);
  }
}

// Rectangle also EXTENDS Shape.
export class Rectangle extends Shape {
  constructor(width, height) {
    super("rectangle");
    this.width = width;
    this.height = height;
  }
  area() {
    return this.width * this.height;
  }
}

// Default export: a function declaration.
export default function makeSquare(side) {
  return new Rectangle(side, side);
}

// Named const export.
export const ORIGIN = { x: 0, y: 0 };
