"""Tests for hop_top_kit.tui.progress."""

from __future__ import annotations

from unittest.mock import MagicMock, patch

from hop_top_kit.cli import _build_theme
from hop_top_kit.tui.progress import progress


def _make_mock_progress():
    """Build a Progress mock that satisfies the context manager protocol."""
    mock_prog = MagicMock()
    mock_prog.__enter__ = MagicMock(return_value=mock_prog)
    mock_prog.__exit__ = MagicMock(return_value=False)
    mock_prog.add_task = MagicMock(return_value=0)
    return mock_prog


def test_progress_context_manager_enters_and_exits():
    """Context manager should enter and exit without error."""
    theme = _build_theme()
    mock_prog = _make_mock_progress()

    with patch("hop_top_kit.tui.progress.Progress", return_value=mock_prog):
        with progress(theme, total=10) as p:
            assert p is not None

    mock_prog.__enter__.assert_called_once()
    mock_prog.__exit__.assert_called_once()


def test_progress_total_stored_on_handle():
    """Handle.total should reflect the configured total."""
    theme = _build_theme()
    mock_prog = _make_mock_progress()

    with patch("hop_top_kit.tui.progress.Progress", return_value=mock_prog):
        with progress(theme, total=42) as p:
            assert p.total == 42


def test_progress_advance_delegates_to_rich():
    """Handle.advance() should call Progress.advance() with the task id."""
    theme = _build_theme()
    mock_prog = _make_mock_progress()
    task_id = 7
    mock_prog.add_task.return_value = task_id

    with patch("hop_top_kit.tui.progress.Progress", return_value=mock_prog):
        with progress(theme, total=10) as p:
            p.advance(3)

    mock_prog.advance.assert_called_once_with(task_id, 3)


def test_progress_default_total():
    """Default total should be 100."""
    theme = _build_theme()
    mock_prog = _make_mock_progress()

    with patch("hop_top_kit.tui.progress.Progress", return_value=mock_prog):
        with progress(theme) as p:
            assert p.total == 100


def test_progress_uses_accent_for_bar():
    """Progress bar column should be constructed with theme.accent as complete_style."""
    theme = _build_theme()
    mock_prog = _make_mock_progress()

    with patch("hop_top_kit.tui.progress.Progress", return_value=mock_prog):
        with patch("hop_top_kit.tui.progress.BarColumn") as MockBar:
            mock_bar_inst = MagicMock()
            MockBar.return_value = mock_bar_inst
            with progress(theme, total=5):
                pass

        _, kwargs = MockBar.call_args
        assert kwargs.get("complete_style") == theme.accent
        assert kwargs.get("style") == theme.muted
