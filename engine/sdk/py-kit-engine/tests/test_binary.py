"""Tests for the kit binary resolver: checksum verification and extraction."""

from __future__ import annotations

import hashlib
import io
import tarfile
import urllib.error
from pathlib import Path

import pytest

from kit_engine import _binary

ARCHIVE = "kit_linux_amd64.tar.gz"
BINARY = b"#!/bin/sh\necho kit\n"


def _tar_gz(members: dict[str, bytes]) -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tf:
        for name, data in members.items():
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = 0o755
            tf.addfile(info, io.BytesIO(data))
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
        if isinstance(self.checksums, Exception):
            raise self.checksums
        return io.BytesIO(self.checksums)

    def _download(self, url, dest):
        assert url.endswith("/" + ARCHIVE), url
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
