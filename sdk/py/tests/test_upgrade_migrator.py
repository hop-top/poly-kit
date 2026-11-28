"""Migrator behaviour the shared vectors cannot reach: real thread
concurrency on a lockable driver, lock release on exception types the run
does not handle, error matching, event delivery and cancellation."""

from __future__ import annotations

import threading
from collections.abc import Callable
from pathlib import Path

import pytest

from hop_top_kit.bus import Event, create_bus
from hop_top_kit.upgrade import (
    BackupUnsupported,
    Canceled,
    Locker,
    MigrateError,
    Migration,
    MigrationTopics,
    Migrator,
    NotRestored,
    Registry,
    SchemaDriver,
    VersionStorer,
    is_backup_unsupported,
    is_canceled,
    is_not_restored,
    is_verify,
    read_version_file_in,
)

TOOL = "unit"


class MemDriver:
    """A version-storing, lockable driver over shared state: the version
    lives in ``state`` and ``mutex`` is the cross-process lock's stand-in."""

    def __init__(self, name: str, state: dict[str, str], mutex: threading.Lock) -> None:
        self.name = name
        self.state = state
        self.mutex = mutex
        self.log: list[str] = []

    def version(self) -> str:
        return self.state["v"]

    def set_version(self, version: str) -> None:
        self.state["v"] = version

    def stores_version(self) -> bool:
        return True

    def backup(self, dest: Path) -> None:
        raise BackupUnsupported

    def restore(self, src: Path) -> None:
        raise BackupUnsupported

    def lock(self, cancel: threading.Event) -> Callable[[], None]:
        while not self.mutex.acquire(timeout=0.01):
            if cancel.is_set():
                raise Canceled
        self.log.append("lock")

        def unlock() -> None:
            self.log.append("unlock")
            self.mutex.release()

        return unlock


def _registry(*migs: Migration) -> Registry:
    reg = Registry()
    for m in migs:
        reg.register(m)
    return reg


def test_protocols() -> None:
    d = MemDriver("db", {"v": ""}, threading.Lock())
    assert isinstance(d, SchemaDriver)
    assert isinstance(d, Locker)
    assert isinstance(d, VersionStorer)


def test_second_migrator_waits_for_the_lock_then_finds_nothing_pending(tmp_path: Path) -> None:
    state = {"v": ""}
    mutex = threading.Lock()
    ups: list[str] = []
    inside, release = threading.Event(), threading.Event()

    def slow_up() -> None:
        ups.append("a")
        inside.set()
        assert release.wait(5), "test never released the first migrator"

    def other_up() -> None:
        ups.append("b")

    a_driver = MemDriver("db", state, mutex)
    b_driver = MemDriver("db", state, mutex)
    a = Migrator(
        TOOL,
        "1.1.0",
        data_dir=tmp_path / "a",
        registry=_registry(Migration("1.1.0", "db", slow_up)),
    )
    b = Migrator(
        TOOL,
        "1.1.0",
        data_dir=tmp_path / "b",
        registry=_registry(Migration("1.1.0", "db", other_up)),
    )
    a.add_driver(a_driver)
    b.add_driver(b_driver)

    errors: list[BaseException] = []

    def go(m: Migrator) -> None:
        try:
            m.run()
        except BaseException as e:
            errors.append(e)

    ta = threading.Thread(target=go, args=(a,))
    ta.start()
    assert inside.wait(5)
    tb = threading.Thread(target=go, args=(b,))
    tb.start()
    tb.join(0.3)
    assert tb.is_alive(), "second migrator must wait while the first holds the lock"
    assert b_driver.log == [], "second migrator must not hold the lock yet"
    release.set()
    ta.join(5)
    tb.join(5)
    assert not ta.is_alive()
    assert not tb.is_alive()
    assert errors == []
    assert ups == ["a"], "the second migrator found nothing pending"
    assert state["v"] == "1.1.0"
    assert a_driver.log == ["lock", "unlock"]
    assert b_driver.log == ["lock", "unlock"]
    assert [h.version for h in a.history()] == ["1.1.0"]
    assert b.history() == []


class _Unexpected(BaseException):
    """Not an Exception: nothing in a run handles it."""


@pytest.mark.parametrize("exc", [_Unexpected, KeyboardInterrupt, SystemExit])
def test_lock_released_when_migration_raises_unexpected_type(
    tmp_path: Path, exc: type[BaseException]
) -> None:
    state = {"v": ""}
    mutex = threading.Lock()
    d = MemDriver("db", state, mutex)

    def up() -> None:
        raise exc("stop")

    m = Migrator(TOOL, "1.1.0", data_dir=tmp_path, registry=_registry(Migration("1.1.0", "db", up)))
    m.add_driver(d)
    with pytest.raises(exc):
        m.run()
    assert d.log == ["lock", "unlock"]
    assert mutex.acquire(blocking=False), "lock still held after the run"
    mutex.release()
    assert state["v"] == ""


