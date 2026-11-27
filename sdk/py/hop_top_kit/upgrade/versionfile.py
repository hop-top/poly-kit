"""The Migrator's canonical version file and the data dir it lives in:
``<data dir>/<schema>/version``, with backups under ``<data dir>/backups``."""

from __future__ import annotations

import os
from pathlib import Path

from hop_top_kit import xdg


def resolve_data_dir(tool: str, data_dir: str | os.PathLike[str] | None) -> Path:
    """*data_dir*, or ``xdg.data_dir("hop/<tool>")``."""
    if data_dir:
        return Path(data_dir)
    return Path(xdg.data_dir(f"hop/{tool}"))


def _read(directory: Path) -> str:
    try:
        return (directory / "version").read_text().strip()
    except FileNotFoundError:
        return ""


def read_version_file(tool: str, driver_name: str) -> str:
    """The version from the standard location; ``""`` when absent."""
    return _read(resolve_data_dir(tool, None) / driver_name)


def read_version_file_in(data_dir: str | os.PathLike[str], driver_name: str) -> str:
    """``<data_dir>/<driver_name>/version``, for a Migrator built with
    ``data_dir``; ``""`` when absent."""
    return _read(Path(data_dir) / driver_name)


def write_version_file(base: Path, schema: str, version: str) -> None:
    """Write ``<base>/<schema>/version``; raises :class:`OSError`."""
    d = base / schema
    d.mkdir(mode=0o750, parents=True, exist_ok=True)
    fd = os.open(d / "version", os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write(version)
