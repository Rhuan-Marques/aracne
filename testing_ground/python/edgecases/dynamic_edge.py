"""Dynamic / exotic edge cases: monkey-patching, setattr, functools.partial, and
module-level calls.
"""
import functools


class Plugin:
    """A class whose method gets monkey-patched at module load."""

    def run(self) -> str:
        return "run"


def external_method(self) -> str:
    """A free function assigned onto Plugin as a monkey-patch."""
    return "patched"


# Monkey-patching: bind a free function onto a class attribute.
Plugin.run = external_method  # IDEAL: an edge capturing the monkey-patch.


def helper_fn(a: int, b: int) -> int:
    """Leaf function referenced by a module-level call."""
    return a + b


def partial_target(a: int, b: int) -> int:
    """Leaf function referenced ONLY through functools.partial below."""
    return a * b


bound = functools.partial(partial_target, 1)


def set_attr_dynamic(p: Plugin) -> None:
    """IDEAL: dynamic setattr recognized (or, at minimum, no crash)."""
    setattr(p, "flag", True)


def module_level_caller() -> int:
    """Called at module scope below."""
    return helper_fn(2, 3)


RESULT = module_level_caller()  # IDEAL: module-level call recorded as an edge.
