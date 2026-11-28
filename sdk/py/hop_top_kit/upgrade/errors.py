"""Errors a Migrator run reports, and the helpers that find them in a chain.

A failed run raises :class:`MigrateError`. Its text is the vectors' text;
its ``__cause__`` is the failure that ended the schema's run and ``errors``
holds every cause (a failed rollback adds a second one). Match with
:func:`is_not_restored`, :func:`is_backup_unsupported`, :func:`is_verify`
and :func:`is_canceled`, which walk that chain: the Python counterpart of
Go's ``errors.Is`` through ``Run``.
"""

from __future__ import annotations

from collections.abc import Iterator

from hop_top_kit.upgrade.migration import VerifyError


class UpgradeError(Exception):
    """Base of every error the Migrator itself raises."""


class Canceled(UpgradeError):  # noqa: N818 - names shared with the Go and Rust ports
    """The run's cancel event was set. A lock that waits raises it."""

    def __init__(self, text: str = "context canceled") -> None:
        super().__init__(text)


class BackupUnsupported(UpgradeError):  # noqa: N818 - names shared with the Go and Rust ports
    """What a driver's ``backup`` or ``restore`` raises when kit cannot
    back up or restore its store (a server database, a file under a live
    pool). A backup that raises it is no failure: the run goes on with no
    backup. A restore that raises it restores nothing. Raise it directly or
    chain it (``raise X from BackupUnsupported(...)``): it stays matchable."""

    def __init__(self, text: str = "upgrade: backup unsupported") -> None:
        super().__init__(text)


class NotRestored(UpgradeError):  # noqa: N818 - names shared with the Go and Rust ports
    """A rollback that could not undo a failed run: a ``down`` was missing or
    failed, or an ``up`` failed (it cannot be downed), and no backup could
    restore the schema, so the stored version stays at the last step still
    applied. Its text is pinned by the vectors;
    its cause is typically :class:`BackupUnsupported`."""

    def __init__(self, text: str, cause: BaseException | None = None) -> None:
        super().__init__(text)
        self.__cause__ = cause


class MigrateError(UpgradeError):
    """A failed run or manual rollback; ``errors`` holds every cause."""

    def __init__(self, text: str, *causes: BaseException) -> None:
        super().__init__(text)
        self.errors: tuple[BaseException, ...] = causes
        self.__cause__ = causes[0] if causes else None


def wrap(prefix: str, cause: BaseException) -> MigrateError:
    """``"<prefix>: <cause>"`` with *cause* chained."""
    return MigrateError(f"{prefix}: {cause}", cause)


def walk(err: BaseException | None) -> Iterator[BaseException]:
    """*err* and every error reachable through ``errors`` and ``__cause__``."""
    seen: set[int] = set()
    todo: list[BaseException] = [err] if err is not None else []
    while todo:
        e = todo.pop(0)
        if id(e) in seen:
            continue
        seen.add(id(e))
        yield e
        todo.extend(getattr(e, "errors", ()))
        if e.__cause__ is not None:
            todo.append(e.__cause__)


def is_not_restored(err: BaseException | None) -> bool:
    """Whether *err* carries a :class:`NotRestored`."""
    return any(isinstance(e, NotRestored) for e in walk(err))


def is_backup_unsupported(err: BaseException | None) -> bool:
    """Whether *err* carries a :class:`BackupUnsupported`."""
    return any(isinstance(e, BackupUnsupported) for e in walk(err))


def is_verify(err: BaseException | None) -> bool:
    """Whether *err* carries a :class:`VerifyError`."""
    return any(isinstance(e, VerifyError) for e in walk(err))


def is_canceled(err: BaseException | None) -> bool:
    """Whether *err* carries a :class:`Canceled`."""
    return any(isinstance(e, Canceled) for e in walk(err))
