"""Decorator edge cases: custom wrapping decorators, decorator factories,
attribute-access decorators, stacking, and class decorators.
"""
import functools


def trace(fn):
    """A custom wrapping decorator."""

    @functools.wraps(fn)
    def wrapper(*args, **kwargs):
        return fn(*args, **kwargs)

    return wrapper


def repeat(times: int):
    """A decorator factory that takes arguments."""

    def deco(fn):
        return fn

    return deco


class Registry:
    """Holds a method used as an attribute decorator."""

    def register(self, fn):
        return fn


registry = Registry()


@trace
def traced_fn() -> int:
    """IDEAL: decorated by trace -> an edge (calls/uses) to trace."""
    return 1


@repeat(3)
def repeated_fn() -> int:
    """IDEAL: decorator factory repeat(3) resolved -> calls repeat."""
    return 2


@registry.register
def registered_fn() -> int:
    """IDEAL: attribute decorator registry.register -> uses_extvar registry
    (or calls Registry.register)."""
    return 3


@functools.lru_cache
def cached_fn(n: int) -> int:
    """IDEAL: functools dependency recorded -> uses_dependency functools."""
    return n


@trace
@repeat(2)
def stacked_fn() -> int:
    """IDEAL: BOTH stacked decorators (trace and repeat) recorded."""
    return 4


def decorate_class(cls):
    """A class decorator."""
    return cls


@decorate_class
class Decorated:
    """IDEAL: class decorator decorate_class recorded -> calls/uses edge."""
