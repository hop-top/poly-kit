"""Tests for hop_top_kit.tui.select."""

from unittest.mock import MagicMock, patch

import pytest

from hop_top_kit.cli import _build_theme
from hop_top_kit.tui.select import SelectItem, select


@pytest.fixture
def theme():
    return _build_theme()


@pytest.fixture
def items():
    return [
        SelectItem(label="Alpha", value="alpha"),
        SelectItem(label="Beta", value="beta"),
        SelectItem(label="Gamma", value="gamma"),
    ]


def test_select_returns_value(theme, items):
    mock_question = MagicMock()
    mock_question.ask.return_value = "beta"

    with patch("hop_top_kit.tui.select.questionary.select", return_value=mock_question):
        result = select(theme, "Pick one:", items)

    assert result == "beta"


def test_select_raises_on_none(theme, items):
    mock_question = MagicMock()
    mock_question.ask.return_value = None

    with patch("hop_top_kit.tui.select.questionary.select", return_value=mock_question):
        with pytest.raises(KeyboardInterrupt):
            select(theme, "Pick one:", items)


def test_select_passes_correct_choices(theme, items):
    mock_question = MagicMock()
    mock_question.ask.return_value = "alpha"

    with patch("hop_top_kit.tui.select.questionary.select", return_value=mock_question) as mock_sel:
        select(theme, "Choose:", items)

    _, kwargs = mock_sel.call_args
    choices = kwargs["choices"]
    assert len(choices) == 3
    assert choices[0].title == "Alpha"
    assert choices[0].value == "alpha"
    assert choices[1].title == "Beta"
    assert choices[1].value == "beta"
    assert choices[2].title == "Gamma"
    assert choices[2].value == "gamma"


def test_select_passes_message(theme, items):
    mock_question = MagicMock()
    mock_question.ask.return_value = "alpha"

    with patch("hop_top_kit.tui.select.questionary.select", return_value=mock_question) as mock_sel:
        select(theme, "Which option?", items)

    call_args = mock_sel.call_args
    assert call_args.args[0] == "Which option?"


def test_select_default_none_when_not_set(theme, items):
    mock_question = MagicMock()
    mock_question.ask.return_value = "alpha"

    with patch("hop_top_kit.tui.select.questionary.select", return_value=mock_question) as mock_sel:
        select(theme, "Pick:", items, default=None)

    _, kwargs = mock_sel.call_args
    assert kwargs["default"] is None


def test_select_default_matched_by_value(theme, items):
    mock_question = MagicMock()
    mock_question.ask.return_value = "gamma"

    with patch("hop_top_kit.tui.select.questionary.select", return_value=mock_question) as mock_sel:
        select(theme, "Pick:", items, default="gamma")

    _, kwargs = mock_sel.call_args
    assert kwargs["default"] is not None
    assert kwargs["default"].value == "gamma"


def test_select_uses_accent_in_style(theme, items):
    mock_question = MagicMock()
    mock_question.ask.return_value = "alpha"

    with patch("hop_top_kit.tui.select.questionary.select", return_value=mock_question) as mock_sel:
        select(theme, "Pick:", items)

    _, kwargs = mock_sel.call_args
    style = kwargs["style"]
    rules_str = str(style.style_rules)
    assert theme.accent in rules_str
