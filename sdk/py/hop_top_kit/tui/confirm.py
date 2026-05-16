"""hop_top_kit.tui.confirm — yes/no confirmation prompt."""

from __future__ import annotations

import questionary
from questionary import Style

from hop_top_kit.cli import Theme


def confirm(theme: Theme, message: str, default: bool = True) -> bool:
    """Prompt user for yes/no confirmation.

    Args:
        theme:   Theme supplying accent color for the pointer.
        message: Prompt text shown to the user.
        default: Pre-selected answer (True = yes, False = no).

    Returns:
        True if confirmed, False otherwise.

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

    result = questionary.confirm(message, default=default, style=style).ask()

    if result is None:
        raise KeyboardInterrupt

    return result
