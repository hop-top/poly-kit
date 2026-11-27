"""
hop_top_kit.upgrade — version check and notify for hop-top Python tools.

Fetches the latest GitHub release for a tool and writes a human-readable
upgrade notice to an output stream when a newer version is available.

Cache behaviour:
    Results are cached in a JSON file at
    ``{state_dir}/.upgrade-{name}-cache.json`` with the fields
    ``version`` (str) and ``checked_at`` (Unix timestamp float).
    While the cached entry is younger than ``cache_ttl``, no network
    request is made.  When the cache is absent or expired the GitHub
    Releases API is queried; a fresh entry is written on success.

Error handling:
    :meth:`Checker.notify_if_available` silently swallows all errors
    (network, JSON parse, file I/O, version parse); it is intended for
    best-effort use at CLI startup and must never abort the tool.
    :meth:`Checker.check` is the strict variant: it returns an
    :class:`UpgradeCheckResult` and raises :class:`UpgradeCheckError`
    (original error chained as ``__cause__``) so callers, e.g.
    ``upgrade --check``, can tell a failed lookup from "up to date".

Notice format (written to ``out`` when a newer version exists)::

    \\nA new release of {name} is available: {current} → {latest}\\n
    https://github.com/{owner}/{repo}/releases/latest\\n

Component tags and prereleases:
    By default the check reads ``releases/latest`` and strips leading
    ``v`` characters.  Set ``tag_prefix`` (e.g. ``"xray/v"``) for a
    monorepo whose tags carry a component: releases are listed, tags
    without the prefix are ignored and the notice links to the matching
    release.  Set ``include_prereleases`` to consider prereleases;
    without it prereleases are never offered.  Both list at most 3 pages
    of 100 releases, newest first, drafts skipped; the highest version
    wins.

Usage example::

    from datetime import timedelta
    from hop_top_kit.upgrade import CheckerOptions, create_checker

    opts = CheckerOptions(
        name="mytool",
        current_version="1.2.3",
        owner="myorg",
        repo="mytool",
        cache_ttl=timedelta(hours=24),
    )
    checker = create_checker(opts)

    # At CLI startup — prints notice if newer version exists, else silent:
    checker.notify_if_available()

    # Strict variant — raises UpgradeCheckError on failure:
    result = checker.check()
    if result.update_available:
        print(result.latest, result.url)
"""

from __future__ import annotations

import contextlib
import json
import os
import re
import sys
import tempfile
import time
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from datetime import timedelta
from typing import IO

from packaging.version import InvalidVersion, Version

# Releases requested per page when listing (GitHub's maximum), and the
# most pages fetched: bounds a listing to 3 requests.
_PAGE_SIZE = 100
_MAX_PAGES = 3

# Strict semver 2.0.0 (semver.org), as the TypeScript SDK validates tags.
_SEMVER = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)"
    r"(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?"
    r"(?:\+[0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*)?$"
)


@dataclass
class UpgradeCheckResult:
    """Outcome of :meth:`Checker.check`.

    Attributes:
        current:          Running version, as configured.
        latest:           Latest release version (no ``v`` / tag prefix).
        update_available: True when *latest* is strictly greater than
                          *current*.
        url:              Release page to link to.
    """

    current: str
    latest: str
    update_available: bool
    url: str


class UpgradeCheckError(Exception):
    """Raised by :meth:`Checker.check`; the underlying error, if any, is
    chained as ``__cause__``."""


@dataclass
class CheckerOptions:
    """Static configuration for an upgrade Checker.

    Attributes:
        name:            Package / tool name displayed in the notice.
        current_version: Installed version string (e.g. ``"1.2.3"``).
        owner:           GitHub repository owner (org or user).
        repo:            GitHub repository name.
        cache_ttl:       How long to reuse a cached check result before
                         hitting the network again.  Default: 24 hours.
        state_dir:       Directory used for the cache file.  Defaults to
                         ``tempfile.gettempdir()`` when ``None``.
        timeout:         HTTP request timeout in seconds.  Default: 5.
        tag_prefix:      Prefix identifying this tool's release tags, e.g.
                         ``"xray/v"`` for ``xray/v0.2.0``; stripped before
                         comparing.  Tags without it are not candidates.
                         Setting it lists releases instead of reading
                         ``releases/latest``.  Default: ``""``.
        include_prereleases: Offer prereleases as upgrades.  Setting it
                         lists releases instead of reading
                         ``releases/latest``.  Default: ``False``.
    """

    name: str
    current_version: str
    owner: str
    repo: str
    cache_ttl: timedelta = field(default_factory=lambda: timedelta(hours=24))
    state_dir: str | None = None
    timeout: int = 5
    tag_prefix: str = ""
    include_prereleases: bool = False


