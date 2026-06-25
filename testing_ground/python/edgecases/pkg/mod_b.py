"""Consumer module exercising aliased and relative imports.

IDEAL outcomes:
- ``from . import mod_a`` -> an imports_module edge mod_b -> mod_a.
- ``provide as make_svc`` -> calls resolve to mod_a.provide.
- ``Service as Svc`` -> annotations using Svc resolve to mod_a.Service.
"""
from . import mod_a
from .mod_a import provide as make_svc
from .mod_a import Service as Svc


def via_relative() -> "mod_a.Service":
    """IDEAL: mod_a.provide() resolves through the relative module import."""
    return mod_a.provide()


def via_alias() -> "Svc":
    """IDEAL: the aliased make_svc() resolves to mod_a.provide; Svc -> Service."""
    s = make_svc()
    return s
