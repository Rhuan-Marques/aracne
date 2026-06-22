// Base class for geometric shapes with name, area calculation, and description methods.

export class Shape {
// Initializes a Shape with a name property.
  constructor(name) {
    this.name = name;
  }
// Returns 0; base implementation to be overridden by subclasses.
  area() {
    return 0;
  }
// Returns a string description of the shape by name
  describe() {
    return `shape ${this.name}`;
  }
}

// Circle shape class that extends Shape with radius property, area calculation, diameter getter, scale setter, and unit factory method.
export class Circle extends Shape {
// Initializes a Circle with a given radius, calling parent Shape constructor with type "circle"
  constructor(radius) {
    super("circle");
    this.radius = radius;
  }
// Computes circle area as π times radius squared.
  area() {
    return Math.PI * this.radius * this.radius;
  }
// Getter that returns the circle's diameter as twice the radius
  get diameter() {
    return this.radius * 2;
  }
// Setter that scales the circle's radius by a given factor
  set scale(factor) {
    this.radius *= factor;
  }
// Static factory method that creates a Circle with radius 1
  static unit() {
    return new Circle(1);
  }
}

// Shape subclass representing a rectangle with width and height, providing an area method
export class Rectangle extends Shape {
// Initializes a Rectangle with width and height, calling parent constructor with "rectangle".
  constructor(width, height) {
    super("rectangle");
    this.width = width;
    this.height = height;
  }
// Calculates rectangle area as width times height.
  area() {
    return this.width * this.height;
  }
}

// Creates a square Rectangle with equal width and height
export default function makeSquare(side) {
  return new Rectangle(side, side);
}

// Named const export.
export const ORIGIN = { x: 0, y: 0 };
