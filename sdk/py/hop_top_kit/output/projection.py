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

Column order:

- A ``columns`` (``list[ColumnSpec]``) argument drives header order and
  header names, in list order. A column the payload does not carry
  renders as an empty cell rather than an error.
- Payload key order is the fallback used only when no ``columns`` is
  supplied — Python dicts are insertion-ordered and dataclass fields keep
  declaration order, so the fallback is already stable.

filter_columns(headers, rows, cols) projects to the requested column
subset in the USER's order; ``cols`` reorders as well as selects. It
validates against *headers* — the same list to_rows produced from the
ColumnSpec — so validation and lookup share one source of truth.
"""

from __future__ import annotations

import dataclasses
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from hop_top_kit.output.formatter import ColumnSpec


def to_rows(
    v: Any,
    columns: list[ColumnSpec] | None = None,
) -> tuple[list[str], list[list[str]]]:
    """Return ``(headers, rows)`` for *v*. See module docstring for shapes.

    When *columns* is non-empty its order and names define the headers, and
    each row is projected onto them; a name the payload lacks yields ``""``.
    """
    if columns:
        return _to_rows_with_columns(v, columns)
    return _to_rows_inferred(v)


def _to_rows_with_columns(
    v: Any,
    columns: list[ColumnSpec],
) -> tuple[list[str], list[list[str]]]:
    headers = [c.header for c in columns]
    if isinstance(v, list):
        return headers, [_project(item, headers) for item in v]
    if dataclasses.is_dataclass(v) and not isinstance(v, type):
        return headers, [_project(v, headers)]
    if isinstance(v, dict):
        return headers, [_project(v, headers)]
    raise TypeError(f"render: unsupported type {type(v)!r}")


def _project(item: Any, headers: list[str]) -> list[str]:
    """Pull *headers* out of *item*; a missing name yields an empty cell."""
    if dataclasses.is_dataclass(item) and not isinstance(item, type):
        return [_cell(getattr(item, h, None)) for h in headers]
    if isinstance(item, dict):
        return [_cell(item.get(h)) for h in headers]
    raise TypeError(f"render: unsupported row type {type(item)!r}")


def _cell(value: Any) -> str:
    return "" if value is None else str(value)


def _to_rows_inferred(v: Any) -> tuple[list[str], list[list[str]]]:
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
    """Project headers + rows to the requested *cols*, in *cols* order.

    ``--cols`` reorders as well as selects: the user's sequence wins over the
    order in *headers*. Raises ValueError listing the offending name + valid
    set when *cols* contains a name absent from *headers*. ``cols=[]`` returns
    inputs unchanged.
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


def project_payload(data: Any, cols: list[str]) -> Any:
    """Project a raw payload onto *cols* for the structural formatters.

    Unlike to_rows/filter_columns this keeps values un-stringified, so json
    and yaml emit the original types. Key order follows *cols*. Non-mapping
    values and an empty *cols* pass through untouched.
    """
    if not cols:
        return data
    if isinstance(data, list):
        return [_project_value(item, cols) for item in data]
    return _project_value(data, cols)


def _project_value(row: Any, cols: list[str]) -> Any:
    if dataclasses.is_dataclass(row) and not isinstance(row, type):
        row = dataclasses.asdict(row)
    if not isinstance(row, dict):
        return row
    return {c: row.get(c) for c in cols}
