"""The configfile and fsdir upgrade drivers: backup layout shared with the
Go driver, restore, the archive traversal guard and a failing migration
restored end to end through the Migrator."""

from __future__ import annotations

import gzip
import io
import os
import shutil
import tarfile
from collections.abc import Callable
from pathlib import Path

import pytest

from hop_top_kit.upgrade import (
    Locker,
    MigrateError,
    Migration,
    Migrator,
    Registry,
    SchemaDriver,
    VersionStorer,
    read_version_file_in,
)
from hop_top_kit.upgrade.drivers import ConfigFileDriver, FsDirDriver, fsdir
from hop_top_kit.upgrade.drivers.configfile import backup_file_name
from hop_top_kit.upgrade.drivers.fsdir import UnsafeArchiveError

GO_FIXTURE = Path(__file__).parent / "testdata" / "upgrade_fsdir_go.tar.gz"
POSIX = os.name == "posix"


def _read(p: Path) -> str:
    return p.read_text()


# ---------------------------------------------------------------- configfile


def test_configfile_is_a_plain_schema_driver(tmp_path: Path) -> None:
    d = ConfigFileDriver([tmp_path / "a"], name="cfg")
    assert isinstance(d, SchemaDriver)
    assert not isinstance(d, Locker)
    assert not isinstance(d, VersionStorer)
    assert d.name == "cfg"
    assert ConfigFileDriver([]).name == "config"


@pytest.mark.skipif(not POSIX, reason="posix path rules")
@pytest.mark.parametrize(
    ("path", "want"),
    [
        ("/etc/foo/config.yaml", "_etc_foo_config.yaml"),
        ("config.yaml", "config.yaml"),
        ("./a//b/../c.yml", "a_c.yml"),
        ("//srv/x", "_srv_x"),
        ("a/../..", ".."),
        ("", "."),
    ],
)
def test_backup_file_name_matches_go(path: str, want: str) -> None:
    assert backup_file_name(path) == want


def test_configfile_round_trip(tmp_path: Path) -> None:
    a, b = tmp_path / "etc" / "a.yaml", tmp_path / "b.json"
    a.parent.mkdir()
    a.write_text("one")
    b.write_text("two")
    d = ConfigFileDriver([a, b])
    bk = tmp_path / "bk" / "pre-1.0.0"
    d.backup(bk)
    assert sorted(p.name for p in bk.iterdir()) == sorted(
        [backup_file_name(a), backup_file_name(b)]
    )
    assert _read(bk / backup_file_name(a)) == "one"
    a.write_text("changed")
    b.unlink()
    d.restore(bk)
    assert (_read(a), _read(b)) == ("one", "two")


def test_configfile_skips_missing_files(tmp_path: Path) -> None:
    present, absent = tmp_path / "present", tmp_path / "gone" / "absent"
    present.write_text("x")
    d = ConfigFileDriver([present, absent])
    bk = tmp_path / "bk"
    d.backup(bk)
    assert [p.name for p in bk.iterdir()] == [backup_file_name(present)]
    present.write_text("y")
    d.restore(bk)  # the file missing from the backup is left alone, no error
    assert _read(present) == "x"
    assert not absent.exists()


def test_configfile_restore_recreates_parent_and_keeps_mode(tmp_path: Path) -> None:
    f = tmp_path / "deep" / "c.conf"
    f.parent.mkdir()
    f.write_text("secret")
    f.chmod(0o600)
    d = ConfigFileDriver([f])
    bk = tmp_path / "bk"
    d.backup(bk)
    if POSIX:
        assert (bk / backup_file_name(f)).stat().st_mode & 0o777 == 0o600
    f.unlink()
    f.parent.rmdir()
    d.restore(bk)
    assert _read(f) == "secret"
    if POSIX:
        assert f.stat().st_mode & 0o777 == 0o600


