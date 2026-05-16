"""Tests for hop_top_kit.tui.spinner."""

from __future__ import annotations

from unittest.mock import MagicMock, patch

from hop_top_kit.cli import _build_theme
from hop_top_kit.tui.spinner import spinner


def test_spinner_context_manager_enters_and_exits():
    """Context manager should enter and exit without error."""
    theme = _build_theme()
    mock_status = MagicMock()
    mock_status.__enter__ = MagicMock(return_value=mock_status)
    mock_status.__exit__ = MagicMock(return_value=False)

    with patch("hop_top_kit.tui.spinner.Status", return_value=mock_status):
        with spinner(theme, "loading...") as s:
            assert s is not None

    mock_status.__enter__.assert_called_once()
    mock_status.__exit__.assert_called_once()


def test_spinner_update_calls_status_update():
    """Handle.update() should delegate to Status.update()."""
    theme = _build_theme()
    mock_status = MagicMock()
    mock_status.__enter__ = MagicMock(return_value=mock_status)
    mock_status.__exit__ = MagicMock(return_value=False)

    with patch("hop_top_kit.tui.spinner.Status", return_value=mock_status):
        with spinner(theme, "start") as s:
            s.update("new message")

    mock_status.update.assert_called_once_with("new message")


def test_spinner_uses_accent_color():
    """Status should be constructed with the theme accent as spinner_style."""
    theme = _build_theme()

    with patch("hop_top_kit.tui.spinner.Status") as MockStatus:
        mock_inst = MagicMock()
        mock_inst.__enter__ = MagicMock(return_value=mock_inst)
        mock_inst.__exit__ = MagicMock(return_value=False)
        MockStatus.return_value = mock_inst

        with spinner(theme, "msg"):
            pass

    _, kwargs = MockStatus.call_args
    assert kwargs.get("spinner_style") == theme.accent


def test_spinner_empty_message():
    """Spinner should accept empty string message."""
    theme = _build_theme()
    mock_status = MagicMock()
    mock_status.__enter__ = MagicMock(return_value=mock_status)
    mock_status.__exit__ = MagicMock(return_value=False)

    with patch("hop_top_kit.tui.spinner.Status", return_value=mock_status), spinner(theme) as s:
        assert s is not None
