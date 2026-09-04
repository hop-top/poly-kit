"""
hop_top_kit.output.dispatch — Typer-aware render orchestrator.

Reads output flag values from ``ctx.obj`` (populated by
``register_output_flags``), resolves format/options/cols/output, and
calls the active Formatter.

Resolution order (mirrors Go output.Dispatch):

1. Resolve writer: ``--output`` empty / ``-`` → stdout, else open file.
2. ``--format-help`` short-circuit before format resolution.
3. Resolve format: explicit ``--format`` wins; else ``--output`` ext;
   else default ``"table"``.
4. Mismatch detection: explicit ``--format`` + ``--output`` ext that maps
   to a different formatter raises ``typer.BadParameter``.
5. ``--template`` escape hatch (mutually exclusive with ``--cols``).
6. ``parse_options`` against active formatter; validate cols against
   ``columns`` schema; ``Formatter.render``.

Column ordering contract:

- A ``columns`` (``list[ColumnSpec]``) argument drives default column
  order and header names, in list order — the same rule the ``--template``
  path already applies. Payload key order is the fallback used only when
  no ColumnSpec list is supplied.
- ``--cols`` reorders as well as selects: the user's sequence wins.
- ``header == key``; validation and value lookup are one operation on one
  name, so ``--cols`` is validated against exactly the header list the
  formatters project against.
"""

from __future__ import annotations

import inspect
import os
import sys
from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import dataclass, field
from functools import cache
from pathlib import Path
from typing import Any, TextIO

import typer

from hop_top_kit.output.format_help import render_format_help
from hop_top_kit.output.formatter import ColumnSpec, parse_options
from hop_top_kit.output.registry import Registry, default_registry

# Sentinel + default
_STDOUT_SENTINEL = "-"
_DEFAULT_FORMAT = "table"


@dataclass
class OutputFlags:
    """Holds parsed output-flag values stashed in ``ctx.obj``.

    Populated by ``register_output_flags``'s callback. The presence of a
    field matters when distinguishing "user passed --format=table" from
    "default" (extension inference rules); ``format_explicit`` tracks
    that.
    """

    format: str = ""
    format_explicit: bool = False
    format_opt: list[str] = field(default_factory=list)
    format_help: bool = False
    cols: list[str] = field(default_factory=list)
    template: str | None = None
    output: str = ""
    registry: Registry | None = None  # None = use default_registry


# ---------------------------------------------------------------------------
# resolve_writer
# ---------------------------------------------------------------------------


@contextmanager
def resolve_writer(path: str) -> Iterator[TextIO]:
    """Yield a TextIO writer for *path*.

    ``""`` and ``"-"`` both yield ``sys.stdout`` (no close). Otherwise
    opens *path* with mode ``"w"`` (truncates) and closes it on exit.
    """
    if not path or path == _STDOUT_SENTINEL:
        yield sys.stdout
        return
    p = Path(path)
    if p.exists() and p.is_dir():
        raise OSError(f"output path {str(p)!r} is a directory")
    f = p.open("w", encoding="utf-8")
    try:
        yield f
    finally:
        f.close()


# ---------------------------------------------------------------------------
# dispatch
# ---------------------------------------------------------------------------


def dispatch(
    ctx: typer.Context,
    data: Any,
    columns: list[ColumnSpec] | None = None,
) -> None:
    """Render *data* via the active Formatter using flags from *ctx*.

    *columns* is the row schema: it sets the default column order and the
    header names, and is the set ``--cols`` is validated against. It is
    forwarded to the Formatter so the rendered order matches. When None or
    empty, column order falls back to the payload's own key order and
    ``--cols`` is validated by the Formatter against those keys.
    """
    flags = _flags_from_ctx(ctx)
    registry = flags.registry or default_registry

    with resolve_writer(flags.output) as writer:
        # 2. --format-help short-circuit.
        if flags.format_help:
            scope_key = flags.format if flags.format_explicit else ""
            try:
                render_format_help(writer, registry, scope_key)
            except ValueError as exc:
                raise typer.BadParameter(str(exc)) from exc
            raise typer.Exit(0)

        # 3 + 4. Format resolution.
        format_key = _resolve_format(flags, registry)

        # 5. Template escape hatch.
        if flags.template is not None and flags.template != "":
            if flags.cols:
                raise typer.BadParameter("--template and --cols are mutually exclusive")
            _render_template(writer, flags.template, data, columns)
            return

        # 6. Formatter render.
        formatter = registry.lookup(format_key)
        if formatter is None:
            valid = ", ".join(registry.keys())
            raise typer.BadParameter(f"unknown output format {format_key!r} (valid: {valid})")
        try:
            opts = parse_options(flags.format_opt, formatter.options())
        except ValueError as exc:
            raise typer.BadParameter(str(exc)) from exc

        cols = list(flags.cols)
        if cols and columns:
            _validate_cols(cols, columns)

        try:
            _render(formatter, writer, data, opts, cols, columns)
        except ValueError as exc:
            raise typer.BadParameter(str(exc)) from exc


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------


