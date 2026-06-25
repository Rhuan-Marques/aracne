"""Scope edge cases: lambdas, comprehensions, generator expressions, the walrus
operator, and nested functions/closures.

Calls made inside lambdas and comprehensions are the interesting part: the
parser walks function bodies but skips nested scopes, so these calls may be
dropped (documented gaps).
"""


def helper(x: int) -> int:
    """Leaf function the scope cases call into."""
    return x * 2


class Widget:
    """Leaf class the comprehension cases instantiate."""

    def __init__(self, n: int) -> None:
        self.n = n


def uses_lambda(xs: list[int]) -> list[int]:
    """IDEAL: helper() called inside the lambda -> calls helper."""
    fn = lambda v: helper(v)
    return [fn(x) for x in xs]


def uses_listcomp(xs: list[int]) -> list[int]:
    """IDEAL: helper() inside a list comprehension -> calls helper."""
    return [helper(x) for x in xs]


def uses_dictcomp(xs: list[int]) -> dict[int, int]:
    """IDEAL: helper() inside a dict comprehension -> calls helper."""
    return {x: helper(x) for x in xs}


def uses_setcomp(xs: list[int]) -> set[int]:
    """IDEAL: helper() inside a set comprehension -> calls helper."""
    return {helper(x) for x in xs}


def uses_genexp(xs: list[int]) -> int:
    """IDEAL: helper() inside a generator expression -> calls helper."""
    return sum(helper(x) for x in xs)


def comp_instantiates(xs: list[int]) -> list[Widget]:
    """IDEAL: Widget(...) inside a comprehension -> uses_class Widget."""
    return [Widget(x) for x in xs]


def uses_walrus(xs: list[int]) -> int:
    """IDEAL: the walrus-bound call helper() -> calls helper."""
    total = 0
    while (n := helper(total)) < 100:
        total = n
    return total


def outer_closure() -> int:
    """IDEAL: the nested inner()'s call to helper is NOT attributed to
    outer_closure (it belongs to the nested scope)."""
    captured = 10

    def inner() -> int:
        return helper(captured)

    return inner()
