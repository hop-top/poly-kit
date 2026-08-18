"""csv built-in formatter.

Encoding is hand-rolled rather than delegated to ``csv.writer``: that writer's
QUOTE_MINIMAL does not quote a field beginning with whitespace, which the
other runtimes do. A quoted field's bytes are preserved verbatim in both
line-ending modes and both quoting paths — RFC 4180 lists CR and LF as
separate alternatives inside the ``escaped`` production, so a bare CR between
quotes is legal, and the ``crlf`` option changes the record terminator and
nothing else.

Options:
- delimiter (string, ","): single-char field delimiter
- no-header (bool, False): omit header row
- quote-all (bool, False): wrap every field in double quotes
- crlf (bool, False): use CRLF line endings (default LF)

Empty list input → no output. Honors ``cols`` (filters headers + rows).
"""

from __future__ import annotations

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

        if not no_header:
            _write_row(out, headers, delim, eol)
        for row in rows:
            _write_row(out, row, delim, eol)


def _needs_quotes(field: str, delim: str) -> bool:
    r"""Quote iff the field holds the delimiter, a quote, LF or CR, or starts
    with unicode whitespace.

    Note the asymmetry: a LEADING space forces quoting, a trailing one does
    not. The stdlib writer's QUOTE_MINIMAL covers the delimiter, quote, CR and
    LF but NOT leading whitespace, so a value like ``" x"`` came back from a
    round-trip with its space intact only by luck of the reader's skipinitial-
    space default being off. ``\.`` alone on a line terminates a PostgreSQL
    COPY stream and is quoted defensively.
    """
    if field == "":
        return False
    if field == "\\.":
        return True
    if delim in field or '"' in field or "\n" in field or "\r" in field:
        return True
    return field[0].isspace()


def _write_row(out: TextIO, cells: list[str], delim: str, eol: str) -> None:
    """Emit one record terminated by ``eol``.

    Encoding is hand-rolled rather than delegated to ``csv.writer`` because
    that writer keys quoting off QUOTE_MINIMAL, which does not quote a leading
    space the way the other runtimes do. A quoted field's bytes pass through
    verbatim: RFC 4180 lists CR and LF as separate alternatives inside the
    ``escaped`` production, so a bare CR between quotes is legal, and W3C CSV
    on the Web states that line endings within escaped cells are not
    normalised. Only the record terminator varies with ``crlf``.
    """
    parts: list[str] = []
    for c in cells:
        if _needs_quotes(c, delim):
            # RFC 4180: an embedded quote is doubled. Everything else, CR and
            # LF included, is written through untouched.
            parts.append('"' + c.replace('"', '""') + '"')
        else:
            parts.append(c)
    out.write(delim.join(parts))
    out.write(eol)


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
