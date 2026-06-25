"""Typing & annotation edge cases.

Self-contained: imports only from ``typing``. Exercises Optional / Union /
PEP 604 unions, subscripted generics, Callable, Literal, forward references,
Generic + TypeVar, PEP 695 generics and type aliases, an old-style alias, and
``typing.cast``.

The ``@overload`` case lives in the ISOLATED suite because the repeated function
name produces duplicate resource IDs that can abort the whole-corpus write.
"""
from typing import (
    Callable,
    Generic,
    Literal,
    Optional,
    TypeVar,
    Union,
    cast,
)

T = TypeVar("T")


class Alpha:
    """A plain class used as an annotation target."""

    def ping(self) -> str:
        return "alpha"


class Beta:
    """A second plain class used as an annotation target."""

    def pong(self) -> str:
        return "beta"


def takes_optional(x: Optional[Alpha]) -> None:
    """IDEAL: uses_class -> Alpha (Optional's inner type)."""
    ...


def takes_union(x: Union[Alpha, Beta]) -> None:
    """IDEAL: uses_class -> Alpha AND Beta."""
    ...


def takes_pep604(x: Alpha | Beta) -> None:
    """IDEAL: uses_class -> Alpha AND Beta (PEP 604 union syntax)."""
    ...


def takes_list(items: list[Alpha]) -> None:
    """IDEAL: uses_class -> Alpha (subscripted-generic element type)."""
    ...


def takes_dict(m: dict[str, Beta]) -> None:
    """IDEAL: uses_class -> Beta (dict value type)."""
    ...


def takes_callable(cb: Callable[[Alpha], Beta]) -> None:
    """IDEAL: uses_class -> Alpha AND Beta (Callable arg & return types)."""
    ...


def takes_literal(mode: Literal["a", "b"]) -> str:
    """IDEAL: no spurious uses_class edge for the literal string values."""
    return mode


def forward_ref(x: "Alpha") -> "Beta":
    """IDEAL: quoted forward-ref annotations resolve -> uses_class Alpha & Beta."""
    return Beta()


class Box(Generic[T]):
    """Generic container. IDEAL: T is a type parameter, not a uses_class edge."""

    def __init__(self, item: T) -> None:
        self.item = item

    def get(self) -> T:
        return self.item


class Stack[E]:
    """PEP 695 generic class. IDEAL: parses; E is a type parameter."""

    def push(self, e: E) -> None:
        ...


def first[E](items: list[E]) -> E:
    """PEP 695 generic function. IDEAL: parses cleanly; returns the element type."""
    return items[0]


type AlphaList = list[Alpha]


BetaList = list[Beta]


def use_alias(xs: AlphaList) -> None:
    """IDEAL: the AlphaList alias resolves transitively to Alpha."""
    ...


def use_cast(obj: object) -> "Alpha":
    """IDEAL: typing.cast(Alpha, obj) -> uses_class Alpha."""
    return cast(Alpha, obj)
