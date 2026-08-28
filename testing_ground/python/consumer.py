"""Consumer: cross-module wiring and method-call resolution edge cases.

Edge cases exercised here:
- A module-level variable INITIALIZED BY A CROSS-MODULE FUNCTION CALL.
- A parameter type hint that drives method-call resolution (render).
- Local instantiation then a method call (build_and_measure).
- Factory-return chained assignment then a method call (from_factory).
- A NESTED function whose calls must NOT be attributed to its parent (outer).
- A top-level function defined inside an ``if`` block (hoisted by the parser).
- A top-level function defined inside a ``try`` block (hoisted by the parser).

NOTE: this file deliberately AVOIDS assigning the same module-level name in two
sibling branches (e.g. ``FLAG`` in both ``try`` and ``except``). That pattern
currently triggers an aracne duplicate-connection crash; see the testing-ground
README and the filed bug report. Here the ``except`` branch only ``pass``es.
"""
from .factory import make_circle, total_area
from .shapes import Circle, Shape

# Module-level variable INITIALIZED BY A CROSS-MODULE FUNCTION CALL.
DEFAULT_CIRCLE = make_circle(1.0)


def render(shape: Shape) -> str:
    """The parameter is type-hinted as Shape; the method call resolves via it."""
    return shape.describe()


def measure_all(shapes_list) -> float:
    """Delegates to the variadic cross-module total_area."""
    return total_area(*shapes_list)


def build_and_measure() -> float:
    """Local instantiation then a method call: c = Circle(); c.area()."""
    c = Circle(2.0)
    return c.area()


def from_factory() -> float:
    """Factory-return chained assignment: c = make_circle(); c.area()."""
    c = make_circle(3.0)
    return c.area()


def outer() -> int:
    """Contains a NESTED function whose calls must NOT be attributed to outer."""

    def inner() -> float:
        return Circle(1.0).area()  # nested scope: not attributed to outer

    return int(inner())


if True:

    def conditionally_defined() -> str:
        """A top-level function defined inside an ``if`` block (hoisted)."""
        return "hoisted-from-if"


try:
    import json  # noqa: F401

    def serialize(shape: Shape) -> str:
        """A top-level function defined inside a ``try`` block (hoisted)."""
        return json.dumps({"desc": shape.describe()})

except ImportError:  # pragma: no cover
    pass
