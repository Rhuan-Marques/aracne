"""Subpackage exercising relative/aliased imports and __all__ re-exports."""
from .mod_a import Service

__all__ = ["Service"]
