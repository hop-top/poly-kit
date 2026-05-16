"""Flag registration helpers for SetFlag/TextFlag with display config.

Registers Click options with prefix (+/-/=) and/or verbose (--add-X)
forms. Developer controls which appear in help; all forms always parse.
"""

from __future__ import annotations

from enum import Enum

import click

from .setflag import SetFlag
from .textflag import TextFlag


class FlagDisplay(Enum):
    PREFIX = "prefix"
    VERBOSE = "verbose"
    BOTH = "both"


def register_set_flag(
    cmd: click.BaseCommand,
    name: str,
    usage: str,
    display: FlagDisplay = FlagDisplay.PREFIX,
) -> SetFlag:
    """Register a set-valued flag on a Click command."""
    sf = SetFlag()

    show_prefix = display in (FlagDisplay.PREFIX, FlagDisplay.BOTH)
    show_verbose = display in (FlagDisplay.VERBOSE, FlagDisplay.BOTH)

    if show_prefix:
        cmd.params.append(
            click.Option(
                [f"--{name}"],
                multiple=True,
                help=f"{usage} (+add, -remove, =replace)",
                callback=lambda ctx, param, vals: _apply_set_multi(sf, vals),
                expose_value=False,
                is_eager=False,
            )
        )

    if show_verbose:
        cmd.params.append(
            click.Option(
                [f"--add-{name}"],
                multiple=True,
                help=f"Add to {usage}",
                callback=lambda ctx, param, vals: _apply_add(sf, vals),
                expose_value=False,
                is_eager=False,
            )
        )
        cmd.params.append(
            click.Option(
                [f"--remove-{name}"],
                multiple=True,
                help=f"Remove from {usage}",
                callback=lambda ctx, param, vals: _apply_remove(sf, vals),
                expose_value=False,
                is_eager=False,
            )
        )
        cmd.params.append(
            click.Option(
                [f"--clear-{name}"],
                is_flag=True,
                default=False,
                help=f"Clear all {usage}",
                callback=lambda ctx, param, val: sf.clear() if val else None,
                expose_value=False,
                is_eager=False,
            )
        )

    return sf


def register_text_flag(
    cmd: click.BaseCommand,
    name: str,
    usage: str,
    display: FlagDisplay = FlagDisplay.PREFIX,
) -> TextFlag:
    """Register a text-valued flag on a Click command."""
    tf = TextFlag()

    cmd.params.append(
        click.Option(
            [f"--{name}"],
            multiple=True,
            help=usage,
            callback=lambda ctx, param, vals: _apply_text_multi(tf, vals),
            expose_value=False,
            is_eager=False,
        )
    )

    show_verbose = display in (FlagDisplay.VERBOSE, FlagDisplay.BOTH)

    if show_verbose:
        cmd.params.append(
            click.Option(
                [f"--{name}-append"],
                help=f"Append to {usage} (new line)",
                callback=lambda ctx, param, val: tf.append(val) if val else None,
                expose_value=False,
                is_eager=False,
            )
        )
        cmd.params.append(
            click.Option(
                [f"--{name}-append-inline"],
                help=f"Append to {usage} (inline)",
                callback=lambda ctx, param, val: tf.append_inline(val) if val else None,
                expose_value=False,
                is_eager=False,
            )
        )
        cmd.params.append(
            click.Option(
                [f"--{name}-prepend"],
                help=f"Prepend to {usage} (new line)",
                callback=lambda ctx, param, val: tf.prepend(val) if val else None,
                expose_value=False,
                is_eager=False,
            )
        )
        cmd.params.append(
            click.Option(
                [f"--{name}-prepend-inline"],
                help=f"Prepend to {usage} (inline)",
                callback=lambda ctx, param, val: tf.prepend_inline(val) if val else None,
                expose_value=False,
                is_eager=False,
            )
        )

    return tf


def _apply_set_multi(sf: SetFlag, vals: tuple[str, ...]) -> None:
    for v in vals:
        sf.set(v)


def _apply_add(sf: SetFlag, vals: tuple[str, ...]) -> None:
    for v in vals:
        sf.add(v)


def _apply_remove(sf: SetFlag, vals: tuple[str, ...]) -> None:
    for v in vals:
        sf.remove(v)


def _apply_text_multi(tf: TextFlag, vals: tuple[str, ...]) -> None:
    for v in vals:
        tf.set(v)
