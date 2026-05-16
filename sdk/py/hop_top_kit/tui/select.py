"""hop_top_kit.tui.select — interactive selection prompt."""

from __future__ import annotations

from dataclasses import dataclass

import questionary
from questionary import Style

from hop_top_kit.cli import Theme


@dataclass
class SelectItem:
    """A labeled choice for the select prompt."""

    label: str
    value: str


def select(
    theme: Theme,
    message: str,
    items: list[SelectItem],
    default: str | None = None,
) -> str:
    """Prompt user to pick one item from a list.

    Args:
        theme:   Theme supplying accent color for the pointer.
        message: Prompt text shown to the user.
        items:   Choices to display.
        default: Value of the pre-selected choice, or None for first item.

    Returns:
        The ``value`` of the selected item.

    Raises:
        KeyboardInterrupt: on Ctrl-C or if questionary returns None.
    """
    style = Style(
        [
            ("qmark", f"fg:{theme.accent} bold"),
            ("question", "bold"),
            ("answer", f"fg:{theme.accent} bold"),
            ("pointer", f"fg:{theme.accent} bold"),
            ("highlighted", f"fg:{theme.accent} bold"),
            ("selected", f"fg:{theme.accent}"),
        ]
    )

    choices = [questionary.Choice(title=item.label, value=item.value) for item in items]

    default_choice = None
    if default is not None:
        for choice in choices:
            if choice.value == default:
                default_choice = choice
                break

    result = questionary.select(
        message,
        choices=choices,
        default=default_choice,
        style=style,
    ).ask()

    if result is None:
        raise KeyboardInterrupt

    return result
