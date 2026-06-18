"""Factory functions: cross-module type returns, async, and exotic signatures.

Edge cases exercised here:
- Functions whose return annotation is a class from another module (Circle,
  Rectangle, Point) — cross-module type returns.
- An ``async def`` that returns a Circle.
- ``*args`` typed as the Shape ABC.
- positional-only (``/``), keyword-only (``*``), default, and ``**kwargs``
  parameters all in one signature.
"""
from .shapes import Circle, Rectangle, Shape, Point


def make_circle(radius: float) -> Circle:
    """Return a Circle (cross-module type return)."""
    return Circle(radius)


def make_rectangle(width: float, height: float) -> Rectangle:
    """Return a Rectangle (cross-module type return)."""
    return Rectangle(width, height)


def total_area(*shapes: Shape) -> float:
    """``*args`` of Shape; sums area() over each element."""
    return sum(shape.area() for shape in shapes)


def describe_all(shapes_list, sep: str = ", ", /, *, upper: bool = False, **opts) -> str:
    """Mix of positional-only (shapes_list, sep), keyword-only (upper), and **kwargs."""
    parts = [shape.describe() for shape in shapes_list]
    text = sep.join(parts)
    if opts.get("reverse"):
        text = text[::-1]
    return text.upper() if upper else text


async def fetch_circle(radius: float) -> Circle:
    """An async factory returning a Circle (cross-module type return)."""
    return Circle(radius)


def origin() -> Point:
    """Return a Point dataclass instance."""
    return Point(0.0, 0.0)
