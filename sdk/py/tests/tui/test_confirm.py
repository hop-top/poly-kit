"""Tests for hop_top_kit.tui.confirm."""

from unittest.mock import MagicMock, patch

import pytest

from hop_top_kit.cli import _build_theme
from hop_top_kit.tui.confirm import confirm


@pytest.fixture
def theme():
    return _build_theme()


def test_confirm_returns_true(theme):
    mock_question = MagicMock()
    mock_question.ask.return_value = True

    with patch(
        "hop_top_kit.tui.confirm.questionary.confirm", return_value=mock_question
    ) as mock_confirm:
        result = confirm(theme, "Are you sure?")

    assert result is True
    mock_confirm.assert_called_once()
    call_kwargs = mock_confirm.call_args
    assert call_kwargs.args[0] == "Are you sure?"


def test_confirm_returns_false(theme):
    mock_question = MagicMock()
    mock_question.ask.return_value = False

    with patch("hop_top_kit.tui.confirm.questionary.confirm", return_value=mock_question):
        result = confirm(theme, "Delete file?", default=False)

    assert result is False


def test_confirm_raises_on_none(theme):
    mock_question = MagicMock()
    mock_question.ask.return_value = None

    with patch("hop_top_kit.tui.confirm.questionary.confirm", return_value=mock_question):
        with pytest.raises(KeyboardInterrupt):
            confirm(theme, "Proceed?")


def test_confirm_passes_default(theme):
    mock_question = MagicMock()
    mock_question.ask.return_value = False

    with patch(
        "hop_top_kit.tui.confirm.questionary.confirm", return_value=mock_question
    ) as mock_confirm:
        confirm(theme, "Continue?", default=False)

    _, kwargs = mock_confirm.call_args
    assert kwargs["default"] is False


def test_confirm_uses_accent_in_style(theme):
    mock_question = MagicMock()
    mock_question.ask.return_value = True

    with patch(
        "hop_top_kit.tui.confirm.questionary.confirm", return_value=mock_question
    ) as mock_confirm:
        confirm(theme, "Ok?")

    _, kwargs = mock_confirm.call_args
    style = kwargs["style"]
    # Style is a questionary.Style; check its style_rules contain the accent
    rules_str = str(style.style_rules)
    assert theme.accent in rules_str
