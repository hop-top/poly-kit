"""
Tests for hop_top_kit.upgrade — component tags and prereleases.

Covers tag_prefix / include_prereleases: prefix filter, list mode (highest
version, drafts, paging, bounds), prerelease handling, notice link, cached
tag validation, silent errors, and unchanged default behaviour.
"""

from __future__ import annotations

import io
import json
import os
import tempfile
import time
import unittest
import urllib.parse
from datetime import timedelta
from unittest.mock import MagicMock, patch

from hop_top_kit.upgrade import CheckerOptions, create_checker

API = "https://api.github.com/repos/myorg/myrepo/releases"


def _resp(body: object, status: int = 200) -> MagicMock:
    m = MagicMock()
    m.status = status
    m.read.return_value = json.dumps(body).encode()
    m.__enter__ = lambda s: s
    m.__exit__ = MagicMock(return_value=False)
    return m


def rel(tag: str, **kw: object) -> dict:
    return {"tag_name": tag, **kw}


class Listing:
    """A urlopen stand-in serving pages[n-1] for ?page=n on the list URL."""

    def __init__(self, *pages: object, status: int = 200) -> None:
        self.pages = pages
        self.status = status
        self.urls: list[str] = []

    def __call__(self, target, timeout=None):
        url = target if isinstance(target, str) else target.full_url
        self.urls.append(url)
        parsed = urllib.parse.urlparse(url)
        if f"{parsed.scheme}://{parsed.netloc}{parsed.path}" != API:
            return _resp({"message": "Not Found"}, 404)
        n = int(urllib.parse.parse_qs(parsed.query).get("page", ["1"])[0])
        body = self.pages[n - 1] if n <= len(self.pages) else []
        return _resp(body, self.status)


def filler(n: int, count: int = 100) -> list[dict]:
    return [rel(f"kit/v1.{n}.{i}") for i in range(count)]