def test_configfile_backup_error_names_the_file(tmp_path: Path) -> None:
    d = ConfigFileDriver([tmp_path])  # a directory is not a file
    with pytest.raises(OSError, match=r"^backup "):
        d.backup(tmp_path / "bk")


def test_configfile_version_comes_from_the_version_file(tmp_path: Path) -> None:
    d = ConfigFileDriver([], name="cfg", data_dir=tmp_path)
    assert d.version() == ""
    (tmp_path / "cfg").mkdir()
    (tmp_path / "cfg" / "version").write_text("1.2.3\n")
    assert d.version() == "1.2.3"
    d.set_version("9.9.9")  # the Migrator owns the file
    assert d.version() == "1.2.3"


def test_configfile_version_by_tool(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("XDG_DATA_HOME", str(tmp_path))
    d = ConfigFileDriver([], name="cfg", tool="demo")
    assert d.version() == ""
    vdir = tmp_path / "hop" / "demo" / "cfg"
    vdir.mkdir(parents=True)
    (vdir / "version").write_text("0.4.0")
    assert d.version() == "0.4.0"


# --------------------------------------------------------------------- fsdir


def _tree(root: Path) -> None:
    (root / "sub" / "emptydir").mkdir(parents=True)
    (root / "sub2").mkdir()
    (root / "file.txt").write_text("hello")
    (root / "exec.sh").write_text("#!/bin/sh\n")
    (root / "sub" / "nested.txt").write_text("world")
    if POSIX:
        (root / "file.txt").chmod(0o600)
        (root / "exec.sh").chmod(0o755)
        (root / "sub").chmod(0o750)
        (root / "sub" / "emptydir").chmod(0o750)
        (root / "sub2").chmod(0o700)


def _snapshot(root: Path) -> dict[str, tuple[str, str | None]]:
    out: dict[str, tuple[str, str | None]] = {}
    for p in sorted(root.rglob("*")):
        rel = p.relative_to(root).as_posix()
        if p.is_symlink():
            out[rel] = ("link", os.readlink(p))
        elif p.is_dir():
            out[rel] = ("dir", None)
        else:
            out[rel] = ("file", p.read_text())
    return out


def _entries(archive: Path) -> list[tuple[str, bytes, int]]:
    with tarfile.open(archive, "r:gz") as t:
        return [(m.name, m.type, m.mode) for m in t.getmembers()]


def test_fsdir_is_a_plain_schema_driver(tmp_path: Path) -> None:
    d = FsDirDriver("data", tmp_path)
    assert isinstance(d, SchemaDriver)
    assert not isinstance(d, Locker)
    assert not isinstance(d, VersionStorer)
    assert d.name == "data"


def test_fsdir_backup_layout_is_gos(tmp_path: Path) -> None:
    root = tmp_path / "mydata"
    root.mkdir()
    _tree(root)
    bk = tmp_path / "bk"
    FsDirDriver("mydata", root).backup(bk)
    assert [p.name for p in bk.iterdir()] == ["mydata.tar.gz"]
    with open(bk / "mydata.tar.gz", "rb") as f:
        assert f.read(2) == b"\x1f\x8b"
    got = _entries(bk / "mydata.tar.gz")
    d, r = tarfile.DIRTYPE, tarfile.REGTYPE
    assert [(n, t) for n, t, _ in got] == [
        (".", d),
        ("exec.sh", r),
        ("file.txt", r),
        ("sub", d),
        ("sub/emptydir", d),
        ("sub/nested.txt", r),
        ("sub2", d),
    ]
    if POSIX:
        assert {n: m for n, _, m in got}["exec.sh"] == 0o755
        assert {n: m for n, _, m in got}["file.txt"] == 0o600
        assert {n: m for n, _, m in got}["sub2"] == 0o700


def test_fsdir_round_trip(tmp_path: Path) -> None:
    root = tmp_path / "mydata"
    root.mkdir()
    _tree(root)
    want = _snapshot(root)
    d = FsDirDriver("mydata", root)
    bk = tmp_path / "bk"
    d.backup(bk)
    (root / "file.txt").write_text("changed")
    (root / "extra.txt").write_text("new")
    (root / "sub" / "emptydir").rmdir()
    d.restore(bk)
    assert _snapshot(root) == want
    if POSIX:
        assert (root / "exec.sh").stat().st_mode & 0o777 == 0o755
        assert (root / "file.txt").stat().st_mode & 0o777 == 0o600
        assert (root / "sub2").stat().st_mode & 0o777 == 0o700
    siblings = [p.name for p in tmp_path.iterdir()]
    assert sorted(siblings) == ["bk", "mydata"], "no temp directory left beside the target"


def test_fsdir_backup_of_missing_dir_is_nothing(tmp_path: Path) -> None:
    d = FsDirDriver("gone", tmp_path / "gone")
    bk = tmp_path / "bk"
    d.backup(bk)
    assert list(bk.iterdir()) == []
    with pytest.raises(FileNotFoundError, match="backup archive not found"):
        d.restore(bk)


def test_fsdir_restore_creates_missing_target_and_parent(tmp_path: Path) -> None:
    root = tmp_path / "mydata"
    root.mkdir()
    _tree(root)
    bk = tmp_path / "bk"
    FsDirDriver("mydata", root).backup(bk)
    want = _snapshot(root)
    shutil.rmtree(root)
    target = tmp_path / "new" / "place" / "mydata"
    FsDirDriver("mydata", target).restore(bk)
    assert _snapshot(target) == want


@pytest.mark.skipif(not POSIX or not hasattr(tarfile, "data_filter"), reason="symlinks, filters")
def test_fsdir_keeps_internal_symlinks(tmp_path: Path) -> None:
    root = tmp_path / "mydata"
    (root / "sub").mkdir(parents=True)
    (root / "file.txt").write_text("hello")
    (root / "sub" / "up").symlink_to("../file.txt")
    d = FsDirDriver("mydata", root)
    bk = tmp_path / "bk"
    d.backup(bk)
    (root / "sub" / "up").unlink()
    d.restore(bk)
    assert os.readlink(root / "sub" / "up") == "../file.txt"


# A link the restore check refuses must not be backed up: a rollback would fail
# at the worst moment. ``(where, target)``: the link sits at ``where``.
UNRESTORABLE_LINKS = {
    "absolute": ("l", "{abs}"),
    "up-from-root": ("l", "../outside"),
    "up-from-nested": ("sub/l", "../../outside"),
    "up-after-name": ("l", "sub/../../outside"),
    "named-then-up": ("l", "sub/.."),
}

# Every link kind the restore check accepts: it still backs up and round-trips.
RESTORABLE_LINKS = {
    "sibling-file": ("l", "file.txt"),
    "up-then-down": ("sub/l", "../file.txt"),
    "to-dir": ("l", "sub"),
    "to-root": ("l", "."),
    "dangling": ("l", "missing"),
    "to-link": ("l", "other"),
}


def _link_tree(tmp_path: Path, where: str, target: str) -> Path:
    root = tmp_path / "mydata"
    (root / "sub").mkdir(parents=True)
    (root / "file.txt").write_text("hello")
    (root / "other").symlink_to("file.txt")
    (root / where).symlink_to(target.replace("{abs}", str(tmp_path / "outside")))
    return root


@pytest.mark.skipif(not POSIX or not hasattr(tarfile, "data_filter"), reason="symlinks, filters")
@pytest.mark.parametrize("case", sorted(UNRESTORABLE_LINKS))
def test_fsdir_backup_refuses_a_link_its_restore_would_refuse(tmp_path: Path, case: str) -> None:
    where, target = UNRESTORABLE_LINKS[case]
    root = _link_tree(tmp_path, where, target)
    bk = tmp_path / "bk"
    with pytest.raises(UnsafeArchiveError, match=f"{root / where}"):
        FsDirDriver("mydata", root).backup(bk)
    assert list(bk.iterdir()) == [], "no archive, no partial file"


@pytest.mark.skipif(not POSIX or not hasattr(tarfile, "data_filter"), reason="symlinks, filters")
@pytest.mark.parametrize("case", sorted(RESTORABLE_LINKS))
def test_fsdir_backup_keeps_every_link_its_restore_accepts(tmp_path: Path, case: str) -> None:
    where, target = RESTORABLE_LINKS[case]
    root = _link_tree(tmp_path, where, target)
    want = _snapshot(root)
    d = FsDirDriver("mydata", root)
    bk = tmp_path / "bk"
    d.backup(bk)
    assert [p.name for p in bk.iterdir()] == ["mydata.tar.gz"]
    os.unlink(root / where)
    (root / "file.txt").write_text("changed")
    d.restore(bk)
    assert os.readlink(root / where) == target
    assert _snapshot(root) == want


@pytest.mark.skipif(not POSIX, reason="fifo")
def test_fsdir_backup_refuses_special_files_and_leaves_no_partial_archive(
    tmp_path: Path,
) -> None:
    root = tmp_path / "mydata"
    root.mkdir()
    (root / "ok.txt").write_text("x")
    os.mkfifo(root / "pipe")
    bk = tmp_path / "bk"
    with pytest.raises(OSError, match="unsupported file type"):
        FsDirDriver("mydata", root).backup(bk)
    assert list(bk.iterdir()) == []


# --- cross-port: a tar laid out exactly as the Go driver writes it


def _go_layout(path: Path, entries: list[tuple[str, bytes | None, int]]) -> None:
    """What Go's ``filepath.Walk`` + ``tar.FileInfoHeader`` produce: USTAR
    headers, root as ``.``, names relative with no trailing slash, parents
    before children, plain gzip with a zero mtime."""
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w", format=tarfile.USTAR_FORMAT) as tar:
        for name, data, mode in entries:
            ti = tarfile.TarInfo(name)
            ti.mode = mode
            ti.mtime = 1_700_000_000
            ti.uname = "root"
            if data is None:
                ti.type = tarfile.DIRTYPE
                tar.addfile(ti)
            else:
                ti.size = len(data)
                tar.addfile(ti, io.BytesIO(data))
    with open(path, "wb") as f, gzip.GzipFile(filename="", mode="wb", fileobj=f, mtime=0) as gz:
        gz.write(raw.getvalue())


GO_TREE = {
    ".": ("dir", None),
    "exec.sh": ("file", "#!/bin/sh\n"),
    "file.txt": ("file", "hello"),
    "sub": ("dir", None),
    "sub/emptydir": ("dir", None),
    "sub/nested.txt": ("file", "world"),
    "sub2": ("dir", None),
}


def _check_go_tree(root: Path) -> None:
    got = _snapshot(root)
    assert got == {k: v for k, v in GO_TREE.items() if k != "."}
    if POSIX:
        assert (root / "exec.sh").stat().st_mode & 0o111, "exec bit comes back"
        assert (root / "file.txt").stat().st_mode & 0o777 == 0o600
        assert (root / "sub2").stat().st_mode & 0o777 == 0o700
        assert root.stat().st_mode & 0o777 == 0o750


def test_fsdir_restores_an_archive_written_by_the_go_driver(tmp_path: Path) -> None:
    """``upgrade_fsdir_go.tar.gz`` is the output of the Go driver's Backup."""
    bk = tmp_path / "bk"
    bk.mkdir()
    (bk / "mydata.tar.gz").write_bytes(GO_FIXTURE.read_bytes())
    root = tmp_path / "mydata"
    root.mkdir()
    (root / "stale.txt").write_text("stale")
    FsDirDriver("mydata", root).restore(bk)
    _check_go_tree(root)


def test_fsdir_restores_a_hand_laid_out_go_archive(tmp_path: Path) -> None:
    bk = tmp_path / "bk"
    bk.mkdir()
    _go_layout(
        bk / "mydata.tar.gz",
        [
            (".", None, 0o750),
            ("exec.sh", b"#!/bin/sh\n", 0o755),
            ("file.txt", b"hello", 0o600),
            ("sub", None, 0o750),
            ("sub/emptydir", None, 0o750),
            ("sub/nested.txt", b"world", 0o644),
            ("sub2", None, 0o700),
        ],
    )
    root = tmp_path / "mydata"
    FsDirDriver("mydata", root).restore(bk)
    _check_go_tree(root)


def test_fsdir_backup_has_the_members_go_writes(tmp_path: Path) -> None:
    """Same names, types and modes as the Go fixture (what Go's restore reads)."""
    root = tmp_path / "mydata"
    root.mkdir()
    _tree(root)
    (root / "sub" / "nested.txt").chmod(0o644)
    root.chmod(0o750)
    bk = tmp_path / "bk"
    FsDirDriver("mydata", root).backup(bk)
    mine = [(n, t, m) for n, t, m in _entries(bk / "mydata.tar.gz")]
    go = [(n, t, m) for n, t, m in _entries(GO_FIXTURE)]
    if POSIX:
        assert mine == go
    else:
        assert [(n, t) for n, t, _ in mine] == [(n, t) for n, t, _ in go]


# --- the traversal guard


@pytest.fixture(params=["stdlib-filter", "no-filter"])
def extraction_path(request: pytest.FixtureRequest, monkeypatch: pytest.MonkeyPatch) -> str:
    """Run a test with and without the stdlib ``data`` filter: the second is
    what Python 3.11.0 to 3.11.3 does, where the module's own check is the
    only guard."""
    if request.param == "no-filter":
        monkeypatch.setattr(fsdir, "_HAS_DATA_FILTER", False)
    elif not hasattr(tarfile, "data_filter"):
        pytest.skip("this Python has no tarfile extraction filters")
    return str(request.param)


def _evil(
    path: Path,
    entries: list[tuple[str, bytes, bytes | None]],
) -> None:
    """Write a gzip tar of ``(name, type, data-or-linkname)`` members, after a
    valid first member so a half-done extraction would show."""
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w", format=tarfile.GNU_FORMAT) as tar:
        ok = tarfile.TarInfo("good.txt")
        ok.size = 3
        tar.addfile(ok, io.BytesIO(b"new"))
        for name, typ, extra in entries:
            ti = tarfile.TarInfo(name)
            ti.type = typ
            ti.mode = 0o755 if typ == tarfile.DIRTYPE else 0o644
            if typ in (tarfile.SYMTYPE, tarfile.LNKTYPE):
                ti.linkname = (extra or b"").decode()
            elif typ == tarfile.REGTYPE:
                ti.size = len(extra or b"")
                tar.addfile(ti, io.BytesIO(extra or b""))
                continue
            tar.addfile(ti)
    with gzip.open(path, "wb") as gz:
        gz.write(raw.getvalue())


R, S, H = tarfile.REGTYPE, tarfile.SYMTYPE, tarfile.LNKTYPE

BAD_MEMBERS = {
    "dotdot": [("../evil.txt", R, b"x")],
    "nested-dotdot": [("sub/../../evil.txt", R, b"x")],
    "absolute": [("{outside}/abs.txt", R, b"x")],
    "dotdot-dir": [("../evil-dir", tarfile.DIRTYPE, None)],
    "symlink-up": [("l", S, b"../outside")],
    "symlink-nested-up": [("a/l", S, b"../../outside")],
    "symlink-absolute": [("l", S, b"{outside}")],
    "symlink-empty-target": [("l", S, b"")],
    "hardlink-up": [("h", H, b"../outside")],
    "hardlink-absolute": [("h", H, b"{outside}/target")],
    "write-below-symlink-loop": [("here", S, b"."), ("here/up", S, b"..")],
    "write-below-symlink": [("here", S, b"."), ("here/evil.txt", R, b"x")],
    "fifo": [("p", tarfile.FIFOTYPE, None)],
    "chardev": [("c", tarfile.CHRTYPE, None)],
    "blockdev": [("b", tarfile.BLKTYPE, None)],
}


@pytest.mark.skipif(not POSIX, reason="posix archive members")
@pytest.mark.parametrize("case", sorted(BAD_MEMBERS))
def test_fsdir_restore_rejects_unsafe_members_and_changes_nothing(
    tmp_path: Path, case: str, extraction_path: str
) -> None:
    home = tmp_path / "home"
    home.mkdir()
    root = home / "mydata"
    root.mkdir()
    (root / "keep.txt").write_text("keep")
    bk = tmp_path / "bk"
    bk.mkdir()
    outside = tmp_path / "outside"  # an absolute name pointing here must not land
    members = [
        (
            n.replace("{outside}", str(outside)),
            typ,
            None if x is None else x.replace(b"{outside}", str(outside).encode()),
        )
        for n, typ, x in BAD_MEMBERS[case]
    ]
    _evil(bk / "mydata.tar.gz", members)
    before = sorted(p.name for p in tmp_path.rglob("*"))

    with pytest.raises(UnsafeArchiveError):
        FsDirDriver("mydata", root).restore(bk)

    assert _snapshot(root) == {"keep.txt": ("file", "keep")}, "current directory untouched"
    assert sorted(p.name for p in home.iterdir()) == ["mydata"], "no temp directory left"
    assert sorted(p.name for p in tmp_path.rglob("*")) == before
    assert not outside.exists()


@pytest.mark.skipif(not POSIX, reason="posix archive members")
def test_fsdir_accepts_links_that_stay_inside(tmp_path: Path, extraction_path: str) -> None:
    if extraction_path == "no-filter":
        pytest.skip("without filters every link is refused, see the refusal test below")
    bk = tmp_path / "bk"
    bk.mkdir()
    _evil(
        bk / "mydata.tar.gz",
        [("d", tarfile.DIRTYPE, None), ("d/s", S, b"../good.txt"), ("hl", H, b"good.txt")],
    )
    root = tmp_path / "mydata"
    FsDirDriver("mydata", root).restore(bk)
    assert os.readlink(root / "d" / "s") == "../good.txt"
    assert (root / "hl").read_text() == "new"


@pytest.mark.skipif(not POSIX, reason="posix modes")
def test_fsdir_restore_drops_setuid_bits(tmp_path: Path, extraction_path: str) -> None:
    bk = tmp_path / "bk"
    bk.mkdir()
    _go_layout(bk / "mydata.tar.gz", [(".", None, 0o750), ("tool", b"x", 0o4755)])
    root = tmp_path / "mydata"
    FsDirDriver("mydata", root).restore(bk)
    assert (root / "tool").stat().st_mode & 0o7777 == 0o755


def test_fsdir_failed_extraction_leaves_the_directory_as_it_was(tmp_path: Path) -> None:
    root = tmp_path / "mydata"
    root.mkdir()
    (root / "keep.txt").write_text("keep")
    bk = tmp_path / "bk"
    bk.mkdir()
    full = tmp_path / "full.tar.gz"
    _go_layout(full, [("a.txt", b"a" * 5000, 0o644), ("b.txt", b"b" * 5000, 0o644)])
    data = full.read_bytes()
    (bk / "mydata.tar.gz").write_bytes(data[: len(data) // 2])  # truncated gzip
    with pytest.raises((OSError, EOFError, tarfile.TarError)):
        FsDirDriver("mydata", root).restore(bk)
    assert _snapshot(root) == {"keep.txt": ("file", "keep")}
    assert sorted(p.name for p in tmp_path.iterdir()) == ["bk", "full.tar.gz", "mydata"]


def test_fsdir_garbage_archive_is_an_error_not_a_swap(tmp_path: Path) -> None:
    root = tmp_path / "mydata"
    root.mkdir()
    (root / "keep.txt").write_text("keep")
    bk = tmp_path / "bk"
    bk.mkdir()
    (bk / "mydata.tar.gz").write_bytes(b"not a gzip")
    with pytest.raises((OSError, tarfile.TarError)):
        FsDirDriver("mydata", root).restore(bk)
    assert _snapshot(root) == {"keep.txt": ("file", "keep")}


# ------------------------------------------------------- through the Migrator


def _failing_run(
    tmp_path: Path,
    driver: SchemaDriver,
    schema: str,
    mutate: Callable[[], None],
    state: Callable[[], object],
) -> None:
    """Migrate to 1.0.0, then run 2.0.0 (mutates the store) and 2.1.0 (fails),
    neither with a ``down``: the Migrator must restore the backup and record
    1.0.0 again."""
    data = tmp_path / "data"

    def reg(*migs: Migration) -> Registry:
        r = Registry()
        for m in migs:
            r.register(m)
        return r

    first = Migrator(
        "demo",
        "1.0.0",
        data_dir=data,
        registry=reg(Migration("1.0.0", schema, lambda: None)),
    )
    first.add_driver(driver)
    first.run()
    assert read_version_file_in(data, schema) == "1.0.0"
    before = state()

    def boom() -> None:
        raise RuntimeError("boom")

    second = Migrator(
        "demo",
        "2.1.0",
        data_dir=data,
        registry=reg(Migration("2.0.0", schema, mutate), Migration("2.1.0", schema, boom)),
    )
    second.add_driver(driver)
    with pytest.raises(MigrateError, match="boom"):
        second.run()

    assert state() == before, "prior state restored"
    assert read_version_file_in(data, schema) == "1.0.0", "stored version back at the start"
    assert driver.version() == "1.0.0"
    backups = sorted(p.name for p in (data / "backups" / schema).iterdir() if p.is_dir())
    assert backups == ["pre-1.0.0", "pre-2.0.0"]


def test_migrator_restores_configfile_after_a_failed_migration(tmp_path: Path) -> None:
    a, b = tmp_path / "etc" / "a.yaml", tmp_path / "b.json"
    a.parent.mkdir()
    a.write_text("a: 1\n")
    b.write_text("{}")
    d = ConfigFileDriver([a, b], name="config", data_dir=tmp_path / "data")

    def mutate() -> None:
        a.write_text("a: 2\n")
        b.unlink()

    _failing_run(
        tmp_path, d, "config", mutate, lambda: (a.read_text(), b.exists() and b.read_text())
    )


def test_migrator_restores_fsdir_after_a_failed_migration(tmp_path: Path) -> None:
    root = tmp_path / "store"
    root.mkdir()
    _tree(root)
    d = FsDirDriver("store", root, data_dir=tmp_path / "data")

    def mutate() -> None:
        (root / "file.txt").write_text("corrupted")
        (root / "sub" / "nested.txt").unlink()
        (root / "added").mkdir()

    _failing_run(tmp_path, d, "store", mutate, lambda: _snapshot(root))


@pytest.mark.skipif(not POSIX or not hasattr(tarfile, "data_filter"), reason="symlinks, filters")
def test_migrator_fails_the_schema_before_any_up_when_backup_refuses_a_link(
    tmp_path: Path,
) -> None:
    root = tmp_path / "store"
    root.mkdir()
    (root / "abs").symlink_to(str(tmp_path / "outside"))
    ran: list[str] = []
    r = Registry()
    r.register(Migration("1.0.0", "store", lambda: ran.append("up")))
    m = Migrator("demo", "1.0.0", data_dir=tmp_path / "data", registry=r)
    m.add_driver(FsDirDriver("store", root, data_dir=tmp_path / "data"))
    with pytest.raises(MigrateError, match=r"^migrate store: backup: .*abs") as ei:
        m.run()
    assert ran == [], "no migration ran"
    assert "backup:" in str(ei.value)


# --- links through a symlink to the root

# ``d`` points at the directory being extracted, so ``d/..`` is its parent:
# lexically harmless, really an escape.
ROOT_LINK_ATTACKS = {
    "write-through-symlink": [
        ("d", S, b"."),
        ("f", S, b"d/../outside.txt"),
        ("f", R, b"PWNED"),
    ],
    "write-through-dangling-first": [
        ("f", S, b"d/../outside.txt"),
        ("d", S, b"."),
        ("f", R, b"PWNED"),
    ],
    "hardlink-through-symlink": [
        ("d", S, b"."),
        ("s", S, b"d/.."),
        ("leak", H, b"s/outside.txt"),
    ],
    "escaping-symlink-left-in-tree": [("d", S, b"."), ("s", S, b"d/..")],
    "escaping-symlink-dangling-first": [("s", S, b"d/.."), ("d", S, b".")],
}


@pytest.mark.skipif(not POSIX, reason="posix archive members")
@pytest.mark.parametrize("case", sorted(ROOT_LINK_ATTACKS))
def test_fsdir_restore_refuses_links_through_a_symlink_to_the_root(
    tmp_path: Path, case: str, extraction_path: str
) -> None:
    home = tmp_path / "home"
    home.mkdir()
    root = home / "mydata"
    root.mkdir()
    (root / "keep.txt").write_text("keep")
    outside = home / "outside.txt"  # beside the restore target
    outside.write_text("ORIG")
    links = outside.stat().st_nlink
    bk = tmp_path / "bk"
    bk.mkdir()
    _evil(bk / "mydata.tar.gz", ROOT_LINK_ATTACKS[case])

    with pytest.raises(UnsafeArchiveError):
        FsDirDriver("mydata", root).restore(bk)

    assert _snapshot(root) == {"keep.txt": ("file", "keep")}, "current directory untouched"
    assert outside.read_text() == "ORIG"
    assert outside.stat().st_nlink == links
    assert sorted(p.name for p in home.iterdir()) == ["mydata", "outside.txt"], "no temp directory"


@pytest.mark.skipif(not POSIX, reason="symlinks")
def test_fsdir_without_filters_refuses_even_a_harmless_link(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(fsdir, "_HAS_DATA_FILTER", False)
    root = tmp_path / "mydata"
    root.mkdir()
    (root / "keep.txt").write_text("keep")
    bk = tmp_path / "bk"
    bk.mkdir()
    _evil(bk / "mydata.tar.gz", [("l", S, b"good.txt")])
    with pytest.raises(UnsafeArchiveError, match=r"3\.11\.4"):
        FsDirDriver("mydata", root).restore(bk)
    assert _snapshot(root) == {"keep.txt": ("file", "keep")}
    assert sorted(p.name for p in tmp_path.iterdir()) == ["bk", "mydata"]


@pytest.mark.skipif(not POSIX, reason="symlinks")
def test_fsdir_without_filters_backup_refuses_symlinks(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A backup its own restore would refuse is not written."""
    monkeypatch.setattr(fsdir, "_HAS_DATA_FILTER", False)
    root = tmp_path / "mydata"
    root.mkdir()
    (root / "file.txt").write_text("x")
    (root / "l").symlink_to("file.txt")
    bk = tmp_path / "bk"
    with pytest.raises(OSError, match=r"3\.11\.4"):
        FsDirDriver("mydata", root).backup(bk)
    assert list(bk.iterdir()) == []


def test_fsdir_without_filters_round_trips_plain_trees(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(fsdir, "_HAS_DATA_FILTER", False)
    root = tmp_path / "mydata"
    root.mkdir()
    _tree(root)
    want = _snapshot(root)
    d = FsDirDriver("mydata", root)
    d.backup(tmp_path / "bk")
    (root / "file.txt").write_text("changed")
    d.restore(tmp_path / "bk")
    assert _snapshot(root) == want
