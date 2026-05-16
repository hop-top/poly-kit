"""text built-in formatter — kv / lines / paragraph styles.

Mirrors hop.top/kit/go/console/output/text.go byte-for-byte:

- ``style=kv`` (default): ``HEADER<sep>VALUE\\n`` per field; blank line
  between records.
- ``style=lines``: TSV — fields tab-joined; one record per line.
- ``style=paragraph``: ``Record N:\\n`` header + ``  HEADER: VALUE\\n``
  lines; blank line between records.

Empty list → no output. Honors ``cols``. Zero deps.
"""

from __future__ import annotations

from typing import Any, TextIO

from hop_top_kit.output.formatter import OptionSpec
from hop_top_kit.output.projection import filter_columns, to_rows

_STYLE_KV = "kv"
_STYLE_LINES = "lines"
_STYLE_PARAGRAPH = "paragraph"


class TextFormatter:
    key = "text"
    extensions: tuple[str, ...] = (".txt",)

    def options(self) -> list[OptionSpec]:
        return [
            OptionSpec(
                name="style",
                type="enum",
                default=_STYLE_KV,
                enum=(_STYLE_KV, _STYLE_LINES, _STYLE_PARAGRAPH),
                usage="output style",
            ),
            OptionSpec(
                name="separator",
                type="string",
                default="=",
                usage="kv separator (kv style only)",
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

        style = opts.get("style", _STYLE_KV) or _STYLE_KV
        if style == _STYLE_KV:
            sep = opts.get("separator", "=") or "="
            _render_kv(out, headers, rows, sep)
        elif style == _STYLE_LINES:
            _render_lines(out, rows)
        elif style == _STYLE_PARAGRAPH:
            _render_paragraph(out, headers, rows)
        else:
            raise ValueError(f"text formatter: unknown style {style!r}")


def _render_kv(out: TextIO, headers: list[str], rows: list[list[str]], sep: str) -> None:
    for i, row in enumerate(rows):
        if i > 0:
            out.write("\n")
        for h, cell in zip(headers, row, strict=False):
            out.write(f"{h}{sep}{cell}\n")


def _render_lines(out: TextIO, rows: list[list[str]]) -> None:
    for row in rows:
        out.write("\t".join(row) + "\n")


def _render_paragraph(out: TextIO, headers: list[str], rows: list[list[str]]) -> None:
    for i, row in enumerate(rows):
        if i > 0:
            out.write("\n")
        out.write(f"Record {i + 1}:\n")
        for h, cell in zip(headers, row, strict=False):
            out.write(f"  {h}: {cell}\n")