def _flags_from_ctx(ctx: typer.Context) -> OutputFlags:
    obj = ctx.obj
    if isinstance(obj, OutputFlags):
        return obj
    if isinstance(obj, dict) and "output_flags" in obj:
        return obj["output_flags"]  # type: ignore[no-any-return]
    return OutputFlags()


def _resolve_format(flags: OutputFlags, registry: Registry) -> str:
    fmt = flags.format or _DEFAULT_FORMAT
    path = flags.output
    if not path or path == _STDOUT_SENTINEL:
        return fmt
    ext = os.path.splitext(path)[1].lower()
    if not ext:
        return fmt
    em = registry.extension_map()
    mapped = em.get(ext)
    if not mapped:
        return fmt
    if not flags.format_explicit:
        return mapped
    if mapped != fmt:
        primary = _primary_ext(registry, fmt) or "." + fmt
        raise typer.BadParameter(
            f"format {fmt!r} does not match output extension {ext!r} "
            f"(use -o file{primary} or --format {mapped})"
        )
    return fmt


def _primary_ext(registry: Registry, key: str) -> str:
    f = registry.lookup(key)
    if f is None or not f.extensions:
        return ""
    return f.extensions[0]


def _render(
    formatter: Any,
    writer: TextIO,
    data: Any,
    opts: dict[str, Any],
    cols: list[str],
    columns: list[ColumnSpec] | None,
) -> None:
    """Invoke ``formatter.render``, forwarding *columns* when it is accepted.

    Adopters predating the ColumnSpec-ordering contract implement the
    four-argument ``render(out, data, opts, cols)``; calling those with a
    fifth argument would break them, so the signature decides.
    """
    if _accepts_columns(type(formatter).render):
        formatter.render(writer, data, opts, cols, columns)
        return
    formatter.render(writer, data, opts, cols)


@cache
def _accepts_columns(render: Any) -> bool:
    try:
        params = inspect.signature(render).parameters
    except (TypeError, ValueError):  # pragma: no cover - exotic callables
        return False
    if "columns" in params:
        return True
    return any(p.kind is inspect.Parameter.VAR_KEYWORD for p in params.values())


def _validate_cols(cols: list[str], schema: list[ColumnSpec]) -> None:
    """Reject a ``--cols`` name absent from *schema*.

    Single source of truth: *schema* headers are exactly the header list the
    formatters project against, so a name accepted here cannot fail again
    inside ``filter_columns`` mid-render. The valid set is listed in schema
    order to match the formatter-side message.
    """
    headers = [c.header for c in schema]
    have = set(headers)
    for c in cols:
        if c not in have:
            valid = ", ".join(headers)
            raise typer.BadParameter(f"unknown column {c!r} (valid: {valid})")


def _render_template(
    writer: TextIO,
    template_src: str,
    data: Any,
    columns: list[ColumnSpec] | None,
) -> None:
    """Render *template_src* via Jinja2 with ``items`` + ``cols`` context."""
    try:
        import jinja2
    except ImportError as exc:  # pragma: no cover
        raise typer.BadParameter("Jinja2 is required for --template (pip install Jinja2)") from exc

    items = _to_items(data)
    cols = [c.header for c in columns] if columns else _infer_cols(items)
    try:
        tmpl = jinja2.Template(template_src, autoescape=False)
    except jinja2.TemplateError as exc:
        raise typer.BadParameter(f"parse template: {exc}") from exc
    try:
        writer.write(tmpl.render(items=items, cols=cols, data=data))
    except jinja2.TemplateError as exc:
        raise typer.BadParameter(f"execute template: {exc}") from exc


def _to_items(data: Any) -> list[dict[str, Any]]:
    """Best-effort coercion of *data* into a list of dicts for templating."""
    import dataclasses

    if dataclasses.is_dataclass(data) and not isinstance(data, type):
        return [dataclasses.asdict(data)]
    if isinstance(data, dict):
        return [data]
    if isinstance(data, list):
        out: list[dict[str, Any]] = []
        for item in data:
            if dataclasses.is_dataclass(item) and not isinstance(item, type):
                out.append(dataclasses.asdict(item))
            elif isinstance(item, dict):
                out.append(item)
            else:
                out.append({"value": item})
        return out
    return [{"value": data}]


def _infer_cols(items: list[dict[str, Any]]) -> list[str]:
    if not items:
        return []
    return list(items[0].keys())


__all__ = ["OutputFlags", "dispatch", "resolve_writer"]
