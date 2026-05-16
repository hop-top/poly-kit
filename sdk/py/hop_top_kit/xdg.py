"""
hop_top_kit.xdg — XDG Base Directory resolution with OS-native fallbacks.

Resolves per-user directories following the XDG Base Directory Specification.
Each function checks the corresponding XDG environment variable first; when
unset or empty, falls back to platform-native paths via ``platformdirs``.

Platform fallback matrix (XDG env unset):

+----------+---------------------------------+-------------------------------+
| Function | macOS                           | Windows / Linux               |
+----------+---------------------------------+-------------------------------+
| config   | ~/Library/Preferences           | %APPDATA% / ~/.config         |
| data     | ~/Library/Application Support   | %LOCALAPPDATA% / ~/.local/share|
| cache    | ~/Library/Caches                | %LOCALAPPDATA%\\cache /        |
|          |                                 | ~/.cache                      |
| state    | .../Application Support/<t>/state| %LOCALAPPDATA%\\<t>\\state /   |
|          |                                 | ~/.local/state                |
+----------+---------------------------------+-------------------------------+

Usage example::

    from hop_top_kit.xdg import config_dir, must_ensure

    cfg = must_ensure(config_dir("mytool"))   # creates dir, returns path
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

from platformdirs import PlatformDirs

__all__ = [
    "cache_dir",
    "config_dir",
    "data_dir",
    "must_ensure",
    "state_dir",
]

# Exposed as a module-level variable so tests can monkeypatch it without
# touching sys.platform directly (which is read-only on CPython 3.12+).
_PLATFORM: str = sys.platform


def _platform() -> str:
    """Return the current platform identifier (mockable by tests)."""
    return _PLATFORM


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def config_dir(tool: str) -> str:
    """Return the configuration directory for *tool*.

    Checks ``$XDG_CONFIG_HOME`` first; falls back to
    ``platformdirs.PlatformDirs(tool).user_config_dir``.

    Args:
        tool: Name of the application / tool (e.g. ``"mytool"``).

    Returns:
        Absolute path string. The directory is **not** guaranteed to exist;
        call :func:`must_ensure` to create it.

    Examples:
        >>> import os; os.environ["XDG_CONFIG_HOME"] = "/tmp/cfg"
        >>> config_dir("demo")
        '/tmp/cfg/demo'
    """
    xdg = os.environ.get("XDG_CONFIG_HOME", "")
    if xdg:
        return os.path.join(xdg, tool)
    return PlatformDirs(tool).user_config_dir


def data_dir(tool: str) -> str:
    """Return the data directory for *tool*.

    Checks ``$XDG_DATA_HOME`` first.  When unset, falls back to:

    - **macOS**:   ``~/Library/Application Support/<tool>``
    - **Windows**: ``%LOCALAPPDATA%\\<tool>``
    - **Linux**:   ``~/.local/share/<tool>``

    Args:
        tool: Name of the application / tool.

    Returns:
        Absolute path string.

    Raises:
        RuntimeError: If ``%LOCALAPPDATA%`` is unset on Windows.

    Examples:
        >>> import os; os.environ["XDG_DATA_HOME"] = "/tmp/data"
        >>> data_dir("demo")
        '/tmp/data/demo'
    """
    xdg = os.environ.get("XDG_DATA_HOME", "")
    if xdg:
        return os.path.join(xdg, tool)

    plat = _platform()
    home = str(Path.home())

    if plat == "darwin":
        return os.path.join(home, "Library", "Application Support", tool)
    if plat == "win32":
        local = os.environ.get("LOCALAPPDATA", "")
        if not local:
            raise RuntimeError("%LOCALAPPDATA% is not set")
        return os.path.join(local, tool)
    # Linux / other
    return os.path.join(home, ".local", "share", tool)


def cache_dir(tool: str) -> str:
    """Return the cache directory for *tool*.

    Checks ``$XDG_CACHE_HOME`` first; falls back to
    ``platformdirs.PlatformDirs(tool).user_cache_dir``.

    Args:
        tool: Name of the application / tool.

    Returns:
        Absolute path string.

    Examples:
        >>> import os; os.environ["XDG_CACHE_HOME"] = "/tmp/cache"
        >>> cache_dir("demo")
        '/tmp/cache/demo'
    """
    xdg = os.environ.get("XDG_CACHE_HOME", "")
    if xdg:
        return os.path.join(xdg, tool)
    return PlatformDirs(tool).user_cache_dir


def state_dir(tool: str) -> str:
    """Return the state directory for *tool*.

    Checks ``$XDG_STATE_HOME`` first.  When unset, falls back to:

    - **macOS**:   ``~/Library/Application Support/<tool>/state``
    - **Windows**: ``%LOCALAPPDATA%\\<tool>\\state``
    - **Linux**:   ``~/.local/state/<tool>``

    The ``/state`` suffix on macOS and Windows prevents collisions with
    :func:`data_dir`, which uses the same base path on those platforms.

    Args:
        tool: Name of the application / tool.

    Returns:
        Absolute path string.

    Raises:
        RuntimeError: If ``%LOCALAPPDATA%`` is unset on Windows.

    Examples:
        >>> import os; os.environ["XDG_STATE_HOME"] = "/tmp/state"
        >>> state_dir("demo")
        '/tmp/state/demo'
    """
    xdg = os.environ.get("XDG_STATE_HOME", "")
    if xdg:
        return os.path.join(xdg, tool)

    plat = _platform()
    home = str(Path.home())

    if plat == "darwin":
        return os.path.join(home, "Library", "Application Support", tool, "state")
    if plat == "win32":
        local = os.environ.get("LOCALAPPDATA", "")
        if not local:
            raise RuntimeError("%LOCALAPPDATA% is not set")
        return os.path.join(local, tool, "state")
    # Linux / other
    return os.path.join(home, ".local", "state", tool)


def must_ensure(path: str) -> str:
    """Create *path* (and any parents) with mode ``0o750``; return *path*.

    Idempotent — safe to call when the directory already exists.

    Args:
        path: Absolute directory path to create.

    Returns:
        The same *path* string passed in.

    Raises:
        OSError: If the directory cannot be created (e.g. a file already
            exists at that location, or permission denied).

    Examples:
        >>> import tempfile, os
        >>> with tempfile.TemporaryDirectory() as d:
        ...     p = must_ensure(os.path.join(d, "sub"))
        ...     os.path.isdir(p)
        True
    """
    os.makedirs(path, mode=0o750, exist_ok=True)
    return path
