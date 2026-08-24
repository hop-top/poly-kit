"""Telemetry is exempt from ``--offline``.

``--offline`` stops network traffic the user asked for. It is not a
second consent gate on diagnostics: telemetry, and any future
remote-logging sink, keep emitting the way a remote syslog target would
not be muted by an offline flag. Consent and telemetry mode already
govern whether anything is emitted at all.

py's guard lives in urllib's opener chain, so the exemption holds only
while the telemetry sink stays off ``urllib.request.urlopen``. These
tests pin that structurally: they fail if the sink is ever moved onto
urllib, which would silently start suppressing telemetry under
``--offline``.
"""

from __future__ import annotations

import inspect

from hop_top_kit import netpolicy
from hop_top_kit.telemetry import client as telemetry_client


def _code_lines(module) -> str:
    """Source with comments and docstrings stripped.

    Comments in this repo legitimately mention urllib when explaining the
    exemption; only executable code should be asserted on.
    """
    import io
    import tokenize

    src = inspect.getsource(module)
    out: list[str] = []
    prev_type = tokenize.INDENT
    for tok in tokenize.generate_tokens(io.StringIO(src).readline):
        if tok.type == tokenize.COMMENT:
            continue
        # A STRING in statement position is a docstring; skip it.
        if tok.type == tokenize.STRING and prev_type in (
            tokenize.INDENT,
            tokenize.NEWLINE,
            tokenize.NL,
            tokenize.DEDENT,
        ):
            prev_type = tok.type
            continue
        if tok.type not in (tokenize.NL, tokenize.NEWLINE, tokenize.INDENT, tokenize.DEDENT):
            out.append(tok.string)
        prev_type = tok.type
    return " ".join(out)


def test_https_sink_does_not_use_urllib() -> None:
    """The sink must not route through the guarded opener chain."""
    code = _code_lines(telemetry_client)
    assert "urlopen" not in code, (
        "telemetry sink now calls urlopen, which netpolicy guards: "
        "--offline would suppress logging-class egress. Keep the sink on "
        "httpx, or add an explicit exemption."
    )
    assert "urllib" not in code, (
        "telemetry sink imports urllib; see above -- it must stay outside the guarded opener chain."
    )


def test_guard_installed_does_not_touch_telemetry_transport() -> None:
    """Installing the guard must not alter the telemetry sink's client."""
    netpolicy.set_offline(True)
    try:
        netpolicy.install()
        # httpx is the sink's transport; the guard only wraps urllib's
        # opener, so httpx must remain unpatched.
        try:
            import httpx
        except ImportError:
            return  # optional dep absent; structural test above still applies
        assert not hasattr(httpx.Client, "_hop_top_offline_guarded"), (
            "netpolicy.install() patched httpx: telemetry is logging-class "
            "egress and must stay exempt from --offline"
        )
    finally:
        netpolicy.set_offline(False)


def test_user_traffic_still_refused_while_offline() -> None:
    """The exemption stays narrow: ordinary egress is still refused."""
    import urllib.request

    netpolicy.set_offline(True)
    netpolicy.install()
    try:
        try:
            urllib.request.urlopen("https://example.com/", timeout=1)
        except netpolicy.OfflineError:
            pass
        else:
            raise AssertionError("user-initiated request allowed while offline")
    finally:
        netpolicy.set_offline(False)
