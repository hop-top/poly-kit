"""
hop_top_kit.output.cli — register_output_flags helper for Typer apps.

Adds ``--format``, ``--format-opt``, ``--format-help``, ``--cols`` /
``--columns``, ``--template``, and ``--output`` (``-o``) so they can be
passed *after* the subcommand name (matching the Go cobra convention
``cli list --format json``).

Click/Typer don't auto-inherit root-callback options into subcommands,
so ``register_output_flags`` instead intercepts ``app.command`` and
``app.add_typer``: every subsequently-registered command gets the
output flags injected at the head of its parameter list, and a
post-callback parses them into ``OutputFlags`` on ``ctx.obj`` before
the user's body runs.

Adopters call once::

    register_output_flags(app)

    @app.command()
    def list_things(ctx: typer.Context) -> None:
        dispatch(ctx, items)
"""

from __future__ import annotations

import functools
import inspect
from collections.abc import Callable
from typing import Any, Optional

import typer

from hop_top_kit.output.dispatch import OutputFlags
from hop_top_kit.output.registry import Registry


def register_output_flags(
    app: typer.Typer,
    *,
    disable: dict[str, bool] | None = None,
    registry: Registry | None = None,
) -> None:
    """Patch *app* so every command picks up the output flag suite.

    Idempotent: a second call is a no-op (sentinel attr on the app).
    """
    if getattr(app, "_hop_top_output_flags_wired", False):
        return
    app._hop_top_output_flags_wired = True  # type: ignore[attr-defined]

    dis = disable or {}
    disable_output = bool(dis.get("output"))

    # Typer collapses a single-command app into the root when there is no
    # callback. We need the multi-command shape so flags can sit on each
    # subcommand. Install a no-op callback when the app doesn't have one.
    if not getattr(app, "registered_callback", None):

        @app.callback()
        def _output_flags_root() -> None:
            """Root callback (installed by register_output_flags)."""
            pass

    original_command = app.command

    @functools.wraps(original_command)
    def _patched_command(*cmd_args, **cmd_kwargs):
        decorator = original_command(*cmd_args, **cmd_kwargs)

        def _decorate(func: Callable[..., Any]) -> Callable[..., Any]:
            wrapped = _wrap_with_flags(func, registry, disable_output)
            return decorator(wrapped)

        return _decorate

    app.command = _patched_command  # type: ignore[assignment]


def _wrap_with_flags(
    func: Callable[..., Any],
    registry: Registry | None,
    disable_output: bool,
) -> Callable[..., Any]:
    """Return a wrapper that prepends output-flag params to *func*'s signature.

    The wrapper extracts the output flag values, builds an OutputFlags
    instance, stashes it on ctx.obj, then delegates to func with the
    original kwargs.
    """
    sig = inspect.signature(func)
    orig_params = list(sig.parameters.values())

    if "ctx" not in {p.name for p in orig_params}:
        # Inject ctx as the first parameter so dispatch can read it.
        ctx_param = inspect.Parameter(
            "ctx",
            inspect.Parameter.POSITIONAL_OR_KEYWORD,
            annotation=typer.Context,
        )
        orig_params = [ctx_param, *orig_params]
        injected_ctx = True
    else:
        injected_ctx = False

    # Drop output flags whose Click flag name collides with an option
    # already declared on the adopter's command (avoids Click's
    # "parameter X is used more than once" warning). Adopters that
    # define their own `--format` opt out of our injected one.
    user_flag_names = _collect_flag_names(orig_params)
    flag_params = [
        p for p in _build_flag_params(disable_output) if not _flag_collides(p, user_flag_names)
    ]

    # The wrapper takes **kwargs, so every parameter in the synthesized
    # signature must be KEYWORD_ONLY (otherwise the param ordering is
    # invalid: KEYWORD_ONLY cannot appear before POSITIONAL_OR_KEYWORD).
    # Promote ctx + adopter params to KEYWORD_ONLY accordingly.
    new_params: list[inspect.Parameter] = []
    seen_names: set[str] = set()
    flag_names = {p.name for p in flag_params}
    for p in orig_params:
        if p.name == "ctx":
            new_params.append(p.replace(kind=inspect.Parameter.KEYWORD_ONLY))
            seen_names.add("ctx")
            new_params.extend(flag_params)
            seen_names.update(flag_names)
            continue
        if p.name in seen_names:
            continue
        new_params.append(p.replace(kind=inspect.Parameter.KEYWORD_ONLY))
        seen_names.add(p.name)

    @functools.wraps(func)
    def wrapper(**kwargs: Any) -> Any:
        ctx = kwargs.get("ctx")
        flags = OutputFlags(
            format=kwargs.pop("_out_format", None) or "",
            format_explicit=kwargs.get("_out_format_explicit", False)
            or kwargs.pop("__format_was_set__", False),
            format_opt=list(kwargs.pop("_out_format_opt", None) or []),
            format_help=bool(kwargs.pop("_out_format_help", False)),
            cols=_split_cols(kwargs.pop("_out_cols", None) or []),
            template=kwargs.pop("_out_template", None),
            output=kwargs.pop("_out_output", "") or "",
            registry=registry,
        )
        # the explicit-flag scaffolding key — strip it before forwarding
        kwargs.pop("_out_format_explicit", None)
        # Detect explicit --format from typer.Option default sentinel.
        # We use None as default for --format; if user passed it, value is non-None.
        flags.format_explicit = bool(flags.format)
        if ctx is not None:
            ctx.obj = flags
        if injected_ctx:
            kwargs.pop("ctx", None)
        return func(**kwargs)

    wrapper.__signature__ = sig.replace(parameters=new_params)  # type: ignore[attr-defined]
    new_annotations = dict(func.__annotations__)
    new_annotations["ctx"] = typer.Context
    for p in flag_params:
        new_annotations[p.name] = p.annotation
    wrapper.__annotations__ = new_annotations
    return wrapper


