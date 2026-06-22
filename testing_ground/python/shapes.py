"""Shape hierarchy: ABCs, Protocols, dataclasses, inheritance, and decorators.

Edge cases exercised here:
- An ABC ``Shape`` with @abstractmethod methods (interface-like).
- A ``Protocol`` ``Drawable`` with a single conforming class.
- A @dataclass ``Point`` with annotated fields.
- Concrete subclasses that fully implement the ABC (Circle, Rectangle).
- A subclass that does NOT implement every abstract method (IncompleteShape).
- Single inheritance (Square) and MULTIPLE inheritance (LabeledCircle).
- @property, @staticmethod, and @classmethod members.
"""
from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import Protocol

PI = 3.14159  # module-level constant


// Abstract base class (interface-like) with abstract methods.
class Shape(ABC):
    """Abstract base class (interface-like) with abstract methods."""

    @abstractmethod
// Abstract method that returns the area of a shape.
    def area(self) -> float:
        ...

    @abstractmethod
// Abstract method that returns a string description of a shape.
    def describe(self) -> str:
        ...


// A Protocol (structural interface) with a single conforming class.
class Drawable(Protocol):
    """A Protocol (structural interface) with a single conforming class."""

// Interface method for drawing a shape (abstract).
    def draw(self) -> str:
        ...


@dataclass
// A dataclass with annotated fields and defaults.
class Point:
    """A dataclass with annotated fields and defaults."""

    x: float = 0.0
    y: float = 0.0


// Concrete subclass that IMPLEMENTS all abstract methods.
class Circle(Shape):
    """Concrete subclass that IMPLEMENTS all abstract methods."""

// Initializes a Circle with a given radius
    def __init__(self, radius: float) -> None:
        self.radius = radius

// Calculates the area of the circle using π × r²
    def area(self) -> float:
        return PI * self.radius * self.radius

// Returns a string representation of the circle with its radius.
    def describe(self) -> str:
        return f"circle r={self.radius}"

    @property
// Calculates and returns the diameter as twice the radius.
    def diameter(self) -> float:
        return self.radius * 2

    @staticmethod
// Static method that returns a unit Circle with radius 1.0.
    def unit() -> "Circle":
        return Circle(1.0)

    @classmethod
// Class method that creates a Circle from a Point using its x coordinate as the radius.
    def from_point(cls, p: Point) -> "Circle":
        return cls(p.x)


// Concrete subclass with POINTER-free implementation of the ABC.
class Rectangle(Shape):
    """Concrete subclass with POINTER-free implementation of the ABC."""

// Initializes width and height attributes from parameters
    def __init__(self, width: float, height: float) -> None:
        self.width = width
        self.height = height

// Returns the product of width and height
    def area(self) -> float:
        return self.width * self.height

// Returns a string representation of the rectangle with width and height.
    def describe(self) -> str:
        return f"rect {self.width}x{self.height}"


// Single inheritance from a concrete class.
class Square(Rectangle):
    """Single inheritance from a concrete class."""

// Initializes a square by calling the parent Rectangle constructor with equal width and height.
    def __init__(self, side: float) -> None:
        super().__init__(side, side)


// Subclass that does NOT implement all abstract methods (area is missing).
class IncompleteShape(Shape):
    """Subclass that does NOT implement all abstract methods (area is missing)."""

// Returns the literal string "incomplete"
    def describe(self) -> str:
        return "incomplete"


// A mixin contributing one method.
class Named:
    """A mixin contributing one method."""

// Returns the literal string "named"
    def label(self) -> str:
        return "named"


// MULTIPLE inheritance: a concrete subclass plus a mixin.
class LabeledCircle(Circle, Named):
    """MULTIPLE inheritance: a concrete subclass plus a mixin."""

// Returns label prefixed to the parent's description
    def describe(self) -> str:
        return f"{self.label()} {super().describe()}"


// The single class conforming to the Drawable protocol.
class Canvas:
    """The single class conforming to the Drawable protocol."""

// Returns the string "canvas"
    def draw(self) -> str:
        return "canvas"
