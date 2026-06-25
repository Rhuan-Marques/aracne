"""Provider module for cross-module import-resolution edge cases."""


class Service:
    """A class imported (and aliased) by sibling modules."""

    def serve(self) -> str:
        return "served"


def provide() -> Service:
    """Factory returning a Service (cross-module return type)."""
    return Service()


HELPER_CONST = 42
