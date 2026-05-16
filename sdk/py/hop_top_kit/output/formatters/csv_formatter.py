"""csv built-in formatter — stdlib csv.writer.

Mirrors hop.top/kit/go/console/output/csv.go semantics:
- delimiter (string, ","): single-char field delimiter
- no-header (bool, False): omit header row
- quote-all (bool, False): wrap every field in double quotes
- crlf (bool, False): use CRLF line endings (default LF)

Empty list input → no output. Honors ``cols`` (filters headers + rows).
"""

from __future__ import annotations

import csv as _csv
import io
from typing import Any, TextIO

from hop_top_kit.output.formatter import OptionSpec
from hop_top_kit.output.projection import filter_columns, to_rows


class CSVFormatter:
    key = "csv"
    extensions: tuple[str, ...] = (".csv",)

    def options(self) -> list[OptionSpec]:
        return [
            OptionSpec(
                name="delimiter",
                type="string",
                default=",",
                usage="field delimiter",
            ),
            OptionSpec(
                name="no-header",
                type="bool",
                default=False,
                usage="omit header row",
            ),
            OptionSpec(
                name="quote-all",
                type="bool",
                default=False,
                usage="quote every field, not just those needing it",
            ),
            OptionSpec(
                name="crlf",
                type="bool",
                default=False,
                usage="use CRLF line endings (default LF)",
            ),
        ]

    def render(
        self,
        out: TextIO,
        data: Any,
        opts: dict[str, Any],
        cols: list[str],
    ) -> None:
        headers, rows = to_rows(data)
        if not headers:
            return
        if cols:
            headers, rows = filter_columns(headers, rows, cols)

        delim = opts.get("delimiter", ",")
        if len(delim) != 1:
            raise ValueError("option 'delimiter': delimiter must be exactly one character")

        no_header = bool(opts.get("no-header", False))
        quote_all = bool(opts.get("quote-all", False))
        crlf = bool(opts.get("crlf", False))
        eol = "\r\n" if crlf else "\n"

        if quote_all:
            _write_quote_all(out, headers, rows, delim, eol, no_header)
            return

        # stdlib csv writes its own line terminator; we capture into a
        # buffer with newline='' so it doesn't translate, then re-emit
        # with our chosen eol.
        buf = io.StringIO(newline="")
        writer = _csv.writer(buf, delimiter=delim, lineterminator=eol)
        if not no_header:
            writer.writerow(headers)
        for row in rows:
            writer.writerow(row)
        out.write(buf.getvalue())


def _write_quote_all(
    out: TextIO,
    headers: list[str],
    rows: list[list[str]],
    delim: str,
    eol: str,
    no_header: bool,
) -> None:
    """Emit every field wrapped in double quotes, RFC4180 escape (`"` → `""`)."""

    def write_row(cells: list[str]) -> None:
        parts: list[str] = []
        for c in cells:
            escaped = c.replace('"', '""')
            parts.append(f'"{escaped}"')
        out.write(delim.join(parts))
        out.write(eol)

    if not no_header:
        write_row(headers)
    for row in rows:
        write_row(row)
