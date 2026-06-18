"""Aracne testing ground — Python package exercising topology edge cases.

This __init__ re-exports a few names to exercise package-level import edges.
"""
from .shapes import Shape, Circle, Rectangle, Drawable  # noqa: F401
