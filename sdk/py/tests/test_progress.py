"""Tests for hop_top_kit.progress — observable long-running ops."""

from __future__ import annotations

import io
import json

import pytest

from hop_top_kit.progress import JobHandle, ProgressEvent, ProgressReporter


class TestProgressEvent:
    """ProgressEvent dataclass fields."""

    def test_fields(self) -> None:
        ev = ProgressEvent(
            phase="download",
            step="fetch",
            current=5,
            total=10,
            percent=50.0,
            message="halfway",
        )
        assert ev.phase == "download"
        assert ev.step == "fetch"
        assert ev.current == 5
        assert ev.total == 10
        assert ev.percent == 50.0
        assert ev.message == "halfway"

    def test_message_default(self) -> None:
        ev = ProgressEvent(
            phase="p",
            step="s",
            current=0,
            total=1,
            percent=0.0,
        )
        assert ev.message == ""


class TestProgressReporterJSON:
    """Non-TTY mode emits JSON lines."""

    def test_emit_json(self) -> None:
        buf = io.StringIO()
        r = ProgressReporter(w=buf, is_tty=False)
        ev = ProgressEvent(
            phase="build",
            step="compile",
            current=1,
            total=3,
            percent=33.3,
            message="compiling",
        )
        r.emit(ev)
        line = buf.getvalue().strip()
        parsed = json.loads(line)
        assert parsed["phase"] == "build"
        assert parsed["step"] == "compile"
        assert parsed["current"] == 1
        assert parsed["total"] == 3
        assert parsed["percent"] == 33.3
        assert parsed["message"] == "compiling"

    def test_done_json(self) -> None:
        buf = io.StringIO()
        r = ProgressReporter(w=buf, is_tty=False)
        r.done("finished")
        line = buf.getvalue().strip()
        parsed = json.loads(line)
        assert parsed["done"] is True
        assert parsed["message"] == "finished"


class TestProgressReporterHuman:
    """TTY mode emits human-readable text."""

    def test_emit_human(self) -> None:
        buf = io.StringIO()
        r = ProgressReporter(w=buf, is_tty=True)
        ev = ProgressEvent(
            phase="deploy",
            step="push",
            current=2,
            total=4,
            percent=50.0,
            message="pushing",
        )
        r.emit(ev)
        output = buf.getvalue()
        assert "deploy" in output
        assert "50.0%" in output

    def test_done_human(self) -> None:
        buf = io.StringIO()
        r = ProgressReporter(w=buf, is_tty=True)
        r.done("all done")
        output = buf.getvalue()
        assert "all done" in output


class TestJobHandle:
    """JobHandle tracks async operation status."""

    def test_fields(self) -> None:
        j = JobHandle(id="abc-123", status="running")
        assert j.id == "abc-123"
        assert j.status == "running"

    @pytest.mark.parametrize(
        "status",
        ["running", "completed", "failed", "cancelled"],
    )
    def test_valid_statuses(self, status: str) -> None:
        j = JobHandle(id="x", status=status)
        assert j.status == status
