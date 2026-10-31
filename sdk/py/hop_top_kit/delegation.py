"""delegation — wires :class:`hop_top_kit.gate.Gate` into a Typer app.

``create_app`` hands every command resolved through the root to
:func:`install`, which wraps each leaf callback so the gate runs before the
handler. Flag precedence follows Go's ``flagValue``: a leaf that declares
its own ``--confirm`` or ``--max-ops`` supplies the value the gate reads
(even when unset on that run); ``--policy`` and ``--dry-run`` are the
root's only, so a leaf-local ``--policy`` is never the delegation policy.
``--confirm-token`` is the one kit-added leaf flag, put on leaves annotated
``kit/destructive-token: required``.
"""

from __future__ import annotations

import sys
from typing import Any

import typer
import typer.core

from hop_top_kit import gate as _gate
from hop_top_kit import sideeffect
from hop_top_kit.output.error import CLIError, render_error

#: Root children kit owns: help and completion dispatch no handler of the author's.
BUILTIN_ROOT_COMMANDS = frozenset({"help", "completion"})

#: Names the root registers for the delegation globals; reserved.
RESERVED_GLOBALS = frozenset({"confirm", "max-ops", "policy", "dry-run"})

_TOKEN_META = "kit.confirm_token"
_GATED_ATTR = "_kit_gated"


def install(cmd: Any, name: str, gate: _gate.Gate) -> None:
    """Gate every leaf under ``cmd`` (registered at the root as ``name``)."""
    if name in BUILTIN_ROOT_COMMANDS:
        return
    _walk(cmd, (name,), gate)


def _walk(cmd: Any, path: tuple[str, ...], gate: _gate.Gate) -> None:
    children = getattr(cmd, "commands", None)
    if children:
        for n, c in children.items():
            _walk(c, (*path, n), gate)
    elif cmd.callback is not None and not getattr(cmd, _GATED_ATTR, False):
        _wrap(cmd, path, gate)


def _wrap(cmd: Any, path: tuple[str, ...], gate: _gate.Gate) -> None:
    orig = cmd.callback
    ann = sideeffect.annotations_of(orig)
    own_token = any(p.name == "confirm_token" for p in cmd.params)
    if _gate.requires_token(ann) and not own_token:
        cmd.params.append(
            typer.core.TyperOption(
                param_decls=[f"--{_gate.CONFIRM_TOKEN_FLAG}"],
                help="Typed confirmation token printed by the refusal. "
                "Required for kit/destructive-token commands.",
                expose_value=False,
                callback=_stash_token,
            )
        )

    orig_invoke = cmd.invoke

    def invoke(ctx: Any) -> Any:
        leaf = _gate.Leaf(
            program=gate.program,
            path=path,
            annotations=ann,
            format=_format(ctx),
            confirm_token=str(ctx.params.get("confirm_token") or ctx.meta.get(_TOKEN_META) or ""),
        )
        try:
            return gate.run(leaf, _globals(ctx), lambda: orig_invoke(ctx))
        except CLIError as e:
            render_error(sys.stderr, leaf.format, e)
            raise typer.Exit(e.exit_code) from None

    cmd.invoke = invoke
    setattr(cmd, _GATED_ATTR, True)


def _stash_token(ctx: Any, param: Any, value: Any) -> Any:
    ctx.meta[_TOKEN_META] = value or ""
    return value


def _globals(ctx: Any) -> _gate.Globals:
    p = ctx.find_root().params
    # Go's flagValue: the leaf's own flag shadows the root's, set or not.
    confirm = ctx.params["confirm"] if "confirm" in ctx.params else p.get("confirm")
    max_ops = ctx.params["max_ops"] if "max_ops" in ctx.params else p.get("max_ops")
    return _gate.Globals(
        confirm=str(confirm or ""),
        max_ops=_int(max_ops),
        policy=str(p.get("policy") or ""),
        dry_run=bool(p.get("dry_run")),
    )


def _int(v: Any) -> int:
    """Go's flagInt: 0 when missing or unparseable."""
    try:
        return int(v or 0)
    except (TypeError, ValueError):
        return 0


def _format(ctx: Any) -> str:
    return str(ctx.params.get("_out_format") or ctx.find_root().params.get("format") or "")
