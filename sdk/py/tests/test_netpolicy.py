"""Semantics of the process-wide offline marker and its urllib chokepoint.

Mirrors ``go/core/netpolicy/netpolicy_test.go``: the same five guarantees,
expressed against urllib's opener chain instead of ``http.RoundTripper``.
"""

from __future__ import annotations

import email.message
import ssl
import urllib.request

import pytest

from hop_top_kit import netpolicy


class _Recorder(urllib.request.BaseHandler):
    """Stands in for the real protocol handlers and records reachability.

    ``handler_order`` sits below ``UnknownHandler`` so it wins the
    ``default_open`` round for every scheme, letting a test observe whether
    the guard let a request through without opening a socket.
    """

    handler_order = 200

    def __init__(self) -> None:
        self.reached = False

    def default_open(self, req: urllib.request.Request):
        self.reached = True
        return _Resp()


class _Resp:
    """Minimal stand-in for ``http.client.HTTPResponse``.

    ``code``/``msg``/``info()`` are what ``HTTPErrorProcessor`` reads on the
    way back out of ``OpenerDirector.open``; without them the response
    post-processing chain raises before the test can assert.
    """

    code = 204
    status = 204
    msg = "No Content"

    def info(self) -> email.message.Message:
        return email.message.Message()

    def read(self, *_a: object) -> bytes:
        return b""

    def close(self) -> None:
        return None

    def __enter__(self) -> _Resp:
        return self

    def __exit__(self, *_a: object) -> None:
        return None


def _opener(rec: _Recorder) -> urllib.request.OpenerDirector:
    return netpolicy.guard(urllib.request.build_opener(rec))


def test_blocks_external_when_offline():
    """A marked process must stop the request before it reaches the wire.

    The destination is external: loopback is exempt by design.
    """
    rec = _Recorder()
    op = _opener(rec)
    netpolicy.set_offline(True)

    with pytest.raises(netpolicy.OfflineError) as ei:
        op.open("https://example.invalid/v1/thing")

    assert "example.invalid" in str(ei.value)
    assert not rec.reached, "request reached the handler despite offline marker"


_SECRET_URL = (
    "https://alice:pw-secret@example.invalid/v1/models/m:generate?alt=sse&key=q-secret#frag-secret"
)
_LEAKS = ("alice", "pw-secret", "q-secret", "key=", "alt=sse", "frag-secret", "?", "#", "@")


@pytest.mark.parametrize("method", ["GET", "POST"])
def test_refusal_omits_query_fragment_userinfo(method: str):
    """The refusal names where the request was going, never what it carried.

    Scheme, host and path only: query, fragment and userinfo may hold
    credentials (an API key param, basic-auth userinfo) and are dropped.
    Mirrors Go's ``TestGuard_RefusalOmitsQueryFragmentUserinfo``.
    """
    rec = _Recorder()
    op = _opener(rec)
    netpolicy.set_offline(True)

    data = b"{}" if method == "POST" else None
    with pytest.raises(netpolicy.OfflineError) as ei:
        op.open(urllib.request.Request(_SECRET_URL, data=data, method=method))

    msg = str(ei.value)
    assert msg == (
        f"{method} https://example.invalid/v1/models/m:generate: network disabled by --offline"
    )
    for leak in _LEAKS:
        assert leak not in msg, f"refusal {msg!r} carries {leak!r}"
    assert isinstance(ei.value, OSError)
    assert not rec.reached


def test_refusal_keeps_port():
    """The port is part of where the request was going, so it stays."""
    netpolicy.set_offline(True)

    with pytest.raises(netpolicy.OfflineError) as ei:
        _opener(_Recorder()).open("http://u:p@example.invalid:8443/x?key=q-secret")

    assert str(ei.value) == "GET http://example.invalid:8443/x: network disabled by --offline"


@pytest.mark.parametrize(
    ("url", "want"),
    [
        # Opaque: the part after the scheme is not a path, so none of it shows.
        ("mailto:alice@example.invalid?subject=q-secret", "mailto:"),
        # Unparseable: cannot be stripped reliably, so none of it shows.
        ("http://[::1/x?key=q-secret", "<unparseable URL>"),
    ],
)
def test_destination_edge_cases(url: str, want: str):
    """Targets the guard cannot render as scheme://host/path echo nothing."""
    assert netpolicy._destination(url) == want


