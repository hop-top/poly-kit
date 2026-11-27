"""A schema driver for one or more files rewritten in place (config, saved
layouts, caches): Go ``upgrade/driver/configfile``.

The version lives in the Migrator's version file; ``set_version`` does
nothing. A backup copies each file into the backup directory under its
flattened name, so backups move between ports.
"""

from __future__ import annotations

import os
import shutil
from collections.abc import Iterable
from pathlib import Path

from hop_top_kit.upgrade.versionfile import read_version_file, read_version_file_in


def backup_file_name(path: str | os.PathLike[str]) -> str:
    """The collision-safe name a file is saved under: the cleaned path with
    each separator replaced by ``_``, so ``/etc/foo/config.yaml`` is
    ``_etc_foo_config.yaml``. Same rule as Go ``backupFileName``."""
    clean = os.path.normpath(os.fspath(path))
    if clean.startswith("//") and not clean.startswith("///"):
        clean = clean[1:]  # normpath keeps a POSIX double slash; Go's Clean does not
    return clean.replace(os.sep, "_")


def _copy(src: Path, dst: Path) -> bool:
    """Copy *src* over *dst*; ``False`` when *src* does not exist. A new
    *dst* gets *src*'s permission bits and is never wider while it is being
    written; an existing one keeps its own."""
    try:
        source = src.open("rb")
    except FileNotFoundError:
        return False
    with source:
        dst.parent.mkdir(mode=0o750, parents=True, exist_ok=True)
        fresh = not dst.exists()
        fd = os.open(dst, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "wb") as out:
            shutil.copyfileobj(source, out)
        if fresh:
            shutil.copymode(src, dst)
    return True


class ConfigFileDriver:
    """Backs up and restores a set of files.

    Args:
        paths: The files this schema owns. A missing file is skipped on
            backup, and a file absent from a backup is left alone on restore.
        name: Schema name, matched against ``Migration.schema``.
        tool: Tool whose standard data dir holds the version file.
        data_dir: Read the version file under this dir instead; pass the
            Migrator's own ``data_dir`` when it sets one.
    """

    def __init__(
        self,
        paths: Iterable[str | os.PathLike[str]],
        *,
        name: str = "config",
        tool: str = "",
        data_dir: str | os.PathLike[str] | None = None,
    ) -> None:
        self._name = name
        self._tool = tool
        self._data_dir = data_dir
        self._paths = [Path(p) for p in paths]

    @property
    def name(self) -> str:
        return self._name

    def version(self) -> str:
        if self._data_dir:
            return read_version_file_in(self._data_dir, self._name)
        return read_version_file(self._tool, self._name)

    def set_version(self, version: str) -> None:
        """No-op: the Migrator wrote the version file already."""

    def backup(self, dest: Path) -> None:
        dest = Path(dest)
        dest.mkdir(mode=0o750, parents=True, exist_ok=True)
        for p in self._paths:
            name = backup_file_name(p)
            try:
                _copy(p, dest / name)
            except OSError as e:
                raise OSError(f"backup {name}: {e}") from e

    def restore(self, src: Path) -> None:
        src = Path(src)
        for p in self._paths:
            name = backup_file_name(p)
            try:
                _copy(src / name, p)
            except OSError as e:
                raise OSError(f"restore {name}: {e}") from e
