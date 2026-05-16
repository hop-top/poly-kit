"""Tests for hop_top_kit.log — structured logger matching Go kit/log."""

import io
from unittest import mock

from hop_top_kit.log import create_logger

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _capture(fn) -> str:
    buf = io.StringIO()
    with mock.patch("sys.stderr", buf):
        fn()
    return buf.getvalue()


# ---------------------------------------------------------------------------
# Format
# ---------------------------------------------------------------------------


class TestLogFormat:
    def test_writes_level_msg_to_stderr(self):
        log = create_logger()
        out = _capture(lambda: log.info("hello"))
        assert "INFO" in out
        assert "hello" in out
        assert out.endswith("\n")

    def test_formats_key_val_pairs(self):
        log = create_logger()
        out = _capture(lambda: log.info("start", port=8080, host="localhost"))
        assert "port=8080" in out
        assert "host=localhost" in out

    def test_correct_level_prefixes(self):
        log = create_logger()
        out = _capture(
            lambda: (
                log.error("e"),
                log.warn("w"),
                log.info("i"),
                log.debug("d"),
            )
        )
        assert "ERRO" in out
        assert "WARN" in out
        assert "INFO" in out
        assert "DEBU" in out


# ---------------------------------------------------------------------------
# Color
# ---------------------------------------------------------------------------


class TestLogColor:
    def test_includes_ansi_by_default(self):
        log = create_logger()
        out = _capture(lambda: log.error("fail"))
        assert "\x1b[" in out

    def test_strips_ansi_when_no_color(self):
        log = create_logger(no_color=True)
        out = _capture(lambda: log.error("fail"))
        assert "\x1b[" not in out


# ---------------------------------------------------------------------------
# Quiet
# ---------------------------------------------------------------------------


class TestLogQuiet:
    def test_suppresses_info(self):
        log = create_logger(quiet=True)
        out = _capture(lambda: log.info("hidden"))
        assert out == ""

    def test_suppresses_debug(self):
        log = create_logger(quiet=True)
        out = _capture(lambda: log.debug("hidden"))
        assert out == ""

    def test_shows_warn(self):
        log = create_logger(quiet=True)
        out = _capture(lambda: log.warn("visible"))
        assert "WARN" in out

    def test_shows_error(self):
        log = create_logger(quiet=True)
        out = _capture(lambda: log.error("visible"))
        assert "ERRO" in out


# ---------------------------------------------------------------------------
# Key-value edge cases
# ---------------------------------------------------------------------------


class TestLogKeyValueEdgeCases:
    def test_quotes_values_with_spaces(self):
        log = create_logger(no_color=True)
        out = _capture(lambda: log.info("msg", path="/my dir/file"))
        assert 'path="/my dir/file"' in out

    def test_handles_empty_kwargs(self):
        log = create_logger(no_color=True)
        out = _capture(lambda: log.info("msg"))
        assert "INFO" in out
        assert "msg" in out
