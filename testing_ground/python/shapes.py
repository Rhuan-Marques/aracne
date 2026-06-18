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


class Shape(ABC):
    """Abstract base class (interface-like) with abstract methods."""

    @abstractmethod
    def area(self) -> float:
        ...

    @abstractmethod
    def describe(self) -> str:
        ...


class Drawable(Protocol):
    """A Protocol (structural interface) with a single conforming class."""

    def draw(self) -> str:
        ...


@dataclass
class Point:
    """A dataclass with annotated fields and defaults."""

    x: float = 0.0
    y: float = 0.0


class Circle(Shape):
    """Concrete subclass that IMPLEMENTS all abstract methods."""

    def __init__(self, radius: float) -> None:
        self.radius = radius

    def area(self) -> float:
        return PI * self.radius * self.radius

    def describe(self) -> str:
        return f"circle r={self.radius}"

    @property
    def diameter(self) -> float:
        return self.radius * 2

    @staticmethod
    def unit() -> "Circle":
        return Circle(1.0)

    @classmethod
    def from_point(cls, p: Point) -> "Circle":
        return cls(p.x)


class Rectangle(Shape):
    """Concrete subclass with POINTER-free implementation of the ABC."""

    def __init__(self, width: float, height: float) -> None:
        self.width = width
        self.height = height

    def area(self) -> float:
        return self.width * self.height

    def describe(self) -> str:
        return f"rect {self.width}x{self.height}"


class Square(Rectangle):
    """Single inheritance from a concrete class."""

    def __init__(self, side: float) -> None:
        super().__init__(side, side)


class IncompleteShape(Shape):
    """Subclass that does NOT implement all abstract methods (area is missing)."""

    def describe(self) -> str:
        return "incomplete"


class Named:
    """A mixin contributing one method."""

    def label(self) -> str:
        return "named"


class LabeledCircle(Circle, Named):
    """MULTIPLE inheritance: a concrete subclass plus a mixin."""

    def describe(self) -> str:
        return f"{self.label()} {super().describe()}"


class Canvas:
    """The single class conforming to the Drawable protocol."""

    def draw(self) -> str:
        return "canvas"
