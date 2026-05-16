"""Tests for hop_top_kit.xdg — XDG path resolution."""

from __future__ import annotations

import os
from pathlib import Path

import pytest

from hop_top_kit.xdg import cache_dir, config_dir, data_dir, must_ensure, state_dir

# ---------------------------------------------------------------------------
# XDG env-var overrides
# ---------------------------------------------------------------------------


class TestConfigDir:
    def test_xdg_override(self, monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
        """XDG_CONFIG_HOME env var is honoured."""
        monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "cfg"))
        result = config_dir("mytool")
        assert result == str(tmp_path / "cfg" / "mytool")

    def test_xdg_empty_falls_through(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """Empty XDG_CONFIG_HOME falls back to platformdirs."""
        monkeypatch.delenv("XDG_CONFIG_HOME", raising=False)
        result = config_dir("mytool")
        assert "mytool" in result
        assert os.path.isabs(result)

    def test_returns_str(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("XDG_CONFIG_HOME", raising=False)
        assert isinstance(config_dir("x"), str)


class TestDataDir:
    def test_xdg_override(self, monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
        monkeypatch.setenv("XDG_DATA_HOME", str(tmp_path / "data"))
        assert data_dir("mytool") == str(tmp_path / "data" / "mytool")

    def test_xdg_empty_falls_through(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("XDG_DATA_HOME", raising=False)
        result = data_dir("mytool")
        assert "mytool" in result
        assert os.path.isabs(result)

    def test_darwin_fallback(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("XDG_DATA_HOME", raising=False)
        monkeypatch.setenv("HOME", "/Users/testuser")
        from hop_top_kit import xdg as xdg_mod

        original = xdg_mod._PLATFORM
        try:
            xdg_mod._PLATFORM = "darwin"
            result = data_dir("mytool")
            assert "Library" in result
            assert "Application Support" in result
            assert "mytool" in result
        finally:
            xdg_mod._PLATFORM = original

    def test_linux_fallback(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("XDG_DATA_HOME", raising=False)
        monkeypatch.setenv("HOME", "/home/testuser")
        from hop_top_kit import xdg as xdg_mod

        original = xdg_mod._PLATFORM
        try:
            xdg_mod._PLATFORM = "linux"
            result = data_dir("mytool")
            assert result == "/home/testuser/.local/share/mytool"
        finally:
            xdg_mod._PLATFORM = original

    def test_windows_fallback(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("XDG_DATA_HOME", raising=False)
        local = "C:\\Users\\test\\AppData\\Local"
        monkeypatch.setenv("LOCALAPPDATA", local)
        from hop_top_kit import xdg as xdg_mod

        original = xdg_mod._PLATFORM
        try:
            xdg_mod._PLATFORM = "win32"
            result = data_dir("mytool")
            # os.path.join uses the host separator; compare using it
            assert result == os.path.join(local, "mytool")
        finally:
            xdg_mod._PLATFORM = original


class TestCacheDir:
    def test_xdg_override(self, monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
        monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path / "cache"))
        assert cache_dir("mytool") == str(tmp_path / "cache" / "mytool")

    def test_xdg_empty_falls_through(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("XDG_CACHE_HOME", raising=False)
        result = cache_dir("mytool")
        assert "mytool" in result
        assert os.path.isabs(result)

    def test_returns_str(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("XDG_CACHE_HOME", raising=False)
        assert isinstance(cache_dir("x"), str)


class TestStateDir:
    def test_xdg_override(self, monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
        monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "state"))
        assert state_dir("mytool") == str(tmp_path / "state" / "mytool")

    def test_xdg_empty_falls_through(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("XDG_STATE_HOME", raising=False)
        result = state_dir("mytool")
        assert "mytool" in result
        assert os.path.isabs(result)

    def test_linux_fallback(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("XDG_STATE_HOME", raising=False)
        monkeypatch.setenv("HOME", "/home/testuser")
        from hop_top_kit import xdg as xdg_mod

        original = xdg_mod._PLATFORM
        try:
            xdg_mod._PLATFORM = "linux"
            result = state_dir("mytool")
            assert result == "/home/testuser/.local/state/mytool"
        finally:
            xdg_mod._PLATFORM = original

    def test_darwin_fallback_has_state_suffix(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("XDG_STATE_HOME", raising=False)
        monkeypatch.setenv("HOME", "/Users/testuser")
        from hop_top_kit import xdg as xdg_mod

        original = xdg_mod._PLATFORM
        try:
            xdg_mod._PLATFORM = "darwin"
            result = state_dir("mytool")
            assert result.endswith("state")
            assert "mytool" in result
        finally:
            xdg_mod._PLATFORM = original

    def test_windows_fallback_has_state_suffix(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("XDG_STATE_HOME", raising=False)
        local = "C:\\Users\\test\\AppData\\Local"
        monkeypatch.setenv("LOCALAPPDATA", local)
        from hop_top_kit import xdg as xdg_mod

        original = xdg_mod._PLATFORM
        try:
            xdg_mod._PLATFORM = "win32"
            result = state_dir("mytool")
            assert result == os.path.join(local, "mytool", "state")
        finally:
            xdg_mod._PLATFORM = original


# ---------------------------------------------------------------------------
# must_ensure
# ---------------------------------------------------------------------------


class TestMustEnsure:
    def test_creates_directory(self, tmp_path: Path) -> None:
        target = str(tmp_path / "sub" / "dir")
        result = must_ensure(target)
        assert result == target
        assert os.path.isdir(target)

    def test_returns_path(self, tmp_path: Path) -> None:
        target = str(tmp_path / "mydir")
        assert must_ensure(target) == target

    def test_idempotent(self, tmp_path: Path) -> None:
        target = str(tmp_path / "existing")
        must_ensure(target)
        must_ensure(target)  # should not raise
        assert os.path.isdir(target)

    def test_mode_0o750(self, tmp_path: Path) -> None:
        target = str(tmp_path / "moded")
        must_ensure(target)
        st = os.stat(target)
        # mask to lower 12 bits (mode bits)
        assert oct(st.st_mode & 0o777) == oct(0o750)

    def test_raises_on_bad_path(self, tmp_path: Path) -> None:
        """must_ensure raises OSError when path cannot be created."""
        # Create a file where we want a directory — makedirs will fail
        blocker = tmp_path / "blocker"
        blocker.write_text("x")
        bad = str(blocker / "child")
        with pytest.raises(OSError):
            must_ensure(bad)
