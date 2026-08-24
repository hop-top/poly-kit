"""
hop_top_kit.netpolicy — the process-wide network policy marker and the
urllib opener that enforces it.

The ``--offline`` global (cli-parity-guide, "Global Flags") promises that
network access is disabled. Setting a marker alone cannot keep that
promise: it is advisory, so any caller that forgets to consult it still
reaches the wire. :func:`guard` closes that gap by refusing the request
inside urllib's opener chain, beneath every ``urlopen``, where no caller
can route around it.

Loopback is deliberately exempt. ``--offline`` means "do not talk to the
network", not "do not talk to myself": a local ``kit serve`` peer, a dev
backend on 127.0.0.1 and unix sockets stay reachable so offline workflows
remain usable.

Scope
-----
:func:`guard` sits in the ``urllib.request`` opener chain, so it covers
HTTP and HTTPS through ``urllib`` — which is every network client in the
Python port today (``hop_top_kit.aim``, ``hop_top_kit.upgrade``). It does
NOT cover code that opens a socket directly: raw ``socket``, ``httpx``
(the optional telemetry HTTPS sink), ``grpc`` (``routellm_grpc``),
``http.client`` used without urllib, or DB-API drivers. For those,
``--offline`` remains advisory and the call site must consult
:func:`is_offline` itself. Closing that gap needs a wrapped socket factory
threaded through each such client; it is deliberately not attempted here.

This mirrors ``go/core/netpolicy`` — same five guarantees, expressed
against urllib's opener chain instead of ``http.RoundTripper``.
"""

from __future__ import annotations

import contextvars
import ipaddress
import urllib.request
from urllib.parse import urlsplit

__all__ = [
    "OfflineError",
    "guard",
    "install",
    "is_offline",
    "set_offline",
]


class OfflineError(OSError):
    """Raised by the guard when a request is attempted while offline.

    Subclasses ``OSError`` so it lands in the same ``except`` clauses
    urllib callers already write for transport failures — an error outside
    that hierarchy would escape their handling and surface as a crash.
    """


_OFFLINE: contextvars.ContextVar[bool] = contextvars.ContextVar(
    "hop_top_kit_offline", default=False
)
"""The offline marker. A ContextVar rather than a module global so async
tasks and threads that copy the context inherit it, matching the Go port's
per-context tag."""


def set_offline(offline: bool) -> None:
    """Mark the current context offline. ``False`` leaves it clean."""
    _OFFLINE.set(bool(offline))


def is_offline() -> bool:
    """Report whether the current context carries the offline marker."""
    return _OFFLINE.get()


# Schemes that never touch the network. ``file:`` and ``data:`` resolve
# locally, so blocking them would break offline workflows rather than
# protect them.
_LOCAL_SCHEMES = frozenset({"file", "data"})


def _is_loopback(host: str | None) -> bool:
    """Report whether ``host`` names a loopback address.

    Hosts that are not literal IPs (DNS names) are treated as remote:
    resolving them would itself be network access.
    """
    if not host:
        return False
    # urlsplit on a synthetic authority strips the port and the IPv6
    # brackets, and lowercases the name — the same normalisation the real
    # URL parse already applied.
    hostname = urlsplit(f"//{host}").hostname
    if not hostname:
        return False
    if hostname == "localhost":
        return True
    try:
        return ipaddress.ip_address(hostname).is_loopback
    except ValueError:
        # Not a literal IP: a DNS name, therefore remote.
        return False


class _OfflineHandler(urllib.request.BaseHandler):
    """Refuses non-loopback requests while the offline marker is set.

    ``default_open`` runs before any protocol handler, so the request is
    stopped ahead of DNS resolution and socket creation. ``handler_order``
    sits below the default 500 to win that round against anything an
    adopter registered.
    """

    handler_order = 100

    def default_open(self, req: urllib.request.Request):
        if not is_offline():
            return None
        if req.type in _LOCAL_SCHEMES:
            return None
        if _is_loopback(req.host):
            return None
        raise OfflineError(f"{req.get_method()} {req.full_url}: network disabled by --offline")


def guard(opener: urllib.request.OpenerDirector | None) -> urllib.request.OpenerDirector:
    """Add the offline handler to ``opener``, returning it.

    ``None`` builds a default opener first. Guarding is idempotent:
    guarding an already guarded opener returns it unchanged.
    """
    if opener is None:
        opener = urllib.request.build_opener()
    for h in opener.handlers:
        if isinstance(h, _OfflineHandler):
            return opener
    opener.add_handler(_OfflineHandler())
    return opener


def install() -> None:
    """Guard the opener that module-level ``urllib.request.urlopen`` uses.

    That is the chokepoint beneath every caller that does not build its
    own opener — the common case across the port and adopter code — so the
    policy is enforced without a per-site change.

    Idempotent and safe to call more than once. Call it once during
    process start-up (``cli.create_app`` does this) and never concurrently
    with in-flight requests: it mutates a process-global.

    Callers that DO build their own opener must wrap it themselves with
    :func:`guard`; ``install`` cannot reach them.
    """
    urllib.request.install_opener(guard(urllib.request._opener))
