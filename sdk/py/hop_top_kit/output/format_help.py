"""--format-help rendering — list formatters or one formatter's options."""

from __future__ import annotations

from typing import TextIO

from hop_top_kit.output.formatter import OptionSpec
from hop_top_kit.output.registry import Registry


def list_formats(r: Registry) -> list[dict[str, str]]:
    """Return one row per registered formatter, sorted by key.

    Each row has keys: ``key``, ``extensions``, ``options``.
    """
    out: list[dict[str, str]] = []
    for f in r.formatters():
        names = sorted(s.name for s in f.options())
        out.append(
            {
                "key": f.key,
                "extensions": ", ".join(f.extensions),
                "options": ", ".join(names),
            }
        )
    return out


def format_options(r: Registry, key: str) -> list[dict[str, str]]:
    """Return one OptionRow per OptionSpec on the formatter at *key*.

    Raises ValueError if *key* is unknown.
    """
    f = r.lookup(key)
    if f is None:
        valid = ", ".join(r.keys())
        raise ValueError(f"unknown format {key!r} (valid: {valid})")
    out: list[dict[str, str]] = []
    for s in f.options():
        out.append(
            {
                "name": s.name,
                "type": s.type,
                "default": "" if s.default is None else str(s.default),
                "enum": ", ".join(s.enum),
                "usage": s.usage,
            }
        )
    return out


def render_format_help(out: TextIO, r: Registry, key: str = "") -> None:
    """Write --format-help output via the table formatter for parity dogfood.

    *key* empty → list catalog. *key* set → that formatter's options.
    """
    from hop_top_kit.output.formatters.table_formatter import TableFormatter

    if key:
        rows = format_options(r, key)
        if not rows:
            out.write(f"format {key!r} has no options\n")
            return
        TableFormatter().render(out, rows, {}, [])
        return
    TableFormatter().render(out, list_formats(r), {}, [])


__all__ = ["OptionSpec", "format_options", "list_formats", "render_format_help"]
