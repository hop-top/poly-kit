"""A recording driver for the Migrator tests, modelled on the Go runner's
``recordingDriver`` / ``storingDriver`` / ``lockingDriver``: every
state-changing call is logged into a list shared with the scenario's
migrations. The vector fields it understands are the ``drivers`` entries of
``contracts/upgrade-v1/migrator.json``."""

from __future__ import annotations

import logging
import threading
from collections.abc import Callable
from pathlib import Path
from typing import Any

from hop_top_kit.bus import Event, create_bus
from hop_top_kit.upgrade import (
    BackupUnsupported,
    Canceled,
    Migration,
    Migrator,
    Registry,
    UpgradeError,
    default_registry,
    read_version_file_in,
    register_migration,
    reset_registry_for_test,
)

DRIVER_FIELDS = {
    "name",
    "version",
    "backup_fail",
    "restore_fail",
    "backup_unsupported",
    "restore_unsupported",
    "version_fail",
    "set_version_fail",
    "set_version_fail_for",
    "stores_version",
    "lock",
    "lock_fail",
    "lock_blocked",
    "lock_sets_version",
    "unlock_fail",
}


def fail_if(msg: str | None) -> None:
    if msg:
        raise RuntimeError(msg)


class RecordingDriver:
    """Reports ``version`` until ``set_version`` succeeds; a ``*_fail`` field
    makes that call raise with its message."""

    def __init__(self, cfg: dict[str, Any], record: Callable[[str], None]) -> None:
        self.cfg = cfg
        self.record = record
        self.current: str = cfg.get("version", "")

    @property
    def name(self) -> str:
        return self.cfg["name"]

    def version(self) -> str:
        if self.cfg.get("lock"):
            self.record(f"{self.name} version")
        fail_if(self.cfg.get("version_fail"))
        return self.current

    def set_version(self, version: str) -> None:
        self.record(f"{self.name} set_version {version}")
        only = self.cfg.get("set_version_fail_for", "")
        if self.cfg.get("set_version_fail") and (not only or only == version):
            fail_if(self.cfg["set_version_fail"])
        self.current = version

    def backup(self, dest: Path) -> None:
        self.record(f"{self.name} backup {dest.name}")
        if self.cfg.get("backup_unsupported"):
            raise BackupUnsupported
        fail_if(self.cfg.get("backup_fail"))

    def restore(self, src: Path) -> None:
        self.record(f"{self.name} restore {src.name}")
        if self.cfg.get("restore_unsupported"):
            raise BackupUnsupported
        fail_if(self.cfg.get("restore_fail"))


class StoringDriver(RecordingDriver):
    """A driver that keeps its own version (no version file)."""

    def stores_version(self) -> bool:
        return True


class LockingDriver(RecordingDriver):
    """A :class:`~hop_top_kit.upgrade.Locker`."""

    def lock(self, cancel: threading.Event) -> Callable[[], None]:
        self.record(f"{self.name} lock")
        if self.cfg.get("lock_blocked"):
            if cancel.is_set():
                raise Canceled
            raise RuntimeError("lock would block forever: the step must set canceled")
        fail_if(self.cfg.get("lock_fail"))
        if self.cfg.get("lock_sets_version"):
            self.current = self.cfg["lock_sets_version"]

        def unlock() -> None:
            self.record(f"{self.name} unlock")
            fail_if(self.cfg.get("unlock_fail"))

        return unlock


class LockingStoringDriver(LockingDriver):
    def stores_version(self) -> bool:
        return True


def make_driver(cfg: dict[str, Any], record: Callable[[str], None]) -> RecordingDriver:
    lock, stores = cfg.get("lock", False), cfg.get("stores_version", False)
    cls = {
        (True, True): LockingStoringDriver,
        (True, False): LockingDriver,
        (False, True): StoringDriver,
        (False, False): RecordingDriver,
    }[(lock, stores)]
    return cls(cfg, record)


# -- scenario execution -------------------------------------------------------

TOOL = "conftool"


class _Capture(logging.Handler):
    """What the run logged, as ``<level>: <message>`` lines."""

    def __init__(self) -> None:
        super().__init__(logging.DEBUG)
        self.lines: list[str] = []

    def emit(self, record: logging.LogRecord) -> None:
        self.lines.append(f"{record.levelname.lower()}: {record.getMessage()}")


