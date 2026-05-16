"""hop_top_kit.output — formatter package.

Public surface
--------------
- ``Formatter`` Protocol + ``OptionSpec`` + ``ColumnSpec`` (T-1012)
- ``Registry``, ``default_registry``, ``new_registry`` (T-1013)
- Built-ins json/yaml/table register against ``default_registry`` at import
- ``render(w, format, v)`` legacy shim → ``default_registry.lookup``
- ``parse_options`` per-formatter option parsing helper

Extending built-ins
-------------------
Adopters register their own formatter via::

    from hop_top_kit.output import default_registry
    default_registry.register(MyFormatter())

Use ``override`` to intentionally replace a built-in.
"""

from __future__ import annotations

from typing import IO, Any, Literal

from hop_top_kit.output.dispatch import OutputFlags, dispatch, resolve_writer
from hop_top_kit.output.formatter import (
    ColumnSpec,
    Formatter,
    OptionSpec,
    parse_options,
)
from hop_top_kit.output.registry import (
    Registry,
    default_registry,
    new_registry,
)

__all__ = [
    "ColumnSpec",
    "Format",
    "Formatter",
    "OptionSpec",
    "OutputFlags",
    "Registry",
    "default_registry",
    "dispatch",
    "new_registry",
    "parse_options",
    "render",
    "resolve_writer",
]

# Format Literal extends as new built-ins register. Extending a Literal is
# a non-breaking change for Python typing — adopters using the narrower
# subset keep working.
Format = Literal["table", "json", "yaml", "csv", "text"]


def render(w: IO[str], format: str, v: Any) -> None:
    """Legacy shim — write *v* to *w* via ``default_registry.lookup(format)``.

    Preserves the original ``render(w, format, v)`` signature from
    sdk/py/hop_top_kit/output.py byte-for-byte. Migration to
    ``dispatch()`` is opt-in per adopter.

    Raises
    ------
    ValueError
        If *format* is not registered.
    """
    f = default_registry.lookup(format)
    if f is None:
        valid = ", ".join(default_registry.keys())
        raise ValueError(f"unknown output format {format!r} (valid: {valid})")
    f.render(w, v, {}, [])


# ---------------------------------------------------------------------------
# Register built-ins (json, yaml, table) at import time.
# csv + text register from their own modules in T-1015 / T-1016.
# ---------------------------------------------------------------------------


def _register_builtins() -> None:
    """Register the json/yaml/table/csv/text built-ins against default_registry.

    Idempotent: subsequent imports are no-ops because the keys already exist.
    """
    from hop_top_kit.output.formatters.csv_formatter import CSVFormatter
    from hop_top_kit.output.formatters.json_formatter import JSONFormatter
    from hop_top_kit.output.formatters.table_formatter import TableFormatter
    from hop_top_kit.output.formatters.text_formatter import TextFormatter
    from hop_top_kit.output.formatters.yaml_formatter import YAMLFormatter

    for f in (
        JSONFormatter(),
        YAMLFormatter(),
        TableFormatter(),
        CSVFormatter(),
        TextFormatter(),
    ):
        if default_registry.lookup(f.key) is None:
            default_registry.register(f)


_register_builtins()
