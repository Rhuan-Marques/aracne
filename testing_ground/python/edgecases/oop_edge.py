"""OOP edge cases: static/classmethods, super(), nested classes, dunders,
__slots__, metaclasses, Enum/NamedTuple/TypedDict, dataclasses, Protocol
structural conformance, and diamond-MRO inheritance.

NOTE: the property getter/setter/deleter case lives in the ISOLATED test suite
because three methods sharing one name (e.g. ``celsius``) currently produce
duplicate resource IDs that can abort the whole-corpus write.
"""
from dataclasses import dataclass
from enum import Enum
from typing import NamedTuple, Protocol, TypedDict


class MathUtil:
    """Holds a @staticmethod and a @classmethod."""

    @staticmethod
    def square(x: float) -> float:
        return x * x

    @classmethod
    def make(cls) -> "MathUtil":
        return cls()


class Person:
    """Plain base class with an initializer and a method."""

    def __init__(self, name: str) -> None:
        self.name = name

    def greet(self) -> str:
        return self.name


class Employee(Person):
    """Subclass calling super().__init__ (IDEAL: calls Person.__init__)."""

    def __init__(self, name: str, salary: float) -> None:
        super().__init__(name)
        self.salary = salary


class Outer:
    """Hosts a NESTED class Outer.Inner."""

    class Inner:
        def ping(self) -> str:
            return "inner"

    def make_inner(self) -> "Outer.Inner":
        """IDEAL: Outer.Inner() -> uses_class of the nested Inner class."""
        return Outer.Inner()


class Vector:
    """Operator overloading through dunder methods."""

    def __init__(self, x: float) -> None:
        self.x = x

    def __add__(self, other: "Vector") -> "Vector":
        return Vector(self.x + other.x)

    def __getitem__(self, idx: int) -> float:
        return self.x

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Vector) and other.x == self.x


def use_operators(a: Vector, b: Vector) -> "Vector":
    """IDEAL: a + b -> calls Vector.__add__; a[0] -> calls Vector.__getitem__."""
    c = a + b
    _ = c[0]
    return c


class Slotted:
    """Defines __slots__ (IDEAL: parsed without crashing; slots recognized)."""

    __slots__ = ("x", "y")

    def __init__(self, x: int, y: int) -> None:
        self.x = x
        self.y = y


class UpperMeta(type):
    """A metaclass."""

    def __new__(mcls, name, bases, namespace):
        return super().__new__(mcls, name, bases, namespace)


class WithMeta(metaclass=UpperMeta):
    """IDEAL: metaclass UpperMeta recorded as a metaclass edge, not a base."""


class Color(Enum):
    """Enum with members (IDEAL: members modeled as enum values/vars)."""

    RED = 1
    GREEN = 2
    BLUE = 3


class PointNT(NamedTuple):
    """class-based NamedTuple (IDEAL: x and y fields modeled)."""

    x: int
    y: int


class Movie(TypedDict):
    """TypedDict (IDEAL: recognized as a type with typed fields)."""

    title: str
    year: int


@dataclass
class Account:
    """dataclass (IDEAL: a synthesized constructor is modeled)."""

    owner: str
    balance: float = 0.0


def open_account(owner: str) -> Account:
    """IDEAL: Account(owner) resolves to the dataclass-generated constructor."""
    return Account(owner)


class Renderable(Protocol):
    """A Protocol describing a render() method."""

    def render(self) -> str:
        ...


class Button:
    """Structurally conforms to Renderable WITHOUT inheriting it.

    IDEAL: an implements/implemented_by edge between Button and Renderable
    (structural typing). Python emits no implements edge, so this is a
    documented gap.
    """

    def render(self) -> str:
        return "button"


class DiamondTop:
    def who(self) -> str:
        return "top"


class DiamondLeft(DiamondTop):
    pass


class DiamondRight(DiamondTop):
    pass


class DiamondBottom(DiamondLeft, DiamondRight):
    """Diamond inheritance (IDEAL: inherits both mids; MRO bottom->left->right->top)."""
