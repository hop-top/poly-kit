"""Tests for probe core logic."""

from __future__ import annotations

import os
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from threading import Thread

from kit_imports import kit_bus, kit_config, kit_progress

_config = kit_config()
_bus_mod = kit_bus()
_progress_mod = kit_progress()

Options = _config.Options
load = _config.load
create_bus = _bus_mod.create_bus
ProgressReporter = _progress_mod.ProgressReporter

from core import Result, check_targets  # noqa: E402


# ---------------------------------------------------------------------------
# Mock server helper
# ---------------------------------------------------------------------------

class _Handler(BaseHTTPRequestHandler):
    status_code = 200

    def do_GET(self) -> None:  # noqa: N802
        self.send_response(self.status_code)
        self.end_headers()

    def log_message(self, *_args: object) -> None:  # noqa: ANN002
        pass  # suppress stderr


def _start_server(status_code: int = 200) -> tuple[HTTPServer, str]:
    _Handler.status_code = status_code
    server = HTTPServer(("127.0.0.1", 0), _Handler)
    port = server.server_address[1]
    t = Thread(target=server.serve_forever, daemon=True)
    t.start()
    return server, f"http://127.0.0.1:{port}"


class _DevNull:
    """Writable no-op for progress output."""

    def write(self, _s: str) -> int:
        return 0

    def isatty(self) -> bool:
        return False


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

class TestConfigLoading:
    def test_load_probe_yaml(self) -> None:
        cfg: dict = {"interval": "", "targets": []}
        cfg_path = os.path.join(
            os.path.dirname(__file__), "..", "probe.yaml",
        )
        load(cfg, Options(project_config_path=cfg_path))
        assert len(cfg["targets"]) > 0
        assert cfg["interval"] == "30s"


class TestCheckTargets:
    def test_passing_target_emits_check(self) -> None:
        server, url = _start_server(200)
        try:
            cfg = {
                "targets": [
                    {
                        "name": "mock",
                        "url": url,
                        "method": "GET",
                        "timeout": "2s",
                        "expect": {"status": 200},
                    },
                ],
            }
            b = create_bus()
            events: list = []
            b.subscribe("kit.probe.#", lambda e: events.append(e))

            progress = ProgressReporter(_DevNull(), False)
            results = check_targets(cfg, b, progress)
            b.close()

            assert len(results) == 1
            assert results[0].ok is True
            assert results[0].status == 200
            assert len(events) >= 1
            assert events[0].topic == "kit.probe.check.executed"
        finally:
            server.shutdown()

    def test_failing_target_emits_alert(self) -> None:
        server, url = _start_server(500)
        try:
            cfg = {
                "targets": [
                    {
                        "name": "bad",
                        "url": url,
                        "method": "GET",
                        "timeout": "2s",
                        "expect": {"status": 200},
                    },
                ],
            }
            b = create_bus()
            topics: list[str] = []
            b.subscribe("kit.probe.#", lambda e: topics.append(e.topic))

            progress = ProgressReporter(_DevNull(), False)
            results = check_targets(cfg, b, progress)
            b.close()

            assert len(results) == 1
            assert results[0].ok is False
            assert "kit.probe.check.executed" in topics
            assert "kit.probe.check.alerted" in topics
        finally:
            server.shutdown()