def test_lock_released_when_migration_raises_ordinary_exception(tmp_path: Path) -> None:
    mutex = threading.Lock()
    d = MemDriver("db", {"v": ""}, mutex)

    def up() -> None:
        raise ValueError("boom")

    m = Migrator(TOOL, "1.1.0", data_dir=tmp_path, registry=_registry(Migration("1.1.0", "db", up)))
    m.add_driver(d)
    # MemDriver has no backup, so the failed up ends not restored.
    with pytest.raises(
        MigrateError,
        match=r"^migrate db: up 1\.1\.0 failed: boom \(rollback also failed: not restored: no backup was taken\)$",
    ) as ei:
        m.run()
    assert isinstance(ei.value.__cause__, MigrateError)
    assert d.log == ["lock", "unlock"]
    assert mutex.acquire(blocking=False)
    mutex.release()


def test_blocked_lock_wakes_on_cancel_from_another_thread(tmp_path: Path) -> None:
    mutex = threading.Lock()
    mutex.acquire()  # another process holds it
    d = MemDriver("db", {"v": ""}, mutex)
    m = Migrator(
        TOOL, "1.1.0", data_dir=tmp_path, registry=_registry(Migration("1.1.0", "db", lambda: None))
    )
    m.add_driver(d)
    cancel = threading.Event()
    threading.Timer(0.1, cancel.set).start()
    with pytest.raises(MigrateError) as ei:
        m.run(cancel)
    assert str(ei.value) == "migrate db: lock: context canceled"
    assert is_canceled(ei.value)
    assert d.log == []


class _Plain:
    """No capabilities: a version file driver."""

    name = "db"

    def __init__(self, version: str = "1.0.0") -> None:
        self._v = version
        self.restored: list[str] = []
        self.unsupported_restore = False

    def version(self) -> str:
        return self._v

    def set_version(self, version: str) -> None:
        self._v = version

    def backup(self, dest: Path) -> None:
        (dest / "snap").write_text(self._v)

    def restore(self, src: Path) -> None:
        if self.unsupported_restore:
            raise BackupUnsupported
        self.restored.append(src.name)


def test_not_restored_and_backup_unsupported_match_through_the_run_error(tmp_path: Path) -> None:
    d = _Plain()

    def up() -> None:
        raise RuntimeError("boom")

    # 1.1.0 applies and has no down, so undoing it needs the backup; 1.2.0 fails.
    reg = _registry(Migration("1.1.0", "db", lambda: None), Migration("1.2.0", "db", up))
    m = Migrator(TOOL, "1.2.0", data_dir=tmp_path, registry=reg)
    m.add_driver(d)
    d.unsupported_restore = True
    with pytest.raises(MigrateError) as ei:
        m.run()
    assert str(ei.value) == (
        "migrate db: up 1.2.0 failed: boom (rollback also failed: not restored: restore unsupported)"
    )
    assert is_not_restored(ei.value)
    assert is_backup_unsupported(ei.value)
    assert not is_verify(ei.value)
    assert isinstance(ei.value.__cause__, MigrateError)
    nr = [e for e in ei.value.__cause__.errors if isinstance(e, NotRestored)]
    assert nr, "NotRestored is among the causes"


def test_failed_up_restores_the_backup_and_calls_no_down(tmp_path: Path) -> None:
    d = _Plain()
    downs: list[str] = []

    def up() -> None:
        raise RuntimeError("boom")

    reg = _registry(
        Migration("1.1.0", "db", lambda: None, lambda: downs.append("1.1.0")),
        Migration("1.2.0", "db", up, lambda: downs.append("1.2.0")),
    )
    bus, seen = create_bus(), []
    bus.subscribe("#", seen.append)
    m = Migrator(TOOL, "1.2.0", data_dir=tmp_path, registry=reg, bus=bus)
    m.add_driver(d)
    with pytest.raises(MigrateError, match=r"^migrate db: up 1\.2\.0 failed: boom$") as ei:
        m.run()
    assert not is_not_restored(ei.value)
    assert downs == []  # neither the failed step's down nor an earlier one
    assert d.restored == ["pre-1.1.0"]
    assert d.version() == "1.0.0"
    reverted = [e.payload["method"] for e in seen if e.topic.endswith(".reverted")]
    assert reverted == ["restore"]


def test_up_failing_with_the_cancel_error_restores_and_calls_no_down(tmp_path: Path) -> None:
    d = _Plain()
    downs: list[str] = []
    cancel = threading.Event()

    def up() -> None:
        cancel.set()
        raise Canceled

    reg = _registry(Migration("1.1.0", "db", up, lambda: downs.append("1.1.0")))
    m = Migrator(TOOL, "1.1.0", data_dir=tmp_path, registry=reg)
    m.add_driver(d)
    with pytest.raises(MigrateError) as ei:
        m.run(cancel)
    assert is_canceled(ei.value)
    assert not is_not_restored(ei.value)
    assert downs == []
    assert d.restored == ["pre-1.1.0"]
    assert d.version() == "1.0.0"


