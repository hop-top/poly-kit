"""Tests for hop_top_kit.stream — exit codes + stream writer."""

from __future__ import annotations

import io
import sys

import pytest

from hop_top_kit.stream import ExitCode, StreamWriter, create_stream_writer


class TestExitCode:
    """ExitCode enum values match the 12-factor spec."""

    @pytest.mark.parametrize(
        ("member", "value"),
        [
            ("OK", 0),
            ("ERROR", 1),
            ("USAGE", 2),
            ("NOT_FOUND", 3),
            ("CONFLICT", 4),
            ("AUTH", 5),
            ("PERMISSION", 6),
            ("TIMEOUT", 7),
            ("CANCELLED", 8),
        ],
    )
    def test_values(self, member: str, value: int) -> None:
        assert ExitCode[member] == value

    def test_is_int(self) -> None:
        assert isinstance(ExitCode.OK, int)
        assert ExitCode.ERROR + 0 == 1

    def test_all_members_count(self) -> None:
        assert len(ExitCode) == 9


class TestStreamWriter:
    """StreamWriter separates data (stdout) from human (stderr)."""

    def test_data_writes_to_data_stream(self) -> None:
        data = io.StringIO()
        human = io.StringIO()
        sw = StreamWriter(data=data, human=human, is_tty=False)
        sw.data.write("structured output\n")
        assert data.getvalue() == "structured output\n"
        assert human.getvalue() == ""

    def test_human_writes_to_human_stream(self) -> None:
        data = io.StringIO()
        human = io.StringIO()
        sw = StreamWriter(data=data, human=human, is_tty=True)
        sw.human.write("progress info\n")
        assert human.getvalue() == "progress info\n"
        assert data.getvalue() == ""

    def test_is_tty_stored(self) -> None:
        sw = StreamWriter(
            data=io.StringIO(),
            human=io.StringIO(),
            is_tty=True,
        )
        assert sw.is_tty is True

    def test_is_tty_false(self) -> None:
        sw = StreamWriter(
            data=io.StringIO(),
            human=io.StringIO(),
            is_tty=False,
        )
        assert sw.is_tty is False


class TestCreateStreamWriter:
    """Factory wires stdout/stderr and detects TTY."""

    def test_returns_stream_writer(self) -> None:
        sw = create_stream_writer()
        assert isinstance(sw, StreamWriter)

    def test_data_is_stdout(self) -> None:
        sw = create_stream_writer()
        assert sw.data is sys.stdout

    def test_human_is_stderr(self) -> None:
        sw = create_stream_writer()
        assert sw.human is sys.stderr
