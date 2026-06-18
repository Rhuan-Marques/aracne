// TypeScript classes: an ABSTRACT class implementing an interface, concrete
// subclasses, access modifiers (public/private/protected), a parameter property,
// a getter, a static method returning an enum, and a GENERIC class.
import { Shape, Drawable, Color } from "./models";

// Abstract class that IMPLEMENTS the Shape interface and declares an abstract
// method. `describe` is concrete; subclasses must supply `area`.
export abstract class Base implements Shape {
  protected name: string;
  constructor(name: string) {
    this.name = name;
  }
  abstract area(): number;
  describe(): string {
    return `shape ${this.name}`;
  }
}

// Circle EXTENDS Base. It uses a parameter property, a private helper method
// (resolved via `this`), a getter, and a static method returning an enum.
export class Circle extends Base {
  constructor(public radius: number) {
    super("circle");
  }
  area(): number {
    return Math.PI * this.helper() * this.helper();
  }
  private helper(): number {
    return this.radius;
  }
  get diameter(): number {
    return this.radius * 2;
  }
  static color(): Color {
    return Color.Red;
  }
}

// Rectangle implements the Shape interface DIRECTLY (private/protected params).
export class Rectangle implements Shape {
  constructor(private width: number, protected height: number) {}
  area(): number {
    return this.width * this.height;
  }
  describe(): string {
    return "rectangle";
  }
}

// Canvas is the SINGLE implementer of the Drawable interface.
export class Canvas implements Drawable {
  draw(): string {
    return "canvas";
  }
}

// Box is a GENERIC class.
export class Box<T> {
  private value: T;
  constructor(value: T) {
    this.value = value;
  }
  get(): T {
    return this.value;
  }
}
