"""Semantics of the process-wide offline marker and its urllib chokepoint.

Mirrors ``go/core/netpolicy/netpolicy_test.go``: the same five guarantees,
expressed against urllib's opener chain instead of ``http.RoundTripper``.
"""

from __future__ import annotations

import email.message
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


@pytest.fixture
def clean_marker():
    """Reset the offline marker after each test."""
    token = netpolicy._OFFLINE.set(False)
    yield
    netpolicy._OFFLINE.reset(token)


def _opener(rec: _Recorder) -> urllib.request.OpenerDirector:
    return netpolicy.guard(urllib.request.build_opener(rec))


def test_blocks_external_when_offline(clean_marker):
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


def test_allows_when_not_offline(clean_marker):
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
def test_allows_loopback_when_offline(clean_marker, target: str):
    """Loopback stays reachable: --offline means no network, not no self."""
    rec = _Recorder()
    op = _opener(rec)
    netpolicy.set_offline(True)

    op.open(target)

    assert rec.reached, f"{target}: loopback request was blocked"


def test_blocks_dns_names_when_offline(clean_marker):
    """A DNS name is remote even if it might resolve to loopback.

    Resolving it is itself network access.
    """
    rec = _Recorder()
    op = _opener(rec)
    netpolicy.set_offline(True)

    with pytest.raises(netpolicy.OfflineError):
        op.open("http://my-host.internal/health")

    assert not rec.reached, "DNS-named host was allowed through"


def test_allows_non_network_schemes_when_offline(clean_marker, tmp_path):
    """``file:`` and ``data:`` never touch the network, so they stay open."""
    rec = _Recorder()
    op = _opener(rec)
    netpolicy.set_offline(True)

    op.open("data:text/plain,hi")

    assert rec.reached


def test_set_offline_false_leaves_marker_clean(clean_marker):
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


def test_install_guards_module_level_urlopen(clean_marker):
    """``install()`` must guard the opener ``urllib.request.urlopen`` uses.

    That is the chokepoint beneath every naive caller in the port: neither
    ``aim`` nor ``upgrade`` builds its own opener.
    """
    saved = urllib.request._opener
    try:
        netpolicy.install()
        netpolicy.install()  # idempotent
        netpolicy.set_offline(True)

        with pytest.raises(netpolicy.OfflineError):
            urllib.request.urlopen("https://example.invalid/x", timeout=1)
    finally:
        urllib.request._opener = saved