def test_failed_up_without_a_backup_is_not_restored_and_calls_no_down(tmp_path: Path) -> None:
    class NoBackup(_Plain):
        def backup(self, dest: Path) -> None:
            raise BackupUnsupported

    d = NoBackup()
    downs: list[str] = []

    def up() -> None:
        raise RuntimeError("boom")

    reg = _registry(
        Migration("1.1.0", "db", lambda: None, lambda: downs.append("1.1.0")),
        Migration("1.2.0", "db", up, lambda: downs.append("1.2.0")),
    )
    bus, seen = create_bus(), []
    bus.subscribe("#", seen.append)
    m = Migrator(TOOL, "1.2.0", data_dir=tmp_path, registry=reg, bus=bus)
    m.add_driver(d)
    with pytest.raises(MigrateError) as ei:
        m.run()
    assert str(ei.value) == (
        "migrate db: up 1.2.0 failed: boom (rollback also failed: not restored: no backup was taken)"
    )
    assert is_not_restored(ei.value)
    assert downs == []
    assert d.restored == []
    assert d.version() == "1.1.0"  # the step before the failing one
    assert not [e for e in seen if e.topic.endswith(".reverted")]


def test_failed_final_verify_still_rolls_back_with_downs_newest_first(tmp_path: Path) -> None:
    d = _Plain()
    downs: list[str] = []
    reg = _registry(
        Migration("1.1.0", "db", lambda: None, lambda: downs.append("1.1.0")),
        Migration("1.2.0", "db", lambda: None, lambda: downs.append("1.2.0")),
    )

    def final(schema: str, version: str) -> None:
        raise RuntimeError("bad")

    m = Migrator(TOOL, "1.2.0", data_dir=tmp_path, registry=reg, verify=final)
    m.add_driver(d)
    with pytest.raises(MigrateError, match=r"final verify 1\.2\.0 failed"):
        m.run()
    assert downs == ["1.2.0", "1.1.0"]
    assert d.restored == []
    assert d.version() == "1.0.0"


def test_verify_failure_matches_is_verify_and_keeps_the_original_cause(tmp_path: Path) -> None:
    d = _Plain()
    original = RuntimeError("bad")

    def verify() -> None:
        raise original

    mig = Migration("1.1.0", "db", lambda: None, lambda: None, verify)
    m = Migrator(TOOL, "1.1.0", data_dir=tmp_path, registry=_registry(mig))
    m.add_driver(d)
    with pytest.raises(MigrateError) as ei:
        m.run()
    assert str(ei.value) == "migrate db: verify 1.1.0 failed: bad"
    assert is_verify(ei.value)
    assert not is_not_restored(ei.value)
    assert d.version() == "1.0.0"
    assert read_version_file_in(tmp_path, "db") == "1.0.0"


def test_events_go_through_the_kit_bus_with_custom_topics(tmp_path: Path) -> None:
    bus = create_bus()
    seen: list[Event] = []
    bus.subscribe("app.core.migration.#", seen.append)
    m = Migrator(
        TOOL,
        "v1.1.0",
        data_dir=tmp_path,
        bus=bus,
        topics=MigrationTopics.with_prefix("app.core.migration"),
        registry=_registry(Migration("1.1.0", "db", lambda: None)),
    )
    assert m.version == "1.1.0"
    m.add_driver(_Plain())
    m.run()
    assert [e.topic for e in seen] == [
        "app.core.migration.run_started",
        "app.core.migration.started",
        "app.core.migration.applied",
        "app.core.migration.run_completed",
    ]
    assert {e.source for e in seen} == {"core.upgrade"}
    assert seen[-1].payload["applied"] == 1


def test_a_failing_subscriber_never_fails_the_run(tmp_path: Path) -> None:
    bus = create_bus()

    def boom(_: Event) -> None:
        raise RuntimeError("subscriber")

    bus.subscribe("#", boom)
    d = _Plain()
    m = Migrator(
        TOOL,
        "1.1.0",
        data_dir=tmp_path,
        bus=bus,
        registry=_registry(Migration("1.1.0", "db", lambda: None)),
    )
    m.add_driver(d)
    m.run()
    assert d.version() == "1.1.0"


def test_topic_prefix_must_have_three_segments() -> None:
    with pytest.raises(ValueError):
        MigrationTopics.with_prefix("app.migration")
    assert MigrationTopics().replace(applied="x.y.z", failed="").failed == (
        "kit.core.migration.failed"
    )


def test_rollback_latest_needs_manual_mode(tmp_path: Path) -> None:
    m = Migrator(TOOL, "1.0.0", data_dir=tmp_path)
    with pytest.raises(MigrateError, match="only available in manual rollback mode"):
        m.rollback_latest()
