"""End-to-end guarantee for the ``--offline`` global.

Mirrors ``go/console/cli/offline_e2e_test.go``. Registering the flag alone
is advisory: any adopter who never heard of ``is_offline`` still reaches the
wire. These tests pin the enforcement, not the registration.

``--offline`` is a root-level global, so it precedes the subcommand
(``prog --offline fetch``) exactly like its siblings ``--quiet`` and
``--format``. Click does not hoist root options onto children the way
cobra's persistent flags do; matching the port's own globals is the parity
target here, not cobra's argument positions.
"""

from __future__ import annotations

import urllib.error
import urllib.request

import pytest
import typer
from typer.testing import CliRunner

from hop_top_kit import netpolicy
from hop_top_kit.cli import create_app, is_offline

runner = CliRunner()


@pytest.fixture(autouse=True)
def restore_process_state():
    """Undo the process-global mutations ``--offline`` performs."""
    saved_opener = urllib.request._opener
    token = netpolicy._OFFLINE.set(False)
    yield
    netpolicy._OFFLINE.reset(token)
    urllib.request._opener = saved_opener


def test_offline_flag_registered():
    """``--offline`` must appear in the root help alongside the other globals."""
    app, _ = create_app(name="probe", version="0.0.0", help="probe")
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    assert "--offline" in result.output


def test_naive_leaf_is_refused_under_offline():
    """A leaf using module-level ``urlopen`` — no offline check anywhere —
    must still be refused. This is the guarantee the parity guide promises.
    """
    app, _ = create_app(name="probe", version="0.0.0", help="probe")
    seen: dict[str, BaseException | None] = {"err": None}

    @app.command()
    def fetch() -> None:
        try:
            # naive: no is_offline() check
            urllib.request.urlopen("https://example.invalid/x", timeout=1)
        except BaseException as e:
            seen["err"] = e

    result = runner.invoke(app, ["--offline", "fetch"])
    assert result.exit_code == 0, result.output
    assert isinstance(seen["err"], netpolicy.OfflineError), (
        f"naive leaf reached the network under --offline: {seen['err']!r}"
    )


def test_leaf_without_offline_flag_is_untouched():
    """Without ``--offline`` the marker stays clear for the leaf."""
    app, _ = create_app(name="probe", version="0.0.0", help="probe")
    seen: dict[str, bool | None] = {"off": None}

    @app.command()
    def check() -> None:
        seen["off"] = is_offline()

    result = runner.invoke(app, ["check"])
    assert result.exit_code == 0, result.output
    assert seen["off"] is False


def test_leaf_reads_marker_when_offline():
    """Leaves that DO consult the marker see it set, so per-command network
    opt-ins can force their opt-out on.
    """
    app, _ = create_app(name="probe", version="0.0.0", help="probe")
    seen: dict[str, bool | None] = {"off": None}

    @app.command()
    def check() -> None:
        seen["off"] = is_offline()

    result = runner.invoke(app, ["--offline", "check"])
    assert result.exit_code == 0, result.output
    assert seen["off"] is True


def test_offline_never_unsets_an_explicit_no_flag():
    """``--offline`` forces opt-outs ON; it must not flip an explicitly
    passed ``--no-*`` back off.
    """
    app, _ = create_app(name="probe", version="0.0.0", help="probe")
    seen: dict[str, object] = {}

    @app.command()
    def sync(push: bool = typer.Option(True, "--push/--no-push")) -> None:
        # Adopter pattern: offline forces the opt-out on, never off.
        seen["effective_push"] = push and not is_offline()
        seen["raw_push"] = push

    assert runner.invoke(app, ["--offline", "sync", "--no-push"]).exit_code == 0
    assert seen["effective_push"] is False
    assert seen["raw_push"] is False

    assert runner.invoke(app, ["--offline", "sync", "--push"]).exit_code == 0
    assert seen["effective_push"] is False

    assert runner.invoke(app, ["sync", "--push"]).exit_code == 0
    assert seen["effective_push"] is True


def test_loopback_leaf_still_works_under_offline():
    """A leaf talking to a local peer must keep working under ``--offline``."""
    app, _ = create_app(name="probe", version="0.0.0", help="probe")
    seen: dict[str, BaseException | None] = {"err": None}

    @app.command()
    def ping() -> None:
        try:
            urllib.request.urlopen("http://127.0.0.1:1/health", timeout=1)
        except BaseException as e:
            seen["err"] = e

    result = runner.invoke(app, ["--offline", "ping"])
    assert result.exit_code == 0, result.output
    # Port 1 refuses the connection — that is the guard letting it through
    # to the real handler, which is the assertion. An OfflineError would
    # mean loopback was wrongly blocked.
    assert not isinstance(seen["err"], netpolicy.OfflineError), (
        "loopback was blocked under --offline"
    )


def test_marker_does_not_leak_across_invocations():
    """A second invocation without ``--offline`` must not inherit the first's
    marker.

    The Go port carries the marker on the per-command context, so it cannot
    outlive a dispatch. Python's ContextVar is process-scoped, so the value
    has to be re-stamped every run — otherwise a long-lived host (a REPL, a
    test harness, an embedding process) refuses requests nobody asked to
    block.
    """
    app, _ = create_app(name="probe", version="0.0.0", help="probe")
    seen: list[bool] = []

    @app.command()
    def check() -> None:
        seen.append(is_offline())

    assert runner.invoke(app, ["--offline", "check"]).exit_code == 0
    assert runner.invoke(app, ["check"]).exit_code == 0

    assert seen == [True, False], "offline marker leaked into the next invocation"