class Checker:
    """Checks whether a newer release is available and notifies the user.

    Constructed via :func:`create_checker`; do not instantiate directly.
    """

    def __init__(self, opts: CheckerOptions) -> None:
        self._opts = opts

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def check(self) -> UpgradeCheckResult:
        """Look up the latest release and return the outcome.

        Unlike :meth:`notify_if_available` failures are not swallowed:
        network, HTTP, parse, "no matching release" and invalid-semver
        errors raise :class:`UpgradeCheckError` with the original error
        chained.  Honours ``tag_prefix``, ``include_prereleases`` and the
        cache; a ``cache_ttl`` of zero bypasses the cache.
        """
        opts = self._opts
        try:
            tag: str | None = None
            cached = self._read_cache()
            if cached is not None:
                latest, tag = cached
            elif self._list_mode():
                found = self._fetch_highest()
                if found is None:
                    raise UpgradeCheckError("no matching release found")
                latest, tag = found
                self._write_cache(latest, tag)
            else:
                latest = self._fetch_latest()
                self._write_cache(latest)
            return UpgradeCheckResult(
                current=opts.current_version,
                latest=latest,
                # Only strictly greater counts as an update.
                update_available=Version(latest) > Version(opts.current_version),
                url=self._notice_url(tag),
            )
        except UpgradeCheckError:
            raise
        except Exception as err:
            raise UpgradeCheckError(f"upgrade check failed: {err}") from err

    def notify_if_available(self, out: IO = sys.stdout) -> None:
        """Write an upgrade notice to *out* if a newer version is available.

        Silently no-ops on any error (network, I/O, parse, etc.).
        Uses a local cache to avoid redundant network requests.

        Args:
            out: Output stream to write the notice to.
                 Defaults to ``sys.stdout``.
        """
        with contextlib.suppress(Exception):
            r = self.check()
            if not r.update_available:
                return
            out.write(
                f"\nA new release of {self._opts.name} is available: "
                f"{r.current} \u2192 {r.latest}\n"
                f"{r.url}\n"
            )

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    def _cache_path(self) -> str:
        state_dir = self._opts.state_dir or tempfile.gettempdir()
        return os.path.join(state_dir, f".upgrade-{self._opts.name}-cache.json")

    def _list_mode(self) -> bool:
        """True when releases are listed instead of ``releases/latest``."""
        return self._opts.tag_prefix != "" or self._opts.include_prereleases

    def _tag_version(self, tag: object) -> str | None:
        """The semver *tag* names once the prefix and one ``v`` are
        stripped; ``None`` for another component's tag or a non-semver."""
        prefix = self._opts.tag_prefix
        if not isinstance(tag, str) or not tag.startswith(prefix):
            return None
        rest = tag[len(prefix) :]
        if rest.startswith("v"):
            rest = rest[1:]
        return rest if _SEMVER.match(rest) else None

    def _tag_names_version(self, tag: object, version: str) -> bool:
        named = self._tag_version(tag)
        if named is None:
            return False
        try:
            return Version(named) == Version(version)
        except InvalidVersion:
            return False

    def _read_cache(self) -> tuple[str, str | None] | None:
        """Return cached ``(version, tag)`` if still within TTL, else None.

        In list mode a cached tag is trusted only if it starts with the
        prefix and names the cached version (the cache may sit in a shared
        directory and the tag becomes part of a link); otherwise, a
        missing tag included, it is a miss.
        """
        path = self._cache_path()
        try:
            with open(path) as f:
                data = json.load(f)
            checked_at: float = data["checked_at"]
            ttl_seconds = self._opts.cache_ttl.total_seconds()
            if time.time() - checked_at < ttl_seconds:
                version = str(data["version"])
                if not self._list_mode():
                    return version, None
                if not self._tag_names_version(data.get("tag"), version):
                    return None
                return version, str(data["tag"])
        except Exception:
            pass
        return None

    def _write_cache(self, version: str, tag: str | None = None) -> None:
        """Persist *version* (and *tag* in list mode) with the timestamp."""
        path = self._cache_path()
        try:
            entry: dict[str, object] = {"version": version, "checked_at": time.time()}
            if tag is not None:
                entry["tag"] = tag
            with open(path, "w") as f:
                json.dump(entry, f)
        except Exception:
            pass

    def _notice_url(self, tag: str | None) -> str:
        """The link in the notice: ``releases/latest`` by default, else the
        exact release (``releases/latest`` may be another component's)."""
        opts = self._opts
        base = f"https://github.com/{opts.owner}/{opts.repo}/releases"
        if not self._list_mode():
            return f"{base}/latest"
        if tag is None:
            return base
        # Per-segment percent-encoding, as JS encodeURIComponent does.
        segs = (urllib.parse.quote(seg, safe="!'()*") for seg in tag.split("/"))
        return f"{base}/tag/{'/'.join(segs)}"

    def _fetch_latest(self) -> str:
        """Fetch the latest release tag from GitHub and return version string.

        Raises on any error or non-200 status.
        """
        url = f"https://api.github.com/repos/{self._opts.owner}/{self._opts.repo}/releases/latest"
        with urllib.request.urlopen(url, timeout=self._opts.timeout) as resp:
            if resp.status != 200:
                raise UpgradeCheckError(f"release lookup failed: HTTP {resp.status}")
            body = resp.read()
        data = json.loads(body)
        tag: str = data["tag_name"]
        return tag.lstrip("v")

    def _fetch_highest(self) -> tuple[str, str] | None:
        """List releases (newest first, bounded) and return ``(version,
        tag)`` of the highest non-draft release whose tag starts with
        ``tag_prefix``; prereleases only with ``include_prereleases``.
        A later page failing keeps what earlier pages found."""
        opts = self._opts
        deadline = time.monotonic() + opts.timeout
        best: tuple[str, str, Version] | None = None
        for page in range(1, _MAX_PAGES + 1):
            url = (
                f"https://api.github.com/repos/{opts.owner}/{opts.repo}/releases"
                f"?per_page={_PAGE_SIZE}&page={page}"
            )
            req = urllib.request.Request(url, headers={"Accept": "application/vnd.github+json"})
            try:
                remaining = max(deadline - time.monotonic(), 0.001)
                with urllib.request.urlopen(req, timeout=remaining) as resp:
                    if resp.status != 200:
                        raise OSError(resp.status)
                    body = json.loads(resp.read())
                if not isinstance(body, list):
                    raise ValueError("not a list")
            except Exception:
                if page == 1:
                    raise
                break
            for rel in body:
                if not isinstance(rel, dict) or rel.get("draft") is True:
                    continue
                tag = rel.get("tag_name")
                named = self._tag_version(tag)
                if named is None:
                    continue
                try:
                    parsed = Version(named)
                except InvalidVersion:
                    continue
                if not opts.include_prereleases and (
                    rel.get("prerelease") is True or parsed.is_prerelease
                ):
                    continue
                if best is None or parsed > best[2]:
                    best = (named, str(tag), parsed)
            if len(body) < _PAGE_SIZE:
                break
        return None if best is None else (best[0], best[1])


def create_checker(opts: CheckerOptions) -> Checker:
    """Create a :class:`Checker` from *opts*.

    Args:
        opts: Configuration for the checker.

    Returns:
        A :class:`Checker` instance ready to call
        :meth:`~Checker.check` or :meth:`~Checker.notify_if_available`.

    Example::

        from datetime import timedelta
        from hop_top_kit.upgrade import CheckerOptions, create_checker

        checker = create_checker(CheckerOptions(
            name="mytool",
            current_version="0.9.0",
            owner="myorg",
            repo="mytool",
        ))
        checker.notify_if_available()
    """
    return Checker(opts)
