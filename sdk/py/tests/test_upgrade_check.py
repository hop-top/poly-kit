"""
Tests for hop_top_kit.upgrade — result-returning Checker.check().

Covers: result shape, up-to-date, tag_prefix / include_prereleases, cache
use and ttl-0 bypass, UpgradeCheckError (with chained cause) on network,
HTTP, parse, no-matching-release and invalid-semver failures, and
notify_if_available staying silent on the same failures.
"""

from __future__ import annotations

import io
import json
import tempfile
import unittest
from datetime import timedelta
from unittest.mock import MagicMock, patch

from hop_top_kit.upgrade import (
    CheckerOptions,
    UpgradeCheckError,
    UpgradeCheckResult,
    create_checker,
)


def _resp(body: object, status: int = 200) -> MagicMock:
    m = MagicMock()
    m.status = status
    m.read.return_value = body if isinstance(body, bytes) else json.dumps(body).encode()
    m.__enter__ = lambda s: s
    m.__exit__ = MagicMock(return_value=False)
    return m


def _opts(td: str, current: str = "1.0.0", **kw: object) -> CheckerOptions:
    return CheckerOptions(
        name="mypkg",
        current_version=current,
        owner="myorg",
        repo="myrepo",
        state_dir=td,
        **kw,  # type: ignore[arg-type]
    )


LATEST = "https://github.com/myorg/myrepo/releases/latest"


class TestCheck(unittest.TestCase):
    def test_update_available(self):
        with tempfile.TemporaryDirectory() as td:
            c = create_checker(_opts(td))
            with patch("urllib.request.urlopen", return_value=_resp({"tag_name": "v2.0.0"})):
                r = c.check()
            assert r == UpgradeCheckResult("1.0.0", "2.0.0", True, LATEST)

    def test_up_to_date(self):
        with tempfile.TemporaryDirectory() as td:
            c = create_checker(_opts(td, current="2.0.0"))
            with patch("urllib.request.urlopen", return_value=_resp({"tag_name": "v2.0.0"})):
                r = c.check()
            assert r.update_available is False
            assert r.latest == "2.0.0"

    def test_tag_prefix_links_release(self):
        with tempfile.TemporaryDirectory() as td:
            c = create_checker(_opts(td, tag_prefix="xray/v"))
            rels = [{"tag_name": "other/v9.0.0"}, {"tag_name": "xray/v1.5.0"}]
            with patch("urllib.request.urlopen", return_value=_resp(rels)):
                r = c.check()
            assert r.latest == "1.5.0"
            assert r.update_available is True
            assert r.url == "https://github.com/myorg/myrepo/releases/tag/xray/v1.5.0"

    def test_prereleases_option(self):
        rels = [{"tag_name": "v2.0.0-rc.1", "prerelease": True}, {"tag_name": "v1.1.0"}]
        with tempfile.TemporaryDirectory() as td:
            with patch("urllib.request.urlopen", return_value=_resp(rels)):
                assert create_checker(_opts(td, tag_prefix="v")).check().latest == "1.1.0"
        with tempfile.TemporaryDirectory() as td:
            with patch("urllib.request.urlopen", return_value=_resp(rels)):
                c = create_checker(_opts(td, include_prereleases=True))
                assert c.check().latest == "2.0.0-rc.1"

    def test_cache_used_then_bypassed_by_zero_ttl(self):
        with tempfile.TemporaryDirectory() as td:
            c = create_checker(_opts(td))
            with patch("urllib.request.urlopen", return_value=_resp({"tag_name": "v2.0.0"})) as m:
                c.check()
                c.check()
                assert m.call_count == 1
        with tempfile.TemporaryDirectory() as td:
            c = create_checker(_opts(td, cache_ttl=timedelta(0)))
            with patch("urllib.request.urlopen", return_value=_resp({"tag_name": "v2.0.0"})) as m:
                c.check()
                c.check()
                assert m.call_count == 2


class TestCheckErrors(unittest.TestCase):
    def _raises(self, opts_kw: dict, **patch_kw: object) -> UpgradeCheckError:
        with tempfile.TemporaryDirectory() as td:
            c = create_checker(_opts(td, **opts_kw))
            with patch("urllib.request.urlopen", **patch_kw):
                with self.assertRaises(UpgradeCheckError) as cm:
                    c.check()
            return cm.exception

    def test_network_error_chains_cause(self):
        boom = OSError("down")
        err = self._raises({}, side_effect=boom)
        assert err.__cause__ is boom

    def test_http_status(self):
        self._raises({}, return_value=_resp({"tag_name": "v2.0.0"}, status=500))

    def test_bad_json(self):
        err = self._raises({}, return_value=_resp(b"not json"))
        assert err.__cause__ is not None

    def test_missing_tag_name(self):
        self._raises({}, return_value=_resp({}))

    def test_list_http_status(self):
        self._raises({"tag_prefix": "x/v"}, return_value=_resp([], status=503))

    def test_no_matching_release(self):
        self._raises({"tag_prefix": "xray/v"}, return_value=_resp([{"tag_name": "other/v1.0.0"}]))

    def test_invalid_current_semver(self):
        with tempfile.TemporaryDirectory() as td:
            c = create_checker(_opts(td, current="not-a-version"))
            with patch("urllib.request.urlopen", return_value=_resp({"tag_name": "v2.0.0"})):
                with self.assertRaises(UpgradeCheckError) as cm:
                    c.check()
            assert cm.exception.__cause__ is not None

    def test_is_exception_subclass(self):
        assert issubclass(UpgradeCheckError, Exception)


class TestNotifyStaysSilent(unittest.TestCase):
    def test_never_raises(self):
        with tempfile.TemporaryDirectory() as td:
            out = io.StringIO()
            c = create_checker(_opts(td))
            with patch("urllib.request.urlopen", side_effect=OSError("down")):
                c.notify_if_available(out=out)
            assert out.getvalue() == ""