def _build_flag_params(disable_output: bool) -> list[inspect.Parameter]:
    params: list[inspect.Parameter] = [
        inspect.Parameter(
            "_out_format",
            inspect.Parameter.KEYWORD_ONLY,
            default=typer.Option(
                None,
                "--format",
                help="Output format (use --format-help to list)",
            ),
            annotation=Optional[str],
        ),
        inspect.Parameter(
            "_out_format_opt",
            inspect.Parameter.KEYWORD_ONLY,
            default=typer.Option(
                None,
                "--format-opt",
                help="Per-format option as key=value (repeatable)",
            ),
            annotation=Optional[list[str]],
        ),
        inspect.Parameter(
            "_out_format_help",
            inspect.Parameter.KEYWORD_ONLY,
            default=typer.Option(
                False,
                "--format-help",
                help="Show available formats and their options",
            ),
            annotation=bool,
        ),
        inspect.Parameter(
            "_out_cols",
            inspect.Parameter.KEYWORD_ONLY,
            default=typer.Option(
                None,
                "--cols",
                "--columns",
                help="Restrict columns (comma-separated; repeatable)",
            ),
            annotation=Optional[list[str]],
        ),
        inspect.Parameter(
            "_out_template",
            inspect.Parameter.KEYWORD_ONLY,
            default=typer.Option(
                None,
                "--template",
                help="Jinja2 template applied to results (mutex with --cols)",
            ),
            annotation=Optional[str],
        ),
    ]
    if not disable_output:
        params.append(
            inspect.Parameter(
                "_out_output",
                inspect.Parameter.KEYWORD_ONLY,
                default=typer.Option(
                    "",
                    "--output",
                    "-o",
                    help="Write to path (use - or empty for stdout)",
                ),
                annotation=str,
            )
        )
    return params


def _collect_flag_names(params: list[inspect.Parameter]) -> set[str]:
    """Return CLI flag names (--foo) declared by *params*.

    Adopters often pass typer.Option(..., '--format', ...) as a default;
    we extract those declarations + add '--<name>' for params using bare
    type annotations (Typer auto-derives the flag name).
    """
    names: set[str] = set()
    for p in params:
        if p.name == "ctx":
            continue
        # Typer derives `--<name>` from the param name when the user
        # didn't pass an explicit option string.
        names.add("--" + p.name.replace("_", "-"))
        default = p.default
        # typer.Option / typer.Argument carries the explicit param decls.
        param_decls = getattr(default, "param_decls", None)
        if param_decls:
            for d in param_decls:
                if isinstance(d, str) and d.startswith("-"):
                    names.add(d)
    return names


def _flag_collides(param: inspect.Parameter, user_names: set[str]) -> bool:
    """True when *param*'s typer.Option declarations overlap *user_names*."""
    default = param.default
    decls = getattr(default, "param_decls", None) or ()
    return any(d in user_names for d in decls)


def _split_cols(values: list[str]) -> list[str]:
    """Comma-split + dedupe preserving first-seen order."""
    seen: set[str] = set()
    out: list[str] = []
    for v in values:
        for part in v.split(","):
            p = part.strip()
            if not p:
                continue
            if p in seen:
                continue
            seen.add(p)
            out.append(p)
    return out


__all__ = ["register_output_flags"]