def test_allows_when_not_offline():
    """An unmarked process must be entirely unaffected."""
    rec = _Recorder()
    op = _opener(rec)

    op.open("https://example.invalid/v1/thing")

    assert rec.reached


@pytest.mark.parametrize(
    "target",
    [
        "http://127.0.0.1:8080/health",
        "http://localhost:9000/health",
        "http://[::1]:9000/health",
    ],
)
def test_allows_loopback_when_offline(target: str):
    """Loopback stays reachable: --offline means no network, not no self."""
    rec = _Recorder()
    op = _opener(rec)
    netpolicy.set_offline(True)

    op.open(target)

    assert rec.reached, f"{target}: loopback request was blocked"


def test_blocks_dns_names_when_offline():
    """A DNS name is remote even if it might resolve to loopback.

    Resolving it is itself network access.
    """
    rec = _Recorder()
    op = _opener(rec)
    netpolicy.set_offline(True)

    with pytest.raises(netpolicy.OfflineError):
        op.open("http://my-host.internal/health")

    assert not rec.reached, "DNS-named host was allowed through"


def test_allows_non_network_schemes_when_offline(tmp_path):
    """``file:`` and ``data:`` never touch the network, so they stay open."""
    rec = _Recorder()
    op = _opener(rec)
    netpolicy.set_offline(True)

    op.open("data:text/plain,hi")

    assert rec.reached


def test_set_offline_false_leaves_marker_clean():
    """``set_offline(False)`` must not mark the process."""
    netpolicy.set_offline(False)
    assert netpolicy.is_offline() is False


def test_offline_error_is_oserror():
    """The typed error must be catchable as an ordinary transport failure.

    urllib callers already guard ``OSError``/``URLError``; an error outside
    that hierarchy would escape their handling and surface as a crash.
    """
    assert issubclass(netpolicy.OfflineError, OSError)


def test_guard_is_idempotent():
    """Guarding an already guarded opener must not double-wrap."""
    once = netpolicy.guard(urllib.request.build_opener())
    assert netpolicy.guard(once) is once
    assert netpolicy.guard(None) is not None


def test_install_guards_module_level_urlopen():
    """``install()`` must guard the opener ``urllib.request.urlopen`` uses.

    That is the chokepoint beneath every naive caller in the port: neither
    ``aim`` nor ``upgrade`` builds its own opener.
    """
    netpolicy.install()
    netpolicy.install()  # idempotent
    netpolicy.set_offline(True)

    with pytest.raises(netpolicy.OfflineError):
        urllib.request.urlopen("https://example.invalid/x", timeout=1)


def test_install_guards_urlopen_with_ssl_context():
    """``urlopen(..., context=...)`` builds a fresh opener per call instead
    of using the installed one. That opener must be guarded too, or passing
    a custom CA bundle would be a way around ``--offline``.
    """
    netpolicy.install()
    netpolicy.set_offline(True)

    with pytest.raises(netpolicy.OfflineError) as ei:
        urllib.request.urlopen(
            "https://example.invalid/x?key=q-secret",
            timeout=1,
            context=ssl.create_default_context(),
        )

    assert str(ei.value) == "GET https://example.invalid/x: network disabled by --offline"


def test_install_guards_openers_built_afterwards():
    """An opener built through ``urllib.request.build_opener`` after
    ``install()`` carries the guard without the caller wrapping it.
    """
    netpolicy.install()
    netpolicy.set_offline(True)
    rec = _Recorder()

    with pytest.raises(netpolicy.OfflineError):
        urllib.request.build_opener(rec).open("https://example.invalid/x")

    assert not rec.reached


def test_install_wraps_build_opener_once():
    """Repeated ``install()`` must not stack wrappers on ``build_opener``."""
    netpolicy.install()
    wrapped = urllib.request.build_opener
    netpolicy.install()

    assert urllib.request.build_opener is wrapped
    handlers = urllib.request.build_opener().handlers
    assert sum(isinstance(h, netpolicy._OfflineHandler) for h in handlers) == 1