class ListModeBase(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.dir = self._td.name
        self.addCleanup(self._td.cleanup)

    def run_check(self, current: str, listing, **extra) -> str:
        out = io.StringIO()
        opts = CheckerOptions(
            name="xray",
            current_version=current,
            owner="myorg",
            repo="myrepo",
            cache_ttl=timedelta(hours=24),
            state_dir=self.dir,
            **extra,
        )
        with patch("urllib.request.urlopen", side_effect=listing):
            create_checker(opts).notify_if_available(out)
        return out.getvalue()

    def clear_cache(self) -> None:
        os.remove(os.path.join(self.dir, ".upgrade-xray-cache.json"))


class TestTagPrefix(ListModeBase):
    def test_prefixed_tag_notifies_and_links_release(self):
        got = self.run_check("0.1.0", Listing([rel("xray/v0.2.0")]), tag_prefix="xray/v")
        self.assertEqual(
            got,
            "\nA new release of xray is available: 0.1.0 → 0.2.0\n"
            "https://github.com/myorg/myrepo/releases/tag/xray/v0.2.0\n",
        )

    def test_other_components_never_notify(self):
        got = self.run_check(
            "0.1.0", Listing([rel("kit/v9.0.0"), rel("xray/v0.2.0")]), tag_prefix="xray/v"
        )
        self.assertIn("0.1.0 → 0.2.0", got)
        self.clear_cache()
        got = self.run_check(
            "0.1.0", Listing([rel("kit/v9.0.0"), rel("xray-cli/v9.0.0")]), tag_prefix="xray/v"
        )
        self.assertEqual(got, "")

    def test_highest_version_not_first_listed(self):
        page = [rel("xray/v0.2.1"), rel("xray/v0.10.0"), rel("xray/v0.9.0")]
        got = self.run_check("0.1.0", Listing(page), tag_prefix="xray/v")
        self.assertIn("→ 0.10.0", got)

    def test_drafts_skipped(self):
        page = [rel("xray/v0.3.0", draft=True), rel("xray/v0.2.0")]
        got = self.run_check("0.1.0", Listing(page), tag_prefix="xray/v")
        self.assertIn("→ 0.2.0", got)

    def test_never_reads_releases_latest(self):
        listing = Listing([rel("xray/v0.2.0")])
        self.run_check("0.1.0", listing, tag_prefix="xray/v")
        self.assertTrue(listing.urls)
        for u in listing.urls:
            self.assertNotIn("/releases/latest", u)


class TestPrereleases(ListModeBase):
    def test_stable_user_not_nudged_without_option(self):
        page = [
            rel("xray/v0.3.0-rc.1", prerelease=True),
            rel("xray/v0.3.0-rc.0"),  # GitHub flag unset; suffix says prerelease
        ]
        self.assertEqual(self.run_check("0.2.0", Listing(page), tag_prefix="xray/v"), "")

    def test_stable_user_sees_prerelease_with_option(self):
        page = [rel("xray/v0.3.0-rc.1", prerelease=True)]
        got = self.run_check("0.2.0", Listing(page), tag_prefix="xray/v", include_prereleases=True)
        self.assertIn("0.2.0 → 0.3.0-rc.1", got)

    def test_prerelease_user_sees_newer_prerelease(self):
        page = [
            rel("xray/v0.1.0-rc.1", prerelease=True),
            rel("xray/v0.1.0-rc.0", prerelease=True),
        ]
        kw = {"tag_prefix": "xray/v", "include_prereleases": True}
        got = self.run_check("0.1.0-rc.0", Listing(page), **kw)
        self.assertIn("0.1.0-rc.0 → 0.1.0-rc.1", got)
        self.clear_cache()
        self.assertEqual(self.run_check("0.1.0-rc.1", Listing(page), **kw), "")

    def test_prerelease_user_sees_final_stable(self):
        page = [rel("xray/v0.1.0-rc.1", prerelease=True), rel("xray/v0.1.0")]
        on = self.run_check(
            "0.1.0-rc.0", Listing(page), tag_prefix="xray/v", include_prereleases=True
        )
        self.assertIn("→ 0.1.0\n", on)
        self.clear_cache()
        off = self.run_check("0.1.0-rc.0", Listing(page), tag_prefix="xray/v")
        self.assertIn("→ 0.1.0\n", off)

    def test_include_prereleases_alone_links_tag_as_published(self):
        page = [rel("v1.0.0-rc.1", prerelease=True), rel("v0.9.0")]
        got = self.run_check("0.9.0", Listing(page), include_prereleases=True)
        self.assertEqual(
            got,
            "\nA new release of xray is available: 0.9.0 → 1.0.0-rc.1\n"
            "https://github.com/myorg/myrepo/releases/tag/v1.0.0-rc.1\n",
        )


class TestPaging(ListModeBase):
    def test_pages_until_short_page(self):
        listing = Listing(filler(0), [rel("xray/v0.2.0")])
        got = self.run_check("0.1.0", listing, tag_prefix="xray/v")
        self.assertIn("→ 0.2.0", got)
        self.assertEqual(len(listing.urls), 2)

    def test_at_most_three_pages(self):
        listing = Listing(filler(0), filler(1), filler(2), [rel("xray/v0.2.0")])
        self.assertEqual(self.run_check("0.1.0", listing, tag_prefix="xray/v"), "")
        self.assertEqual(len(listing.urls), 3)

    def test_later_page_failure_keeps_earlier_pages(self):
        full = [rel(f"xray/v0.2.{i}") for i in range(100)]
        calls = {"n": 0}

        def listing(target, timeout=None):
            calls["n"] += 1
            return _resp(full) if calls["n"] == 1 else _resp({}, 500)

        got = self.run_check("0.1.0", listing, tag_prefix="xray/v")
        self.assertIn("→ 0.2.99", got)


class TestSilentErrors(ListModeBase):
    def test_failures_are_silent(self):
        self.assertEqual(self.run_check("0.1.0", Listing([], status=403), tag_prefix="xray/v"), "")
        self.assertEqual(
            self.run_check("0.1.0", Listing({"message": "nope"}), tag_prefix="xray/v"), ""
        )
        junk = [None, {"tag_name": 7}, rel("xray/vbogus"), "x"]
        self.assertEqual(self.run_check("0.1.0", Listing(junk), tag_prefix="xray/v"), "")

        def boom(target, timeout=None):
            raise OSError("offline")

        self.assertEqual(self.run_check("0.1.0", boom, tag_prefix="xray/v"), "")


class TestCache(ListModeBase):
    def test_cache_keeps_tag_link(self):
        listing = Listing([rel("xray/v0.2.0")])
        self.run_check("0.1.0", listing, tag_prefix="xray/v")
        second = self.run_check("0.1.0", listing, tag_prefix="xray/v")
        self.assertEqual(len(listing.urls), 1)
        self.assertIn("releases/tag/xray/v0.2.0", second)

    def test_cached_tag_must_name_the_version(self):
        for label, tag in [
            ("path traversal", "xray/v../../../evil"),
            ("another version", "xray/v1.0.0"),
            ("another component", "kit/v2.0.0"),
            ("not a string", 42),
        ]:
            with self.subTest(label), tempfile.TemporaryDirectory() as td:
                self.dir = td
                with open(os.path.join(td, ".upgrade-xray-cache.json"), "w") as f:
                    json.dump({"version": "2.0.0", "checked_at": time.time(), "tag": tag}, f)
                listing = Listing([rel("xray/v1.5.0")])
                got = self.run_check("1.0.0", listing, tag_prefix="xray/v")
                self.assertEqual(len(listing.urls), 1, "poisoned cache was served")
                self.assertIn("1.0.0 → 1.5.0", got)
                self.assertIn("releases/tag/xray/v1.5.0", got)
                self.assertNotIn("evil", got)

    def test_tagless_cache_is_a_miss_in_list_mode(self):
        # A record as default mode writes it: no tag.
        with open(os.path.join(self.dir, ".upgrade-xray-cache.json"), "w") as f:
            json.dump({"version": "2.0.0", "checked_at": time.time()}, f)
        listing = Listing([rel("xray/v1.5.0")])
        got = self.run_check("1.0.0", listing, tag_prefix="xray/v")
        self.assertEqual(len(listing.urls), 1, "tagless cache was served")
        self.assertIn("1.0.0 \u2192 1.5.0", got)

    def test_tag_segments_are_encoded(self):
        # prefix is caller-controlled; a space must not reach the link raw
        got = self.run_check("0.1.0", Listing([rel("my tool/v0.2.0")]), tag_prefix="my tool/v")
        self.assertIn("releases/tag/my%20tool/v0.2.0", got)


class TestDefaultUnchanged(unittest.TestCase):
    def test_default_mode_is_releases_latest_with_bare_cache(self):
        with tempfile.TemporaryDirectory() as td:
            out = io.StringIO()
            body = MagicMock()
            body.status = 200
            body.read.return_value = json.dumps({"tag_name": "v2.0.0"}).encode()
            body.__enter__ = lambda s: s
            body.__exit__ = MagicMock(return_value=False)
            opts = CheckerOptions(
                name="xray", current_version="1.0.0", owner="myorg", repo="myrepo", state_dir=td
            )
            with patch("urllib.request.urlopen", return_value=body) as m:
                create_checker(opts).notify_if_available(out)
            self.assertEqual(m.call_count, 1)
            self.assertEqual(m.call_args[0][0], f"{API}/latest")
            self.assertEqual(
                out.getvalue(),
                "\nA new release of xray is available: 1.0.0 → 2.0.0\n"
                "https://github.com/myorg/myrepo/releases/latest\n",
            )
            with open(os.path.join(td, ".upgrade-xray-cache.json")) as f:
                self.assertEqual(sorted(json.load(f)), ["checked_at", "version"])


if __name__ == "__main__":
    unittest.main()
