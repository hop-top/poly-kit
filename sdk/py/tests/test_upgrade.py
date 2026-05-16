"""
Tests for hop_top_kit.upgrade — version check and notify.

Covers:
- newer version available → notice written to out
- same/older version → nothing written
- cache hit within TTL → urlopen not called
- cache expired → urlopen called
- urlopen raises → silently skipped
- non-200 response → silently skipped
- notice format
"""

from __future__ import annotations

import io
import json
import os
import tempfile
import time
import unittest
from datetime import timedelta
from unittest.mock import MagicMock, patch

from hop_top_kit.upgrade import CheckerOptions, create_checker

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_response(tag: str, status: int = 200) -> MagicMock:
    """Return a mock response object for urllib.request.urlopen."""
    body = json.dumps({"tag_name": tag}).encode()
    mock_resp = MagicMock()
    mock_resp.status = status
    mock_resp.read.return_value = body
    mock_resp.__enter__ = lambda s: s
    mock_resp.__exit__ = MagicMock(return_value=False)
    return mock_resp


def _opts(state_dir: str, current: str = "1.0.0") -> CheckerOptions:
    return CheckerOptions(
        name="mypkg",
        current_version=current,
        owner="myorg",
        repo="myrepo",
        cache_ttl=timedelta(hours=24),
        state_dir=state_dir,
        timeout=5,
    )


EXPECTED_NOTICE = (
    "\nA new release of mypkg is available: 1.0.0 → 2.0.0\n"
    "https://github.com/myorg/myrepo/releases/latest\n"
)


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


class TestNotifyIfAvailable(unittest.TestCase):
    def test_newer_version_writes_notice(self, tmp_path=None):
        """Newer release → notice printed to out."""
        with tempfile.TemporaryDirectory() as td:
            out = io.StringIO()
            opts = _opts(td, current="1.0.0")
            checker = create_checker(opts)
            with patch("urllib.request.urlopen", return_value=_make_response("v2.0.0")):
                checker.notify_if_available(out=out)
            assert out.getvalue() == EXPECTED_NOTICE

    def test_same_version_writes_nothing(self):
        """Same release → nothing printed."""
        with tempfile.TemporaryDirectory() as td:
            out = io.StringIO()
            opts = _opts(td, current="2.0.0")
            checker = create_checker(opts)
            with patch("urllib.request.urlopen", return_value=_make_response("v2.0.0")):
                checker.notify_if_available(out=out)
            assert out.getvalue() == ""

    def test_older_version_writes_nothing(self):
        """Older release tag → nothing printed."""
        with tempfile.TemporaryDirectory() as td:
            out = io.StringIO()
            opts = _opts(td, current="3.0.0")
            checker = create_checker(opts)
            with patch("urllib.request.urlopen", return_value=_make_response("v2.0.0")):
                checker.notify_if_available(out=out)
            assert out.getvalue() == ""

    def test_cache_hit_within_ttl_skips_urlopen(self):
        """Cache present and fresh → urlopen not called."""
        with tempfile.TemporaryDirectory() as td:
            cache_path = os.path.join(td, ".upgrade-mypkg-cache.json")
            with open(cache_path, "w") as f:
                json.dump({"version": "1.0.0", "checked_at": time.time()}, f)

            out = io.StringIO()
            opts = _opts(td, current="1.0.0")
            checker = create_checker(opts)
            with patch("urllib.request.urlopen") as mock_open:
                checker.notify_if_available(out=out)
                mock_open.assert_not_called()

    def test_cache_expired_calls_urlopen(self):
        """Cache present but stale → urlopen called."""
        with tempfile.TemporaryDirectory() as td:
            cache_path = os.path.join(td, ".upgrade-mypkg-cache.json")
            stale_time = time.time() - (25 * 3600)  # 25h ago
            with open(cache_path, "w") as f:
                json.dump({"version": "1.0.0", "checked_at": stale_time}, f)

            out = io.StringIO()
            opts = _opts(td, current="1.0.0")
            checker = create_checker(opts)
            with patch(
                "urllib.request.urlopen", return_value=_make_response("v1.0.0")
            ) as mock_open:
                checker.notify_if_available(out=out)
                mock_open.assert_called_once()

    def test_urlopen_raises_silently_skipped(self):
        """Network error → no exception propagated, nothing written."""
        with tempfile.TemporaryDirectory() as td:
            out = io.StringIO()
            opts = _opts(td, current="1.0.0")
            checker = create_checker(opts)
            with patch("urllib.request.urlopen", side_effect=OSError("timeout")):
                checker.notify_if_available(out=out)
            assert out.getvalue() == ""

    def test_non_200_response_silently_skipped(self):
        """Non-200 HTTP status → no notice, no exception."""
        with tempfile.TemporaryDirectory() as td:
            out = io.StringIO()
            opts = _opts(td, current="1.0.0")
            checker = create_checker(opts)
            with patch("urllib.request.urlopen", return_value=_make_response("v2.0.0", status=404)):
                checker.notify_if_available(out=out)
            assert out.getvalue() == ""

    def test_notice_format(self):
        """Verify exact notice format."""
        with tempfile.TemporaryDirectory() as td:
            out = io.StringIO()
            opts = CheckerOptions(
                name="mypkg",
                current_version="1.0.0",
                owner="myorg",
                repo="myrepo",
                cache_ttl=timedelta(hours=24),
                state_dir=td,
            )
            checker = create_checker(opts)
            with patch("urllib.request.urlopen", return_value=_make_response("v2.0.0")):
                checker.notify_if_available(out=out)
            expected = (
                "\nA new release of mypkg is available: 1.0.0 → 2.0.0\n"
                "https://github.com/myorg/myrepo/releases/latest\n"
            )
            assert out.getvalue() == expected


if __name__ == "__main__":
    unittest.main()
