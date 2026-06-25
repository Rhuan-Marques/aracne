"""Control-flow edge cases: match/case statements (including class patterns) and
with-statements (single and nested context managers).
"""


class Handler:
    """A context-manager-like helper with a method."""

    def handle(self) -> str:
        return "ok"


def run_action(cmd: str) -> str:
    """Leaf function called from inside a match/case body."""
    return "action:" + cmd


class Shape3D:
    """Leaf class used as a class pattern in match/case."""


def dispatch(cmd: str) -> str:
    """IDEAL: a call inside a match/case body -> calls run_action."""
    match cmd:
        case "go":
            return run_action(cmd)
        case _:
            return "none"


def match_class(obj) -> str:
    """IDEAL: class pattern `case Shape3D()` -> uses_class Shape3D."""
    match obj:
        case Shape3D():
            return "3d"
        case _:
            return "other"


def use_with() -> str:
    """IDEAL: `with Handler() as h` -> uses_class Handler; h.handle() -> calls handle."""
    with Handler() as h:
        return h.handle()


def nested_with() -> str:
    """IDEAL: both context managers resolved -> uses_class Handler twice."""
    with Handler() as a, Handler() as b:
        return a.handle() + b.handle()
