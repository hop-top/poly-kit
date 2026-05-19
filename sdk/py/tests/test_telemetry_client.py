"""Tests for hop_top_kit.telemetry.client."""

from __future__ import annotations

import json
import time
from pathlib import Path
from typing import Any

import pytest

from hop_top_kit.telemetry.client import Client


def _grant_consent(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """Write a granted consent file under an isolated XDG_CONFIG_HOME."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "cfg"))
    p = tmp_path / "cfg" / "kit" / "telemetry.yaml"
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(
        "telemetry:\n"
        "  consent:\n"
        "    state: granted\n"
        "    prompt_version: 1\n"
        "    decision_source: prompt\n"
    )


@pytest.fixture
def isolated(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    """Isolate XDG state + config + telemetry env per test."""
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "state"))
    _grant_consent(tmp_path, monkeypatch)
    monkeypatch.setenv("KIT_TELEMETRY_MODE", "anon")
    monkeypatch.delenv("KIT_APP_PREFIX", raising=False)
    monkeypatch.delenv("KIT_TELEMETRY_ENDPOINT", raising=False)
    yield tmp_path


# ---------------------------------------------------------------------------
# Mode / consent gating
# ---------------------------------------------------------------------------


class TestGating:
    def test_mode_off_is_noop(self, isolated, monkeypatch):
        monkeypatch.setenv("KIT_TELEMETRY_MODE", "off")
        sink = isolated / "out.jsonl"
        c = Client(sink="jsonl", sink_file=str(sink))
        c.record("x")
        c.shutdown(timeout=1)
        assert not sink.exists()
        assert c.dropped_count == 0

    def test_consent_denied_is_noop(self, isolated, monkeypatch, tmp_path):
        # Overwrite consent → denied.
        (tmp_path / "cfg" / "kit" / "telemetry.yaml").write_text(
            "telemetry:\n  consent:\n    state: denied\n"
        )
        sink = isolated / "out.jsonl"
        c = Client(sink="jsonl", sink_file=str(sink))
        c.record("x")
        c.shutdown(timeout=1)
        assert not sink.exists()


# ---------------------------------------------------------------------------
# JSONL sink
# ---------------------------------------------------------------------------


class TestJSONLSink:
    def test_writes_envelope(self, isolated, monkeypatch):
        # Flip to full mode so attrs survive end-to-end; anon-mode stripping
        # has its own dedicated test class below.
        monkeypatch.setenv("KIT_TELEMETRY_MODE", "full")
        sink = isolated / "out.jsonl"
        c = Client(sink="jsonl", sink_file=str(sink), sdk_version="9.9.9")
        c.record("my_event", {"k": 1})
        c.shutdown(timeout=2)

        lines = sink.read_text().splitlines()
        assert len(lines) == 1
        env = json.loads(lines[0])
        assert env["schema_version"] == "1"
        assert env["sdk_lang"] == "py"
        assert env["sdk_version"] == "9.9.9"
        assert env["mode"] == "full"
        assert env["event"] == "my_event"
        assert env["attrs"] == {"k": 1}
        assert env["installation_id"]  # non-empty
        assert env["occurred_at"]

    def test_multiple_events(self, isolated):
        sink = isolated / "out.jsonl"
        c = Client(sink="jsonl", sink_file=str(sink))
        for i in range(5):
            c.record(f"evt_{i}", {"i": i})
        c.shutdown(timeout=2)
        lines = sink.read_text().splitlines()
        assert len(lines) == 5
        names = [json.loads(line)["event"] for line in lines]
        assert names == [f"evt_{i}" for i in range(5)]


# ---------------------------------------------------------------------------
# Anon-mode attrs stripping (ADR-0038 §7)
# ---------------------------------------------------------------------------


class TestAnonModeStripsAttrs:
    """Anon-mode envelopes MUST drop the free-form ``attrs`` payload — even
    when the caller (or a custom redactor) populated it with PII. The shape
    matches the rs SDK's ``Value::Null``: the ``attrs`` key stays for
    envelope-shape stability, the payload is JSON null.
    """

    def test_anon_strips_attrs_with_obvious_pii(self, isolated, monkeypatch):
        # `isolated` already sets KIT_TELEMETRY_MODE=anon; be explicit anyway.
        monkeypatch.setenv("KIT_TELEMETRY_MODE", "anon")
        sink = isolated / "out.jsonl"
        c = Client(sink="jsonl", sink_file=str(sink))
        c.record(
            "user.signup",
            {
                "email": "alice@example.com",
                "ip": "192.168.1.42",
                "token": "sk-deadbeef12345678",
                "note": "totally not PII",
            },
        )
        c.shutdown(timeout=2)

        env = json.loads(sink.read_text().splitlines()[0])
        assert env["mode"] == "anon"
        # `attrs` key survives (shape stability) but payload is null.
        assert "attrs" in env
        assert env["attrs"] is None

    def test_full_mode_preserves_attrs(self, isolated, monkeypatch):
        # Regression guard: full mode must keep attrs intact (modulo the
        # default redactor, which we don't trip here).
        monkeypatch.setenv("KIT_TELEMETRY_MODE", "full")
        sink = isolated / "out.jsonl"
        c = Client(sink="jsonl", sink_file=str(sink))
        c.record("user.signup", {"plan": "pro", "seats": 3})
        c.shutdown(timeout=2)

        env = json.loads(sink.read_text().splitlines()[0])
        assert env["mode"] == "full"
        assert env["attrs"] == {"plan": "pro", "seats": 3}

    def test_anon_strips_attrs_even_when_custom_redactor_repopulates(self, isolated, monkeypatch):
        # A buggy / malicious caller redactor could try to smuggle PII back
        # into attrs. The anon-mode strip happens at envelope-build time
        # (before the redactor runs), so reinjection still nets ``None``
        # at sink time... unless the redactor explicitly resets attrs.
        # We assert envelope-build behavior here: the envelope handed to
        # the redactor already has ``attrs: None`` in anon mode.
        monkeypatch.setenv("KIT_TELEMETRY_MODE", "anon")
        sink = isolated / "out.jsonl"

        seen: list[Any] = []

        def custom(env: dict) -> dict:
            seen.append(env["attrs"])
            return env

        c = Client(sink="jsonl", sink_file=str(sink), redactor=custom)
        c.record("evt", {"email": "alice@example.com"})
        c.shutdown(timeout=2)

        # The custom redactor saw a None attrs payload, never the caller's
        # PII-laden dict.
        assert seen == [None]


# ---------------------------------------------------------------------------
# Redaction ordering
# ---------------------------------------------------------------------------


class TestRedactionOrdering:
    def test_custom_redactor_runs_before_default(self, isolated, monkeypatch):
        # Full mode so attrs survive into the envelope and into the
        # redactor pipeline (anon mode strips attrs unconditionally).
        monkeypatch.setenv("KIT_TELEMETRY_MODE", "full")
        sink = isolated / "out.jsonl"

        calls: list[str] = []

        def custom(env: dict) -> dict:
            # Mark presence; trust that the default still rewrites the email.
            calls.append("custom")
            env["attrs"]["touched"] = "user@example.com"
            return env

        c = Client(sink="jsonl", sink_file=str(sink), redactor=custom)
        c.record("evt", {"email": "user@example.com"})
        c.shutdown(timeout=2)

        env = json.loads(sink.read_text().splitlines()[0])
        # Default redactor ran AFTER custom and scrubbed both fields.
        assert calls == ["custom"]
        assert env["attrs"]["email"] == "<redacted:email>"
        assert env["attrs"]["touched"] == "<redacted:email>"

    def test_broken_custom_redactor_does_not_break_emission(self, isolated, monkeypatch):
        monkeypatch.setenv("KIT_TELEMETRY_MODE", "full")
        sink = isolated / "out.jsonl"

        def broken(env: dict) -> dict:
            raise RuntimeError("boom")

        c = Client(sink="jsonl", sink_file=str(sink), redactor=broken)
        c.record("evt", {"email": "user@example.com"})
        c.shutdown(timeout=2)

        env = json.loads(sink.read_text().splitlines()[0])
        # Default-only path still scrubs the email.
        assert env["attrs"]["email"] == "<redacted:email>"


# ---------------------------------------------------------------------------
# Backpressure / drop counter / latency
# ---------------------------------------------------------------------------


class TestBackpressure:
    def test_record_returns_quickly_under_saturation(self, isolated):
        sink = isolated / "out.jsonl"
        # Tiny queue, no consumer drain delay: under sustained calls we expect
        # SOME drops without record() ever blocking long.
        c = Client(sink="jsonl", sink_file=str(sink), queue_size=4)
        start = time.perf_counter()
        for i in range(1000):
            c.record("e", {"i": i})
        elapsed = time.perf_counter() - start
        c.shutdown(timeout=5)
        # 1000 calls under 1s in aggregate (~1ms each).
        assert elapsed < 1.0, f"record() too slow: {elapsed:.3f}s for 1000 calls"

    def test_dropped_count_increments_on_full_queue(self, isolated, monkeypatch):
        # Block the drain thread by mocking the sink to sleep.
        sink_path = isolated / "out.jsonl"
        c = Client(sink="jsonl", sink_file=str(sink_path), queue_size=2)

        # Hijack dispatch so the drain thread can't drain.
        drain_block = True

        def slow_dispatch(env):
            while drain_block:
                time.sleep(0.01)

        c._dispatch = slow_dispatch  # type: ignore[method-assign]

        for _ in range(100):
            c.record("e")

        # At least some should have been dropped.
        assert c.dropped_count > 0
        drain_block = False
        c.shutdown(timeout=3)


# ---------------------------------------------------------------------------
# Sync caller without asyncio loop
# ---------------------------------------------------------------------------


class TestCallerContext:
    def test_sync_caller_without_loop(self, isolated):
        # The current test thread has no running asyncio loop; record() must
        # still work via the daemon drain thread.
        import asyncio

        with pytest.raises(RuntimeError):
            asyncio.get_running_loop()

        sink = isolated / "out.jsonl"
        c = Client(sink="jsonl", sink_file=str(sink))
        c.record("evt", {"a": 1})
        c.shutdown(timeout=2)
        assert sink.exists()
        assert json.loads(sink.read_text().splitlines()[0])["event"] == "evt"

    def test_async_caller_with_loop(self, isolated):
        import asyncio

        sink = isolated / "out.jsonl"

        async def emit():
            c = Client(sink="jsonl", sink_file=str(sink))
            c.record("evt_async", {"a": 1})
            # Allow the daemon thread to drain.
            await asyncio.sleep(0.1)
            c.shutdown(timeout=2)

        asyncio.run(emit())
        assert sink.exists()
        assert json.loads(sink.read_text().splitlines()[0])["event"] == "evt_async"


# ---------------------------------------------------------------------------
# HTTPS sink (mocked)
# ---------------------------------------------------------------------------


class _FakeResponse:
    def __init__(self, status_code: int):
        self.status_code = status_code


class _FakeHttpxClient:
    """Mimics ``httpx.Client(...)`` context manager."""

    posts: list[tuple[str, bytes, dict]] = []
    queued_responses: list[Any] = []  # _FakeResponse or Exception

    def __init__(self, *args, **kwargs):
        pass

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def post(self, url, content=None, headers=None):
        self.posts.append((url, content, headers))
        if not self.queued_responses:
            return _FakeResponse(202)
        nxt = self.queued_responses.pop(0)
        if isinstance(nxt, Exception):
            raise nxt
        return nxt


class _FakeTimeout:
    def __init__(self, *a, **kw):
        pass


class _FakeHttpxModule:
    Client = _FakeHttpxClient
    Timeout = _FakeTimeout


@pytest.fixture
def fake_httpx(monkeypatch):
    _FakeHttpxClient.posts = []
    _FakeHttpxClient.queued_responses = []
    fake = _FakeHttpxModule()
    monkeypatch.setitem(__import__("sys").modules, "httpx", fake)
    yield fake


class TestHTTPSSink:
    def test_happy_path(self, isolated, monkeypatch, fake_httpx):
        monkeypatch.setenv("KIT_TELEMETRY_ENDPOINT", "https://t.example.com/v1")
        c = Client(sink="https")
        c.record("evt", {"k": 1})
        c.shutdown(timeout=2)

        assert len(_FakeHttpxClient.posts) == 1
        url, body, headers = _FakeHttpxClient.posts[0]
        assert url == "https://t.example.com/v1"
        assert headers["Content-Type"] == "application/x-ndjson"
        env = json.loads(body.decode("utf-8"))
        assert env["event"] == "evt"

    def test_5xx_retry_then_drop(self, isolated, monkeypatch, fake_httpx):
        monkeypatch.setenv("KIT_TELEMETRY_ENDPOINT", "https://t.example.com/v1")
        _FakeHttpxClient.queued_responses = [_FakeResponse(500), _FakeResponse(500)]
        c = Client(sink="https")
        c.record("evt")
        c.shutdown(timeout=2)
        assert len(_FakeHttpxClient.posts) == 2  # initial + 1 retry
        assert c.dropped_count == 1

    def test_endpoint_missing_drops(self, isolated, monkeypatch):
        # No KIT_TELEMETRY_ENDPOINT, no constructor arg → sink unusable.
        monkeypatch.delenv("KIT_TELEMETRY_ENDPOINT", raising=False)
        c = Client(sink="https")
        c.record("evt")
        c.shutdown(timeout=2)
        assert c.dropped_count == 1
