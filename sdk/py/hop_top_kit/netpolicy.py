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

Marker scope
------------
The marker has two forms, matching the other ports:

- :func:`set_offline` marks the whole process. It is a plain module-level
  flag, so every thread sees it: ``threading.Thread``,
  ``ThreadPoolExecutor``, ``loop.run_in_executor``, asyncio tasks and
  ``asyncio.to_thread`` alike. This is what ``create_app``'s root callback
  sets for ``--offline``. It mirrors TypeScript's ``setProcessOffline`` and
  PHP's ``NetPolicy::setOffline``, and the process-global reach of Go's
  ``netpolicy.Install``.
- :func:`offline_scope` marks one context: the ``with`` block and the work
  that copies its context (asyncio tasks, ``asyncio.to_thread``,
  ``contextvars.copy_context().run``), but not threads that start from an
  empty context. It mirrors Go's ``WithOffline`` and TypeScript's
  ``withOffline``, for hosts that need one unit of work offline while the
  rest of the process stays online.

:func:`is_offline` is true when either applies. A scope can only add the
marker, never lift the process-wide one.

Scope
-----
:func:`guard` sits in the ``urllib.request`` opener chain, so it covers
HTTP and HTTPS through ``urllib`` — which is every network client in the
Python port today (``hop_top_kit.aim``, ``hop_top_kit.upgrade``,
``hop_top_kit.llm.URLSource``). :func:`install` guards both the opener
module-level ``urlopen`` uses and every opener built afterwards through
``urllib.request.build_opener`` — which includes the one-off opener
``urlopen(..., context=...)`` builds per call. It does NOT cover code
that opens a socket directly: raw ``socket``, ``httpx`` (the optional
telemetry HTTPS sink), ``grpc`` (``routellm_grpc``),
``http.client`` used without urllib, or DB-API drivers. For those,
``--offline`` remains advisory and the call site must consult
:func:`is_offline` itself. Closing that gap needs a wrapped socket factory
threaded through each such client; it is deliberately not attempted here.

This mirrors ``go/core/netpolicy`` — same five guarantees, expressed
against urllib's opener chain instead of ``http.RoundTripper``.
"""

from __future__ import annotations

import contextlib
import contextvars
import ipaddress
import urllib.request
from collections.abc import Iterator
from urllib.parse import urlsplit

__all__ = [
    "OfflineError",
    "guard",
    "install",
    "is_offline",
    "offline_scope",
    "set_offline",
]


class OfflineError(OSError):
    """Raised by the guard when a request is attempted while offline.

    Subclasses ``OSError`` so it lands in the same ``except`` clauses
    urllib callers already write for transport failures — an error outside
    that hierarchy would escape their handling and surface as a crash.
    """


_process_offline = False
"""The process-wide marker set by :func:`set_offline`.

A module global, not a ``ContextVar``: ``threading.Thread``,
``ThreadPoolExecutor`` and ``loop.run_in_executor`` start their work from
an empty context, so a context-scoped marker set by the CLI would never
reach them and their requests would go out under ``--offline``. A single
reference store is atomic, so no lock is needed to publish it."""

_scoped_offline: contextvars.ContextVar[bool] = contextvars.ContextVar(
    "hop_top_kit_offline_scope", default=False
)
"""The per-context marker set by :func:`offline_scope`."""


def set_offline(offline: bool) -> None:
    """Mark the whole process offline, or clear that mark with ``False``.

    Process-wide: every thread, executor and task sees it, whichever one
    called this. ``create_app`` calls it on every dispatch with the
    resolved ``--offline`` value. ``False`` clears only this process-wide
    mark; an enclosing :func:`offline_scope` stays in force.
    """
    global _process_offline
    _process_offline = bool(offline)


@contextlib.contextmanager
def offline_scope(offline: bool = True) -> Iterator[None]:
    """Mark the current context offline for the duration of the block.

    Work that copies the context inherits the mark: asyncio tasks created
    inside the block, ``asyncio.to_thread`` and
    ``contextvars.copy_context().run``. Threads that start from an empty
    context — ``threading.Thread``, ``ThreadPoolExecutor.submit``,
    ``loop.run_in_executor`` — do not; use :func:`set_offline` when the
    whole process must stay off the network.

    ``offline=False`` leaves the context unchanged, so it neither marks a
    clean context nor lifts an enclosing mark. Mirrors Go's
    ``WithOffline``.
    """
    if not offline:
        yield
        return
    token = _scoped_offline.set(True)
    try:
        yield
    finally:
        _scoped_offline.reset(token)


def is_offline() -> bool:
    """Report whether the process, or the current context, is offline."""
    return _process_offline or _scoped_offline.get()


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


def _destination(url: str) -> str:
    """Render ``url`` as scheme, host and path only.

    Enough to say where a refused request was going, nothing it carried:
    query, fragment and userinfo are dropped because they may hold
    credentials (an API key param, basic-auth userinfo). The port stays.
    An opaque URL (``mailto:``) keeps its scheme alone, and a target that
    does not parse cannot be stripped reliably, so none of it is echoed.
    Mirrors Go's ``netpolicy`` refusal.
    """
    try:
        parts = urlsplit(url)
    except ValueError:
        return "<unparseable URL>"
    host = parts.netloc.rpartition("@")[2]
    if not host and not parts.path.startswith("/"):
        return f"{parts.scheme}:"
    return f"{parts.scheme}://{host}{parts.path}"


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
        raise OfflineError(
            f"{req.get_method()} {_destination(req.full_url)}: network disabled by --offline"
        )


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


def _guarded_build_opener(build_opener):
    """Wrap ``build_opener`` so every opener it returns carries the guard."""

    def build(*handlers):
        return guard(build_opener(*handlers))

    build._hop_top_kit_unguarded = build_opener  # type: ignore[attr-defined]
    build.__doc__ = build_opener.__doc__
    build.__name__ = build_opener.__name__
    return build


def install() -> None:
    """Guard the openers module-level ``urllib.request.urlopen`` uses.

    Two seams, because ``urlopen`` has two ways to get an opener:

    - The installed opener, used by a plain ``urlopen(url)``. It is wrapped
      with :func:`guard` and reinstalled.
    - ``urllib.request.build_opener``, which ``urlopen(..., context=...)``
      calls to build a one-off opener per request, bypassing the installed
      one. It is replaced with a wrapper that guards what it returns, so
      passing an SSL context is not a way around ``--offline``. Callers
      that build their own opener through it after ``install`` are covered
      by the same wrapper.

    That is the chokepoint beneath every caller that does not assemble its
    own ``OpenerDirector``, so the policy is enforced without a per-site
    change.

    Idempotent and safe to call more than once. Call it once during
    process start-up and never concurrently with in-flight requests: it
    mutates process-globals. Apps built with ``cli.create_app`` need not
    call it: the root callback does, on dispatch, when ``--offline`` is
    set.

    Not reachable from here: an ``OpenerDirector`` assembled by hand, and a
    ``build_opener`` bound by name (``from urllib.request import
    build_opener``) before ``install`` ran. Wrap those with :func:`guard`.
    """
    if not hasattr(urllib.request.build_opener, "_hop_top_kit_unguarded"):
        urllib.request.build_opener = _guarded_build_opener(urllib.request.build_opener)
    urllib.request.install_opener(guard(urllib.request._opener))
