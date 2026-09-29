"""Tests for the kit binary resolver: checksum verification and extraction."""

from __future__ import annotations

import hashlib
import io
import os
import stat
import tarfile
import urllib.error
import zipfile
from pathlib import Path

import pytest

from kit_engine import _binary

ARCHIVE = "kit_linux_amd64.tar.gz"
BINARY = b"#!/bin/sh\necho kit\n"


def _tar_gz(members: dict[str, bytes | tarfile.TarInfo]) -> bytes:
    """Build a tar.gz; a ``TarInfo`` value adds a link/special member as-is."""
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tf:
        for name, data in members.items():
            if isinstance(data, tarfile.TarInfo):
                tf.addfile(data)
                continue
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = 0o755
            tf.addfile(info, io.BytesIO(data))
    return buf.getvalue()


def _link(name: str, target: str, kind: bytes = tarfile.SYMTYPE) -> tarfile.TarInfo:
    info = tarfile.TarInfo(name)
    info.type = kind
    info.linkname = target
    return info


def _zip(members: dict[str, bytes], symlinks: dict[str, str] | None = None) -> bytes:
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as zf:
        for name, data in members.items():
            zf.writestr(name, data)
        for name, target in (symlinks or {}).items():
            info = zipfile.ZipInfo(name)
            info.create_system = 3  # unix
            info.external_attr = (stat.S_IFLNK | 0o777) << 16
            zf.writestr(info, target)
    return buf.getvalue()


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class _Env:
    """Controls the network and filesystem seen by find_kit_binary."""

    def __init__(self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
        self.bin_dir = tmp_path / "bin"
        self.archive = _tar_gz({"kit": BINARY})
        self.checksums: bytes | Exception = f"{_sha256(self.archive)}  {ARCHIVE}\n".encode()
        self.extracted = False
        self.urls: list[str] = []

        monkeypatch.setattr(_binary.shutil, "which", lambda _name: None)
        monkeypatch.setattr(_binary, "_bin_dir", lambda: self.bin_dir)
        monkeypatch.setattr(_binary, "_platform_key", lambda: ("linux", "amd64"))
        monkeypatch.setattr(_binary.platform, "system", lambda: "Linux")
        monkeypatch.setattr(_binary.urllib.request, "urlopen", self._urlopen)
        monkeypatch.setattr(_binary, "_download", self._download)

        real_extract = _binary._extract

        def extract(*args, **kwargs):
            self.extracted = True
            return real_extract(*args, **kwargs)

        monkeypatch.setattr(_binary, "_extract", extract)

    def _urlopen(self, url, timeout=None):
        assert url.endswith("/checksums.txt"), url
        self.urls.append(url)
        if isinstance(self.checksums, Exception):
            raise self.checksums
        return io.BytesIO(self.checksums)

    def _download(self, url, dest):
        assert url.endswith("/" + ARCHIVE), url
        self.urls.append(url)
        Path(dest).write_bytes(self.archive)

    @property
    def installed(self) -> Path:
        return self.bin_dir / "kit"


@pytest.fixture
def env(tmp_path, monkeypatch):
    return _Env(tmp_path, monkeypatch)


def test_installs_verified_archive(env):
    path = _binary.find_kit_binary("1.2.3")
    assert Path(path) == env.installed
    assert env.installed.read_bytes() == BINARY


def test_downloads_from_the_kit_component_release(env):
    # kit releases are tagged `kit/v<version>` on hop-top/poly-kit, and that
    # release carries the archives + checksums.txt (.goreleaser.yaml).
    _binary.find_kit_binary("v0.5.0-alpha.16")
    base = "https://github.com/hop-top/poly-kit/releases/download/kit/v0.5.0-alpha.16"
    assert sorted(env.urls) == [f"{base}/checksums.txt", f"{base}/{ARCHIVE}"]


def test_checksum_line_with_path_prefix_matches_basename(env):
    env.checksums = (
        "# goreleaser checksums\n"
        "\n"
        f"{_sha256(b'other')}  kit_darwin_arm64.tar.gz\n"
        f"{_sha256(env.archive)}  ./dist/{ARCHIVE}\n"
    ).encode()
    _binary.find_kit_binary("1.2.3")
    assert env.installed.read_bytes() == BINARY


@pytest.mark.parametrize(
    "error",
    [
        urllib.error.HTTPError("u", 404, "Not Found", {}, None),  # type: ignore[arg-type]
        urllib.error.URLError("blocked"),
        TimeoutError("timed out"),
    ],
    ids=["http-404", "url-error", "timeout"],
)
def test_checksums_fetch_failure_fails_closed(env, error):
    env.checksums = error
    with pytest.raises(RuntimeError, match="checksums"):
        _binary.find_kit_binary("1.2.3")
    assert not env.extracted
    assert not env.installed.exists()


def test_checksums_undecodable_fails_closed(env):
    env.checksums = b"\xff\xfe\x00garbage"
    with pytest.raises(RuntimeError, match="checksums"):
        _binary.find_kit_binary("1.2.3")
    assert not env.installed.exists()


def test_checksums_missing_entry_fails_closed(env):
    env.checksums = f"{_sha256(env.archive)}  kit_darwin_arm64.tar.gz\n".encode()
    with pytest.raises(RuntimeError, match=f"no checksum.*{ARCHIVE}"):
        _binary.find_kit_binary("1.2.3")
    assert not env.extracted
    assert not env.installed.exists()


def test_checksums_empty_fails_closed(env):
    env.checksums = b""
    with pytest.raises(RuntimeError, match="no checksum"):
        _binary.find_kit_binary("1.2.3")
    assert not env.installed.exists()


def test_checksum_mismatch_fails_closed(env):
    env.checksums = f"{_sha256(b'tampered')}  {ARCHIVE}\n".encode()
    with pytest.raises(RuntimeError, match="Checksum mismatch"):
        _binary.find_kit_binary("1.2.3")
    assert not env.extracted
    assert not env.installed.exists()


# --- extraction ---------------------------------------------------------------


@pytest.fixture
def box(tmp_path):
    """A dest dir nested inside a sandbox so escapes stay observable."""
    dest = tmp_path / "box" / "dest"
    dest.mkdir(parents=True)
    return dest


def _write(path: Path, data: bytes) -> Path:
    path.write_bytes(data)
    return path


def _entries(root: Path) -> set[str]:
    return {str(p.relative_to(root)) for p in root.rglob("*")}


def test_extract_tar_nested_binary(box):
    archive = _write(box.parent / "a.tar.gz", _tar_gz({"kit_1.2.3_linux_amd64/kit": BINARY}))
    out = _binary._extract(archive, box, "kit")
    assert out == box / "kit"
    assert out.read_bytes() == BINARY


def test_extract_tar_writes_only_binary(box):
    archive = _write(
        box.parent / "a.tar.gz",
        _tar_gz({"README.md": b"readme", "docs/x.txt": b"x", "kit": BINARY}),
    )
    _binary._extract(archive, box, "kit")
    assert _entries(box) == {"kit"}


def test_extract_tar_ignores_traversal_members(box):
    sandbox = box.parent
    archive = _write(
        sandbox / "a.tar.gz",
        _tar_gz(
            {
                "../escape": b"pwn",
                "../../escape2": b"pwn",
                str(sandbox / "abs-escape"): b"pwn",
                "kit": BINARY,
            }
        ),
    )
    _binary._extract(archive, box, "kit")
    assert not (sandbox / "escape").exists()
    assert not (sandbox.parent / "escape2").exists()
    assert not (sandbox / "abs-escape").exists()
    assert (box / "kit").read_bytes() == BINARY


def test_extract_tar_symlink_dir_cannot_escape(box, tmp_path):
    outside = tmp_path / "outside"
    outside.mkdir()
    archive = _write(
        box.parent / "a.tar.gz",
        _tar_gz({"d": _link("d", str(outside)), "d/pwn": b"pwn", "kit": BINARY}),
    )
    _binary._extract(archive, box, "kit")
    assert not (outside / "pwn").exists()
    assert (box / "kit").read_bytes() == BINARY


@pytest.mark.parametrize("kind", [tarfile.SYMTYPE, tarfile.LNKTYPE], ids=["symlink", "hardlink"])
def test_extract_tar_rejects_link_as_binary(box, tmp_path, kind):
    target = _write(tmp_path / "target", b"not kit")
    archive = _write(box.parent / "a.tar.gz", _tar_gz({"kit": _link("kit", str(target), kind)}))
    with pytest.raises(RuntimeError, match="not found"):
        _binary._extract(archive, box, "kit")
    assert not os.path.lexists(box / "kit")


def test_extract_zip_nested_binary(box):
    archive = _write(box.parent / "a.zip", _zip({"kit_1.2.3_windows_amd64/kit.exe": BINARY}))
    out = _binary._extract(archive, box, "kit.exe")
    assert out.read_bytes() == BINARY
    assert _entries(box) == {"kit.exe"}


def test_extract_zip_ignores_traversal_members(box):
    sandbox = box.parent
    archive = _write(
        sandbox / "a.zip",
        _zip({"../escape": b"pwn", "sub/../../escape2": b"pwn", "kit.exe": BINARY}),
    )
    _binary._extract(archive, box, "kit.exe")
    assert not (sandbox / "escape").exists()
    assert not (sandbox / "escape2").exists()
    assert _entries(box) == {"kit.exe"}


def test_extract_zip_rejects_symlink_binary(box):
    archive = _write(box.parent / "a.zip", _zip({}, symlinks={"kit.exe": "/etc/passwd"}))
    with pytest.raises(RuntimeError, match="not found"):
        _binary._extract(archive, box, "kit.exe")
    assert not os.path.lexists(box / "kit.exe")


def test_install_rejects_symlinked_binary(env, tmp_path):
    target = _write(tmp_path / "target", b"not kit")
    env.archive = _tar_gz({"kit": _link("kit", str(target))})
    env.checksums = f"{_sha256(env.archive)}  {ARCHIVE}\n".encode()
    with pytest.raises(RuntimeError, match="not found"):
        _binary.find_kit_binary("1.2.3")
    assert not os.path.lexists(env.installed)


# --- version probes -------------------------------------------------------------


@pytest.fixture
def probe(env, monkeypatch):
    """Make ``kit`` resolvable on PATH and control what ``--version`` does."""
    monkeypatch.setattr(_binary.shutil, "which", lambda _name: "/opt/kit")
    outcome: dict[str, object] = {"result": "v1.2.9\n"}

    def check_output(cmd, **_kwargs):
        result = outcome["result"]
        if isinstance(result, BaseException):
            raise result
        return result

    monkeypatch.setattr(_binary.subprocess, "check_output", check_output)
    return outcome


def test_probe_returns_compatible_path_binary(probe):
    assert _binary.find_kit_binary("1.2.3") == "/opt/kit"


@pytest.mark.parametrize(
    "output",
    ["kit v1.2.9\n", "kit v1.2.0-alpha.3\n", "kit version 1.2.9\n", "1.2.9\n"],
)
def test_probe_reads_the_version_kit_prints(probe, output):
    # `kit --version` prints `kit v<version>`.
    probe["result"] = output
    assert _binary.find_kit_binary("1.2.3") == "/opt/kit"


def test_probe_empty_output_falls_through(probe, env):
    probe["result"] = "\n"
    assert Path(_binary.find_kit_binary("1.2.3")) == env.installed


@pytest.mark.parametrize(
    "error",
    [
        FileNotFoundError("kit"),
        PermissionError("kit"),
        _binary.subprocess.CalledProcessError(1, ["kit"]),
        _binary.subprocess.TimeoutExpired(["kit"], 5),
        UnicodeDecodeError("utf-8", b"\xff", 0, 1, "invalid start byte"),
    ],
    ids=["missing", "denied", "exit-1", "timeout", "undecodable"],
)
def test_probe_failure_falls_through_to_download(probe, env, error):
    probe["result"] = error
    assert Path(_binary.find_kit_binary("1.2.3")) == env.installed


def test_probe_incompatible_version_falls_through(probe, env):
    probe["result"] = "v9.9.9\n"
    assert Path(_binary.find_kit_binary("1.2.3")) == env.installed


def test_probe_programming_errors_propagate(probe):
    probe["result"] = TypeError("bug")
    with pytest.raises(TypeError, match="bug"):
        _binary.find_kit_binary("1.2.3")


def test_local_bin_probe_failure_falls_through(env, monkeypatch):
    env.bin_dir.mkdir(parents=True)
    env.installed.write_bytes(b"stale")

    def check_output(cmd, **_kwargs):
        raise _binary.subprocess.CalledProcessError(1, cmd)

    monkeypatch.setattr(_binary.subprocess, "check_output", check_output)
    _binary.find_kit_binary("1.2.3")
    assert env.installed.read_bytes() == BINARY


def test_local_bin_programming_errors_propagate(env, monkeypatch):
    env.bin_dir.mkdir(parents=True)
    env.installed.write_bytes(b"stale")

    def check_output(cmd, **_kwargs):
        raise TypeError("bug")

    monkeypatch.setattr(_binary.subprocess, "check_output", check_output)
    with pytest.raises(TypeError, match="bug"):
        _binary.find_kit_binary("1.2.3")