def _list_backups(base: Path, schema: str) -> list[str]:
    d = base / "backups" / schema
    return sorted(e.name for e in d.iterdir() if e.is_dir()) if d.is_dir() else []


def _list_pre_run(base: Path, schema: str) -> dict[str, str]:
    d = base / "backups" / schema
    if not d.is_dir():
        return {}
    return {
        e.name.removesuffix(".version"): e.read_text()
        for e in d.iterdir()
        if e.is_file() and e.name.endswith(".version")
    }


def _mk_backups(base: Path, spec: dict[str, list[str]], locked: bool, cleanup: list[Path]) -> None:
    for schema, dirs in spec.items():
        (base / "backups" / schema).mkdir(parents=True, exist_ok=True)
        for name in dirs:
            d = base / "backups" / schema / name
            if not locked:
                d.mkdir(parents=True, exist_ok=True)
                continue
            inner = d / "locked"
            inner.mkdir(parents=True)
            (inner / "f").write_bytes(b"")
            inner.chmod(0o500)
            cleanup.append(inner)


def run_scenario(sc: dict[str, Any], tmp: Path, monkeypatch: Any) -> None:
    """Execute one ``migrator.json`` scenario; asserts on any difference."""
    reset_registry_for_test()
    monkeypatch.setenv("XDG_DATA_HOME", str(tmp / "xdg"))
    xdg_base = tmp / "xdg" / "hop" / TOOL
    base = tmp / "custom" if sc.get("data_dir") else xdg_base
    cleanup: list[Path] = []
    try:
        _run(sc, base, xdg_base, cleanup)
    finally:
        for p in cleanup:
            p.chmod(0o750)
        reset_registry_for_test()


def _run(sc: dict[str, Any], base: Path, xdg_base: Path, cleanup: list[Path]) -> None:
    for schema, recorded in sc.get("recorded_backups", {}).items():
        d = base / "backups" / schema
        d.mkdir(parents=True, exist_ok=True)
        for name, pre in recorded.items():
            (d / name).mkdir(parents=True, exist_ok=True)
            (d / (name + ".version")).write_text(pre)
    _mk_backups(base, sc.get("backups", {}), False, cleanup)
    _mk_backups(base, sc.get("locked_backups", {}), True, cleanup)

    calls: list[str] = []
    record = calls.append
    cancel = threading.Event()

    def build(mc: dict[str, Any]) -> Migration:
        schema, version = mc["schema"], mc["version"]

        def up() -> None:
            record(f"{schema} up {version}")
            if mc.get("cancel"):
                cancel.set()
            fail_if(mc.get("fail"))

        def down() -> None:
            record(f"{schema} down {version}")
            fail_if(mc.get("down_fail"))

        def verify() -> None:
            record(f"{schema} verify {version}")
            fail_if(mc.get("verify_fail"))

        return Migration(
            version=version,
            schema=schema,
            up=up,
            down=down if mc.get("down") or mc.get("down_fail") else None,
            verify=verify if mc.get("verify") or mc.get("verify_fail") else None,
        )

    kw: dict[str, Any] = {}
    reg = default_registry()
    if sc.get("registry") == "own":
        reg = Registry()
        kw["registry"] = reg
        for mc in sc.get("default_migrations", []):
            register_migration(build(mc))
    for mc in sc["migrations"]:
        reg.register(build(mc))
    if sc.get("manual_rollback"):
        kw["manual_rollback"] = True
    if sc.get("retention"):
        kw["backup_retention"] = sc["retention"]
    if sc.get("data_dir"):
        kw["data_dir"] = base
    if sc.get("final_verify") or sc.get("final_verify_fail"):

        def final_verify(schema: str, version: str) -> None:
            record(f"{schema} final_verify {version}")
            fail_if(sc.get("final_verify_fail"))

        kw["verify"] = final_verify
    if "baseline" in sc:

        def baseline(schema: str) -> str:
            record(f"{schema} baseline")
            b = sc["baseline"].get(schema, {})
            fail_if(b.get("error"))
            return b.get("version", "")

        kw["baseline"] = baseline

    events: list[dict[str, Any]] = []
    problems: list[str] = []

    def on_event(e: Event) -> None:
        if e.source != "core.upgrade":
            problems.append(f"source = {e.source!r}")
        payload = dict(e.payload)
        if "duration_ms" in payload:
            d = payload.pop("duration_ms")
            if not isinstance(d, int) or isinstance(d, bool) or d < 0:
                problems.append(f"{e.topic}: duration_ms = {d!r}")
        events.append({"topic": e.topic, "payload": payload})

    bus = create_bus()
    bus.subscribe("#", on_event)
    m = Migrator(TOOL, sc["target"], bus=bus, **kw)
    for dc in sc["drivers"]:
        m.add_driver(make_driver(dc, record))

    capture = _Capture()
    logger = logging.getLogger("hop_top_kit.upgrade")
    logger.addHandler(capture)
    prev_level = logger.level
    logger.setLevel(logging.DEBUG)
    try:
        for i, st in enumerate(sc["steps"]):
            calls.clear()
            events.clear()
            capture.lines.clear()
            _step(sc, i, st, m, cancel, base, xdg_base, calls, events, capture, problems)
    finally:
        logger.removeHandler(capture)
        logger.setLevel(prev_level)


