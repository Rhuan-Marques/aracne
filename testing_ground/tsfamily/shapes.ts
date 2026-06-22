// TypeScript classes: an ABSTRACT class implementing an interface, concrete
// subclasses, access modifiers (public/private/protected), a parameter property,
// a getter, a static method returning an enum, and a GENERIC class.
import { Shape, Drawable, Color } from "./models";

// Abstract base class implementing Shape interface with name property and area stub.
export abstract class Base implements Shape {
  protected name: string;
// Initializes Base with a shape name.
  constructor(name: string) {
    this.name = name;
  }
// Abstract method to calculate the area of a shape; implemented by subclasses.
  abstract area(): number;
// Returns a string describing the shape with its name
  describe(): string {
    return `shape ${this.name}`;
  }
}

// A circle shape with radius, area calculation, and static color method returning red
export class Circle extends Base {
// Initializes a circle with a given radius and calls parent constructor with "circle"
  constructor(public radius: number) {
    super("circle");
  }
// Calculates the area of a circle using π * radius²
  area(): number {
    return Math.PI * this.helper() * this.helper();
  }
// Private helper method that returns the circle's radius
  private helper(): number {
    return this.radius;
  }
// Getter that returns the circle's diameter (radius * 2)
  get diameter(): number {
    return this.radius * 2;
  }
// Static method returning the color red
  static color(): Color {
    return Color.Red;
  }
}

// Rectangle shape class implementing Shape interface with width and height properties
export class Rectangle implements Shape {
// Initializes rectangle with private width and protected height properties
  constructor(private width: number, protected height: number) {}
// Calculates rectangle area as width times height
  area(): number {
    return this.width * this.height;
  }
// Returns the string "rectangle" describing the shape type.
  describe(): string {
    return "rectangle";
  }
}

// Drawable implementation that returns the string "canvas"
export class Canvas implements Drawable {
// Returns the string "canvas"
  draw(): string {
    return "canvas";
  }
}

// Generic container class that wraps and retrieves a value of any type
export class Box<T> {
  private value: T;
// Initializes Box with a value of generic type T
  constructor(value: T) {
    this.value = value;
  }
// Returns the boxed value of generic type T
  get(): T {
    return this.value;
  }
}
