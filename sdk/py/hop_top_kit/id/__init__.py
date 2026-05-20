"""hop_top_kit.id — TypeID primitive for the kit ecosystem.

Implements the cross-language kit API SHAPE from ADR 0001 on top of
the upstream ``typeid-python`` package. The wire form is the canonical
``<prefix>_<suffix>`` string (UUIDv7 backing, Crockford base32 suffix).

Public surface
==============

::

    from hop_top_kit.id import new, parse, Typed, TypeId, IdError

    tid = new("task")                   # "task_01J..." canonical str
    parsed = parse(tid)                 # Parsed(prefix="task", uuid=UUID(...))

    # Pydantic v2 annotation with runtime prefix-mismatch enforcement:
    from pydantic import BaseModel
    class Task(BaseModel):
        id: TypeId["task"]              # validates / round-trips str ↔ TypeID

URI composition (``tlc://task/task_01J...``) is **not** part of this
module. Use the ``hop-top-uri`` Py package directly with the canonical
string returned here.
"""

from __future__ import annotations

from ._core import (
    IdError,
    InvalidPrefixError,
    InvalidSuffixError,
    Parsed,
    PrefixMismatchError,
    Typed,
    new,
    parse,
)
from ._pydantic import TypeId

__all__ = (
    "IdError",
    "InvalidPrefixError",
    "InvalidSuffixError",
    "Parsed",
    "PrefixMismatchError",
    "TypeId",
    "Typed",
    "new",
    "parse",
)