def _step(
    sc: dict[str, Any],
    i: int,
    st: dict[str, Any],
    m: Migrator,
    cancel: threading.Event,
    base: Path,
    xdg_base: Path,
    calls: list[str],
    events: list[dict[str, Any]],
    capture: _Capture,
    problems: list[str],
) -> None:
    where = f"{sc['name']} step {i}"
    run_cancel = cancel
    if st.get("canceled"):
        run_cancel = threading.Event()
        run_cancel.set()
    err: Exception | None = None
    try:
        if st["op"] == "run":
            m.run(run_cancel)
        elif st["op"] == "rollback_latest":
            m.rollback_latest()
        elif st["op"] == "status":
            got = [
                {
                    "schema": s.schema,
                    "current": s.current,
                    "target": s.target,
                    "pending": s.pending,
                    "error": None if s.error is None else str(s.error),
                }
                for s in m.status()
            ]
            assert got == st["status"], f"{where}: status"
        else:
            raise AssertionError(f"{where}: unknown op {st['op']!r}")
    except UpgradeError as e:
        err = e
    want = st.get("error")
    assert (str(err) if err else None) == want, f"{where}: error {str(err)!r}, want {want!r}"
    assert not problems, f"{where}: {problems}"
    if "calls" in st:
        assert calls == st["calls"], f"{where}: calls"
    if "history" in st:
        got_h = [{"schema": h.schema, "version": h.version} for h in m.history()]
        assert got_h == st["history"], f"{where}: history"
    if "events" in st:
        assert events == st["events"], f"{where}: events"
    if "warnings" in st:
        lines = capture.lines
        assert len(lines) == len(st["warnings"]), f"{where}: logged {lines}"
        for line, prefix in zip(lines, st["warnings"], strict=True):
            assert line.startswith(prefix), f"{where}: logged {line!r}, want prefix {prefix!r}"
    for schema, want_v in st.get("versions", {}).items():
        got_v = read_version_file_in(base, schema)
        assert got_v == want_v, f"{where}: {schema} version file {got_v!r}, want {want_v!r}"
    for schema, want_b in st.get("backups", {}).items():
        assert _list_backups(base, schema) == want_b, f"{where}: {schema} backups"
    if "stored" in st:
        stored = {s.schema: s.current for s in m.status()}
        for schema, want_v in st["stored"].items():
            assert stored.get(schema, "") == want_v, f"{where}: {schema} stored"
    for schema, want_p in st.get("pre_run_versions", {}).items():
        assert _list_pre_run(base, schema) == want_p, f"{where}: {schema} pre-run versions"
    for schema in st.get("no_version_dir", []):
        assert not (base / schema).exists(), f"{where}: {schema} has a version dir"
    if sc.get("data_dir"):
        assert not xdg_base.exists(), f"{where}: XDG data dir touched despite data_dir"
