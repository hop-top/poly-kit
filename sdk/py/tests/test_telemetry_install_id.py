"""Tests for hop_top_kit.telemetry.install_id."""

from __future__ import annotations

import stat
import threading
from pathlib import Path

import pytest

from hop_top_kit.telemetry import install_id as iid


@pytest.fixture(autouse=True)
def isolated_state_home(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    """Point XDG_STATE_HOME at a per-test tmp dir."""
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path))
    yield tmp_path


def _file_mode(p: Path) -> int:
    return stat.S_IMODE(p.stat().st_mode)


class TestGetInstallID:
    def test_first_call_generates_hex(self):
        h = iid.get_install_id()
        assert isinstance(h, str)
        assert len(h) == 64
        int(h, 16)  # parses as hex

    def test_persisted_file_is_32_bytes(self):
        iid.get_install_id()
        data = iid.install_id_path().read_bytes()
        assert len(data) == 32

    def test_file_perms_0600(self):
        iid.get_install_id()
        assert _file_mode(iid.install_id_path()) == 0o600

    def test_parent_dir_perms_0700(self):
        iid.get_install_id()
        parent = iid.install_id_path().parent
        # Some platforms (notably macOS) may surface broader bits if the
        # umask/parent layout doesn't match; assert the owner-only invariant
        # we care about: the file is unreadable to group/other.
        assert _file_mode(parent) & 0o077 == 0

    def test_stable_across_calls(self):
        seen = {iid.get_install_id() for _ in range(10)}
        assert len(seen) == 1

    def test_rotate_changes_hex(self):
        first = iid.get_install_id()
        second = iid.rotate()
        assert first != second
        # Subsequent get returns the rotated value.
        assert iid.get_install_id() == second

    def test_malformed_file_raises(self, isolated_state_home: Path):
        p = iid.install_id_path()
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(b"\x00" * 16)  # wrong size
        with pytest.raises(ValueError, match="wrong size"):
            iid.get_install_id()

    def test_reset_for_test_clears(self):
        first = iid.get_install_id()
        iid.reset_for_test()
        assert not iid.install_id_path().exists()
        second = iid.get_install_id()
        # New bytes → almost certainly different hex.
        assert first != second

    def test_reset_idempotent(self):
        iid.reset_for_test()  # no file yet
        iid.reset_for_test()  # still no file

    def test_concurrent_first_call_converges(self):
        """4 threads racing to generate; all must observe the same hex."""
        results: list[str] = []
        errors: list[BaseException] = []
        barrier = threading.Barrier(4)

        def worker():
            try:
                barrier.wait()
                results.append(iid.get_install_id())
            except BaseException as e:
                errors.append(e)

        threads = [threading.Thread(target=worker) for _ in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert errors == []
        assert len(results) == 4
        assert len(set(results)) == 1
