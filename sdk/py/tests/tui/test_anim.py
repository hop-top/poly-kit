"""Tests for hop_top_kit.tui.anim."""

from __future__ import annotations

from unittest.mock import MagicMock, patch

from hop_top_kit.cli import _build_theme
from hop_top_kit.tui.anim import _make_gradient, anim


def _make_mock_live():
    mock_live = MagicMock()
    mock_live.__enter__ = MagicMock(return_value=mock_live)
    mock_live.__exit__ = MagicMock(return_value=False)
    return mock_live


def test_anim_context_manager_enters_and_exits():
    """Context manager should enter and exit without error."""
    theme = _build_theme()
    mock_live = _make_mock_live()

    with patch("hop_top_kit.tui.anim.Live", return_value=mock_live):
        with anim(theme, label="test", width=5) as a:
            assert a is not None

    mock_live.__enter__.assert_called_once()
    mock_live.__exit__.assert_called_once()


def test_anim_set_label_updates_label():
    """_AnimHandle.set_label should change the internal label."""
    theme = _build_theme()
    mock_live = _make_mock_live()

    with patch("hop_top_kit.tui.anim.Live", return_value=mock_live):
        with anim(theme, label="initial", width=5) as a:
            a.set_label("updated")
            assert a._label == "updated"


def test_anim_gradient_length_matches_width():
    """Gradient should have exactly width steps."""
    theme = _build_theme()
    grad = _make_gradient(theme.accent, theme.secondary, 8)
    assert len(grad) == 8


def test_anim_gradient_start_end_colors():
    """First color should equal accent, last should equal secondary."""
    theme = _build_theme()
    grad = _make_gradient(theme.accent, theme.secondary, 5)
    # Normalize comparison: strip # and compare lowercase
    assert grad[0].lstrip("#").lower() == theme.accent.lstrip("#").lower()
    assert grad[-1].lstrip("#").lower() == theme.secondary.lstrip("#").lower()


def test_anim_render_contains_label():
    """_render should include the label text in the Rich Text object."""
    theme = _build_theme()
    mock_live = _make_mock_live()

    with patch("hop_top_kit.tui.anim.Live", return_value=mock_live):
        with anim(theme, label="my-label", width=4) as a:
            rendered = a._render()
            plain = rendered.plain
            assert "my-label" in plain


def test_anim_thread_started_and_stopped():
    """Background animation thread should start on enter and stop on exit."""
    theme = _build_theme()
    mock_live = _make_mock_live()

    with patch("hop_top_kit.tui.anim.Live", return_value=mock_live):
        with anim(theme, label="", width=3) as a:
            assert a._thread.is_alive()
        # after context exit, thread should be stopped
        assert not a._thread.is_alive()
