"""table built-in formatter — hand-rolled aligned columns (zero deps).

Mirrors the legacy ``_render_table`` in sdk/py/hop_top_kit/output.py:
- 2-space gap between columns
- header + data rows, each terminated with '\\n'
- zero rows → no output (not even a bare header row)
- ``columns`` sets header order/names; ``cols`` reorders + selects
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Any, TextIO

from hop_top_kit.output.formatter import OptionSpec
from hop_top_kit.output.projection import filter_columns, to_rows

if TYPE_CHECKING:
    from hop_top_kit.output.formatter import ColumnSpec

_GAP = "  "


class TableFormatter:
    key = "table"
    extensions: tuple[str, ...] = ()

    def options(self) -> list[OptionSpec]:
        return []

    def render(
        self,
        out: TextIO,
        data: Any,
        opts: dict[str, Any],
        cols: list[str],
        columns: list[ColumnSpec] | None = None,
    ) -> None:
        headers, rows = to_rows(data, columns)
        # Emptiness is decided by ROW count, never header count: a
        # ColumnSpec list yields headers even for a zero-row payload.
        if not rows:
            return
        if cols:
            headers, rows = filter_columns(headers, rows, cols)

        col_widths = [len(h) for h in headers]
        for row in rows:
            for i, cell in enumerate(row):
                if len(cell) > col_widths[i]:
                    col_widths[i] = len(cell)

        def _fmt(cells: list[str]) -> str:
            padded = [cell.ljust(col_widths[i]) for i, cell in enumerate(cells)]
            return _GAP.join(padded).rstrip()

        out.write(_fmt(headers) + "\n")
        for row in rows:
            out.write(_fmt(row) + "\n")
