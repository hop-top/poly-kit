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
    All errors (network, JSON parse, file I/O, version parse) are
    silently swallowed.  The function is intended for best-effort use
    at CLI startup and must never abort the tool.

Notice format (written to ``out`` when a newer version exists)::

    \\nA new release of {name} is available: {current} → {latest}\\n
    https://github.com/{owner}/{repo}/releases/latest\\n

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
"""

from __future__ import annotations

import contextlib
import json
import os
import sys
import tempfile
import time
import urllib.request
from dataclasses import dataclass, field
from datetime import timedelta
from typing import IO

from packaging.version import InvalidVersion, Version


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
    """

    name: str
    current_version: str
    owner: str
    repo: str
    cache_ttl: timedelta = field(default_factory=lambda: timedelta(hours=24))
    state_dir: str | None = None
    timeout: int = 5


class Checker:
    """Checks whether a newer release is available and notifies the user.

    Constructed via :func:`create_checker`; do not instantiate directly.
    """

    def __init__(self, opts: CheckerOptions) -> None:
        self._opts = opts

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def notify_if_available(self, out: IO = sys.stdout) -> None:
        """Write an upgrade notice to *out* if a newer version is available.

        Silently no-ops on any error (network, I/O, parse, etc.).
        Uses a local cache to avoid redundant network requests.

        Args:
            out: Output stream to write the notice to.
                 Defaults to ``sys.stdout``.
        """
        with contextlib.suppress(Exception):
            self._run(out)

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    def _cache_path(self) -> str:
        state_dir = self._opts.state_dir or tempfile.gettempdir()
        return os.path.join(state_dir, f".upgrade-{self._opts.name}-cache.json")

    def _read_cache(self) -> str | None:
        """Return cached version string if still within TTL, else None."""
        path = self._cache_path()
        try:
            with open(path) as f:
                data = json.load(f)
            checked_at: float = data["checked_at"]
            ttl_seconds = self._opts.cache_ttl.total_seconds()
            if time.time() - checked_at < ttl_seconds:
                return str(data["version"])
        except Exception:
            pass
        return None

    def _write_cache(self, version: str) -> None:
        """Persist *version* and current timestamp to the cache file."""
        path = self._cache_path()
        try:
            with open(path, "w") as f:
                json.dump({"version": version, "checked_at": time.time()}, f)
        except Exception:
            pass

    def _fetch_latest(self) -> str | None:
        """Fetch the latest release tag from GitHub and return version string.

        Returns ``None`` on any error or non-200 status.
        """
        url = f"https://api.github.com/repos/{self._opts.owner}/{self._opts.repo}/releases/latest"
        with urllib.request.urlopen(url, timeout=self._opts.timeout) as resp:
            if resp.status != 200:
                return None
            body = resp.read()
        data = json.loads(body)
        tag: str = data["tag_name"]
        return tag.lstrip("v")

    def _run(self, out: IO) -> None:
        """Core logic: fetch (or read cache), compare, maybe write notice."""
        opts = self._opts

        latest = self._read_cache()
        if latest is None:
            latest = self._fetch_latest()
            if latest is None:
                return
            self._write_cache(latest)

        try:
            current_v = Version(opts.current_version)
            latest_v = Version(latest)
        except InvalidVersion:
            return

        if latest_v <= current_v:
            return

        out.write(
            f"\nA new release of {opts.name} is available: "
            f"{opts.current_version} \u2192 {latest}\n"
            f"https://github.com/{opts.owner}/{opts.repo}/releases/latest\n"
        )


def create_checker(opts: CheckerOptions) -> Checker:
    """Create a :class:`Checker` from *opts*.

    Args:
        opts: Configuration for the checker.

    Returns:
        A :class:`Checker` instance ready to call
        :meth:`~Checker.notify_if_available`.

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
