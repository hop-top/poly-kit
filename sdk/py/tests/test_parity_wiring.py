"""Wiring guards: verbosity and streams behavior must FOLLOW the contract.

``tests/test_parity.py`` proves the contract LOADS. These tests prove the CLI
READS it — the difference between a contract that is honest and one that is
load-bearing. A port that merely imports the loader has gained nothing.

Two kinds of test live here, and the distinction matters:

``*_follows_contract_not_literals``
    Feed a CONSTRUCTED contract whose values diverge from the shipped one and
    assert behavior moves with it. These FAIL if a literal is restored, so
    they are what pins the wiring. Load-bearing.

``*_matches_shipped_contract``
    Assert current behavior equals the shipped contract's values. These pin
    the REQUIREMENT that this refactor changed nothing observable, and keep
    passing under mutation by design. Useful, not load-bearing.

Nothing here mutates ``contracts/parity/parity.json``: it is shared with the
other ports' drift guards, so divergence is injected per call instead.
"""

from __future__ import annotations

import io
import logging
from typing import Any
from unittest import mock

import typer
from typer.testing import CliRunner

from hop_top_kit import log as kitlog
from hop_top_kit import parity
from hop_top_kit.cli import (
    create_app,
    stream_flag_name,
    stream_label,
    stream_output,
    verbosity_flag_usage,
    verbosity_shorthand,
)

runner = CliRunner()


def _contract(**overrides: Any) -> dict[str, Any]:
    """A parity contract deliberately unlike the shipped one.

    Every value differs from ``contracts/parity/parity.json`` so a literal
    left behind in the port cannot coincidentally satisfy an assertion.
    """
    data: dict[str, Any] = {
        "verbosity": {
            "flag": "-d",
            "levels": {"0": "warn", "1": "info", "2": "debug", "3": "trace"},
            "quiet_override": "error",
        },
        "streams": {
            "flag": "--channel",
            "label_format": "<<{name}>>",
            "output": "stdout",
        },
    }
    data.update(overrides)
    return data


# ---------------------------------------------------------------------------
# verbosity.levels — -V count → log level
# ---------------------------------------------------------------------------


class TestVerbosityLevels:
    def test_follows_contract_not_literals(self) -> None:
        """A four-level contract shifts every count, including the ceiling."""
        c = _contract()
        assert kitlog.verbosity_level(0, data=c) == (logging.WARNING, "warning")
        assert kitlog.verbosity_level(1, data=c) == (logging.INFO, "info")
        assert kitlog.verbosity_level(2, data=c) == (logging.DEBUG, "debug")
        assert kitlog.verbosity_level(3, data=c) == (kitlog.TRACE, "trace")

    def test_saturates_at_highest_declared_count(self) -> None:
        """Counts above the top key stay at the top key's level."""
        c = _contract()
        assert kitlog.verbosity_level(9, data=c) == (kitlog.TRACE, "trace")

    def test_matches_shipped_contract(self) -> None:
        """Requirement: shipped mapping is 0=info, 1=debug, 2+=trace."""
        assert kitlog.verbosity_level(0) == (logging.INFO, "info")
        assert kitlog.verbosity_level(1) == (logging.DEBUG, "debug")
        assert kitlog.verbosity_level(2) == (kitlog.TRACE, "trace")
        assert kitlog.verbosity_level(3) == (kitlog.TRACE, "trace")


# ---------------------------------------------------------------------------
# verbosity.quiet_override — --quiet forces a level
# ---------------------------------------------------------------------------


class TestVerbosityQuietOverride:
    def test_follows_contract_not_literals(self) -> None:
        c = _contract()
        assert kitlog.quiet_level(c) == (logging.ERROR, "error")
        # quiet wins over any count, at the contract's level.
        assert kitlog.verbosity_level(3, quiet=True, data=c) == (logging.ERROR, "error")

    def test_matches_shipped_contract(self) -> None:
        """Requirement: --quiet overrides to warn."""
        assert kitlog.quiet_level() == (logging.WARNING, "warning")
        assert kitlog.verbosity_level(2, quiet=True) == (logging.WARNING, "warning")

    def test_quiet_suppresses_info_end_to_end(self) -> None:
        """Requirement: the override reaches real emitted output."""
        buf = io.StringIO()
        with mock.patch("sys.stderr", buf):
            lg = kitlog.with_verbose(2, quiet=True, no_color=True)
            lg.info("hidden")
            lg.warn("visible")
        assert "hidden" not in buf.getvalue()
        assert "WARN" in buf.getvalue()


# ---------------------------------------------------------------------------
# verbosity.flag — the stackable shorthand
# ---------------------------------------------------------------------------


