"""
hop_top_kit.output.projection — shared row extraction for built-in formatters.

Mirrors Go's tableColumns/projection helpers. Supports four shapes for
``data``:

- list of dict
- list of dataclass instances
- single dict
- single dataclass instance

Returns ``(headers, rows)`` where headers is ``list[str]`` and rows is
``list[list[str]]`` (cells stringified). An empty list returns
``([], [])``.

filter_columns(headers, rows, cols) projects to the requested column
subset preserving order; raises ValueError on unknown column.
"""

from __future__ import annotations

import dataclasses
from typing import Any


def to_rows(v: Any) -> tuple[list[str], list[list[str]]]:
    """Return ``(headers, rows)`` for *v*. See module docstring for shapes."""
    if isinstance(v, list):
        if not v:
            return [], []
        first = v[0]
        if dataclasses.is_dataclass(first) and not isinstance(first, type):
            headers = [f.name for f in dataclasses.fields(first)]
            rows = [[str(getattr(item, h)) for h in headers] for item in v]
            return headers, rows
        # list of dict-like
        headers = list(first.keys())
        rows = [[str(item[h]) for h in headers] for item in v]
        return headers, rows

    if dataclasses.is_dataclass(v) and not isinstance(v, type):
        headers = [f.name for f in dataclasses.fields(v)]
        return headers, [[str(getattr(v, h)) for h in headers]]

    if isinstance(v, dict):
        headers = list(v.keys())
        return headers, [[str(v[h]) for h in headers]]

    raise TypeError(f"render: unsupported type {type(v)!r}")


def filter_columns(
    headers: list[str],
    rows: list[list[str]],
    cols: list[str],
) -> tuple[list[str], list[list[str]]]:
    """Project headers + rows to the requested *cols* preserving cols order.

    Raises ValueError listing the offending name + valid set when *cols*
    contains an unknown column. ``cols=[]`` returns inputs unchanged.
    """
    if not cols:
        return headers, rows
    have = {h: i for i, h in enumerate(headers)}
    indices: list[int] = []
    new_headers: list[str] = []
    for c in cols:
        if c not in have:
            valid = ", ".join(headers)
            raise ValueError(f"unknown column {c!r} (valid: {valid})")
        indices.append(have[c])
        new_headers.append(c)
    new_rows = [[row[i] for i in indices] for row in rows]
    return new_headers, new_rows
