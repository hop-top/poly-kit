"""The Click layer a command tree runs on.

typer>=0.26 dispatches through a vendored Click (``typer._click``) with its
own classes; earlier typer, and plain Click adopters, run on the ``click``
package. An object from one layer placed in the other layer's tree still
parses, but typer's help renderer and shell completion select by
``isinstance`` against their own layer and skip it. Kit helpers that build
Click objects take them from here so they match the tree they join.

The only module in the SDK that imports ``click``.
"""

from __future__ import annotations

from typing import Any

import click
import click.shell_completion
import typer.core

try:  # typer>=0.26: no public name for the vendored completion item
    from typer._click.shell_completion import CompletionItem as _TyperCompletionItem
except ImportError:  # typer<0.26 runs on the click package
    _TyperCompletionItem = click.shell_completion.CompletionItem


def make_option(cmd: Any, **kwargs: Any) -> Any:
    """Build an option for ``cmd`` from the Click layer ``cmd`` runs on.

    typer commands get ``typer.core.TyperOption`` (a ``click.Option`` on
    typer<0.26), plain Click commands get ``click.Option``.
    """
    typer_cmd = isinstance(cmd, (typer.core.TyperCommand, typer.core.TyperGroup))
    if isinstance(cmd, click.Command) and not typer_cmd:
        return click.Option(**kwargs)
    return typer.core.TyperOption(**kwargs)


def completion_item(ctx: Any, value: str, help: str | None = None) -> Any:
    """Build a completion item for the Click layer driving ``ctx``.

    A ``click.Context`` (plain Click, or typer<0.26) yields Click's item;
    anything else, including no context, yields typer's layer.
    """
    if isinstance(ctx, click.Context):
        return click.shell_completion.CompletionItem(value, help=help)
    return _TyperCompletionItem(value, help=help)