class TestVerbosityFlag:
    def test_follows_contract_not_literals(self) -> None:
        assert verbosity_shorthand(_contract()) == "-d"

    def test_matches_shipped_contract(self) -> None:
        assert verbosity_shorthand() == "-V"

    def test_registered_shorthand_parses(self) -> None:
        """Requirement: the shipped shorthand still stacks on a real app."""
        app, _ = create_app(name="vt", version="0.1.0", help="t")

        @app.command()
        def show() -> None:
            from hop_top_kit.cli import verbose_count

            typer.echo(f"v={verbose_count()}")

        assert "v=0" in runner.invoke(app, ["show"]).output
        assert "v=1" in runner.invoke(app, ["-V", "show"]).output
        assert "v=2" in runner.invoke(app, ["-VV", "show"]).output
        assert "v=3" in runner.invoke(app, ["-VVV", "show"]).output
        assert "v=0" in runner.invoke(app, ["-VV", "--quiet", "show"]).output


# ---------------------------------------------------------------------------
# verbosity level names embedded in -V help text
# ---------------------------------------------------------------------------


class TestVerbosityFlagUsage:
    def test_follows_contract_not_literals(self) -> None:
        """Both the shorthand and every level name are generated."""
        usage = verbosity_flag_usage(_contract())
        assert usage == "Increase log verbosity (-d=info, -dd=debug, -ddd=trace)"

    def test_matches_shipped_contract(self) -> None:
        assert verbosity_flag_usage() == "Increase log verbosity (-V=debug, -VV=trace)"

    def test_usage_reaches_help_output(self) -> None:
        """Requirement: generated text is what --help actually prints."""
        app, _ = create_app(name="vt", version="0.1.0", help="t")
        out = runner.invoke(app, ["--help"]).output
        assert "-V=debug, -VV=trace" in out


# ---------------------------------------------------------------------------
# streams.label_format — line prefix
# ---------------------------------------------------------------------------


class TestStreamLabel:
    def test_follows_contract_not_literals(self) -> None:
        assert stream_label("audit", _contract()) == "<<audit>> "

    def test_matches_shipped_contract(self) -> None:
        assert stream_label("audit") == "[audit] "

    def test_label_reaches_written_lines(self) -> None:
        """Requirement: shipped label prefixes every emitted line."""
        from hop_top_kit.cli import _get_enabled_streams, channel, register_stream

        register_stream("lblcmd", "audit", "Audit trail")
        _get_enabled_streams()["lblcmd"] = {"audit"}
        buf = io.StringIO()
        with mock.patch("sys.stderr", buf):
            channel("lblcmd", "audit").write("alpha\nbeta\n")
        assert buf.getvalue() == "[audit] alpha\n[audit] beta\n"


# ---------------------------------------------------------------------------
# streams.output — destination
# ---------------------------------------------------------------------------


class TestStreamOutput:
    def test_follows_contract_not_literals(self) -> None:
        """A contract naming stdout must route there, not to stderr."""
        outbuf, errbuf = io.StringIO(), io.StringIO()
        with mock.patch("sys.stdout", outbuf), mock.patch("sys.stderr", errbuf):
            dest = stream_output(_contract())
            dest.write("routed\n")
        assert outbuf.getvalue() == "routed\n"
        assert errbuf.getvalue() == ""

    def test_matches_shipped_contract(self) -> None:
        outbuf, errbuf = io.StringIO(), io.StringIO()
        with mock.patch("sys.stdout", outbuf), mock.patch("sys.stderr", errbuf):
            stream_output().write("routed\n")
        assert errbuf.getvalue() == "routed\n"
        assert outbuf.getvalue() == ""


# ---------------------------------------------------------------------------
# streams.flag — the --stream flag name
# ---------------------------------------------------------------------------


class TestStreamFlag:
    def test_follows_contract_not_literals(self) -> None:
        assert stream_flag_name(_contract()) == "--channel"

    def test_matches_shipped_contract(self) -> None:
        assert stream_flag_name() == "--stream"

    def test_registered_flag_parses(self) -> None:
        """Requirement: the shipped flag name still enables a stream."""
        from hop_top_kit.cli import _get_enabled_streams, channel, register_stream

        app, _ = create_app(name="st", version="0.1.0", help="t")

        @app.command()
        def emit() -> None:
            register_stream("emit", "audit", "Audit trail")
            channel("emit", "audit").write("line\n")

        result = runner.invoke(app, ["--stream", "audit", "emit"])
        assert result.exit_code == 0
        assert "audit" in _get_enabled_streams().get("emit", set())


# ---------------------------------------------------------------------------
# Accessors read the same block the CLI does
# ---------------------------------------------------------------------------


class TestWiringReadsLoader:
    """No port-local copy: the values used are the loader's values."""

    def test_verbosity_sources_agree(self) -> None:
        assert verbosity_shorthand() == parity.VERBOSITY_FLAG
        for count, name in parity.VERBOSITY_LEVELS.items():
            assert kitlog.verbosity_level(int(count)) == kitlog.level_for_name(name)
        assert kitlog.quiet_level() == kitlog.level_for_name(parity.VERBOSITY_QUIET_OVERRIDE)

    def test_streams_sources_agree(self) -> None:
        assert stream_flag_name() == parity.STREAMS_FLAG
        assert stream_label("x") == parity.STREAMS_LABEL_FORMAT.replace("{name}", "x") + " "
