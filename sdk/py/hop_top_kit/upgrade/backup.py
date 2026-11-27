"""Backup bookkeeping for one schema: the ``pre-<version>`` directories
under ``<data dir>/backups/<schema>``, the ``pre-<version>.version`` file
beside each (the version the run started at), the latest backup and prune."""

from __future__ import annotations

import functools
import shutil
from pathlib import Path

from hop_top_kit.upgrade.errors import BackupUnsupported, NotRestored
from hop_top_kit.upgrade.semver import Semver, compare_semver, parse_semver

DEFAULT_RETENTION = 5
_PRE = "pre-"
_SUFFIX = ".version"


def _key(dirname: str) -> Semver:
    return parse_semver(dirname.removeprefix(_PRE).removeprefix("v"))


def _cmp(a: str, b: str) -> int:
    return compare_semver(_key(a), _key(b))


class BackupStore:
    """The backups of one schema."""

    def __init__(self, base: Path, schema: str, retention: int) -> None:
        self.schema = schema
        self.retention = retention if retention > 0 else DEFAULT_RETENTION
        self.dir = base / "backups" / schema

    def has_backup(self, version: str) -> bool:
        return (self.dir / (_PRE + version)).exists()

    def backup_dir(self, version: str) -> Path:
        """Create and return ``pre-<version>``; raises :class:`OSError`."""
        path = self.dir / (_PRE + version)
        path.mkdir(mode=0o750, parents=True, exist_ok=True)
        return path

    @staticmethod
    def save_pre_run(backup: Path, version: str) -> None:
        """Record, beside *backup*, the version the run started at."""
        Path(str(backup) + _SUFFIX).write_text(version)

    @staticmethod
    def read_pre_run(backup: Path) -> tuple[str, bool]:
        """The version saved beside *backup*, and whether one was saved."""
        try:
            return Path(str(backup) + _SUFFIX).read_text().strip(), True
        except FileNotFoundError:
            return "", False

    def _dirs(self) -> list[str]:
        try:
            entries = list(self.dir.iterdir())
        except FileNotFoundError:
            return []
        return [e.name for e in entries if e.is_dir() and e.name.startswith(_PRE)]

    def latest_backup(self) -> Path:
        """The newest backup directory by version; :class:`NotRestored`
        (cause :class:`BackupUnsupported`) when there is none."""
        names = sorted(self._dirs(), key=functools.cmp_to_key(_cmp), reverse=True)
        if not names:
            raise NotRestored(f"no backups found for {self.schema}", BackupUnsupported())
        return self.dir / names[0]

    def prune(self) -> None:
        """Keep the newest ``retention`` backups, removing the rest and
        their ``.version`` files; raises :class:`OSError`-based text."""
        names = sorted(self._dirs(), key=functools.cmp_to_key(_cmp))
        if len(names) <= self.retention:
            return
        for name in names[: len(names) - self.retention]:
            p = self.dir / name
            try:
                shutil.rmtree(p)
                Path(str(p) + _SUFFIX).unlink(missing_ok=True)
            except OSError as e:
                raise OSError(f"prune backup {name}: {e}") from e
