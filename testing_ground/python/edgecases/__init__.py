"""Aracne testing ground — Python edge-case subpackage.

Every module here is SELF-CONTAINED: it never imports from the parent
``testing_ground.python`` package (shapes/factory/consumer). This keeps these
files' topology edges independent of the at-scale mutation suite (which edits
those parent modules), preserving cross-mode equality.

Resource names are unique across the whole corpus so the base-class global
fallback (a bare base name resolving to its unique match across the topology)
never collides with the parent package.
"""
