"""Tests for KitEngine spawn and lifecycle."""

from __future__ import annotations

import json
from unittest.mock import MagicMock, patch

import responses
from kit_engine import KitEngine


@responses.activate
def test_connect_existing():
    responses.add(
        responses.GET,
        "http://localhost:9999/health",
        json={"status": "ok", "pid": 42},
        status=200,
    )
    engine = KitEngine.connect(9999)
    assert engine.port == 9999
    assert engine.pid == 42


@patch("kit_engine._binary.find_kit_binary", return_value="/usr/local/bin/kit")
@patch("subprocess.Popen")
@responses.activate
def test_start_spawns_process(mock_popen, mock_find):
    mock_proc = MagicMock()
    mock_proc.stdout.readline.return_value = json.dumps({"port": 8080, "pid": 100})
    mock_popen.return_value = mock_proc

    responses.add(
        responses.GET,
        "http://localhost:8080/health",
        json={"status": "ok"},
        status=200,
    )

    engine = KitEngine.start(app="myapp", encrypt=True)
    assert engine.port == 8080
    assert engine.pid == 100

    cmd = mock_popen.call_args[0][0]
    assert "serve" in cmd
    assert "--app" in cmd
    assert "--encrypt" in cmd


def test_start_no_binary():
    with patch(
        "kit_engine._binary.find_kit_binary",
        side_effect=RuntimeError("kit binary not found"),
    ):
        try:
            KitEngine.start()
            raise AssertionError("should raise")
        except RuntimeError as e:
            assert "not found" in str(e)


@responses.activate
def test_stop_posts_shutdown():
    responses.add(responses.POST, "http://localhost:5555/shutdown", status=200)
    engine = KitEngine(port=5555, pid=1)
    engine.stop()
    assert len(responses.calls) == 1
