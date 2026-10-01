"""``--offline`` holds process-wide: every thread, executor and task.

Python's concurrency primitives disagree about context propagation.
asyncio tasks and ``asyncio.to_thread`` copy the caller's
``contextvars`` context; ``threading.Thread``, ``ThreadPoolExecutor.submit``
and ``loop.run_in_executor`` start from an empty one. A marker that lived
only in a ``ContextVar`` was therefore invisible to the second group, and
their requests reached the wire under ``--offline``. These tests pin the
marker to the process so no worker can escape it.

No test here touches the network: requests land on a recording handler
installed under the guard, so a regression shows up as "reached", never as
a real connection.
"""

from __future__ import annotations

import asyncio
import contextvars
import threading
import urllib.request
from collections.abc import Callable
from concurrent.futures import ThreadPoolExecutor

import pytest
from typer.testing import CliRunner

from hop_top_kit import netpolicy
from hop_top_kit.cli import create_app

from .test_netpolicy import _Recorder

_SECRET_URL = "https://alice:pw-secret@example.invalid/v1/thing?key=q-secret#frag"
_WANT = "GET https://example.invalid/v1/thing: network disabled by --offline"
_LOOPBACK = "http://127.0.0.1:1/health"


@pytest.fixture
def rec() -> _Recorder:
    """Install a guarded opener whose only transport is a recorder.

    Module-level ``urlopen`` then routes through the guard exactly as it
    does after ``install()``, but nothing can open a socket.
    """
    r = _Recorder()
    urllib.request.install_opener(netpolicy.guard(urllib.request.build_opener(r)))
    return r


def _fetch(url: str) -> BaseException | None:
    """Request ``url`` through module-level ``urlopen``; return what it raised."""
    try:
        urllib.request.urlopen(url, timeout=1)
    except BaseException as e:
        return e
    return None


def _in_thread(url: str) -> BaseException | None:
    out: list[BaseException | None] = []
    t = threading.Thread(target=lambda: out.append(_fetch(url)))
    t.start()
    t.join()
    return out[0]


def _in_pool(url: str) -> BaseException | None:
    with ThreadPoolExecutor(max_workers=1) as pool:
        return pool.submit(_fetch, url).result()


def _in_run_in_executor(url: str) -> BaseException | None:
    async def main() -> BaseException | None:
        return await asyncio.get_running_loop().run_in_executor(None, _fetch, url)

    return asyncio.run(main())


def _in_task(url: str) -> BaseException | None:
    async def fetch() -> BaseException | None:
        return _fetch(url)

    async def main() -> BaseException | None:
        return await asyncio.create_task(fetch())

    return asyncio.run(main())


def _in_to_thread(url: str) -> BaseException | None:
    return asyncio.run(asyncio.to_thread(_fetch, url))


def _in_empty_context(url: str) -> BaseException | None:
    """A context that never saw the caller's: the worst case for a ContextVar."""
    return contextvars.Context().run(_fetch, url)


_WORKERS: dict[str, Callable[[str], BaseException | None]] = {
    "thread": _in_thread,
    "thread_pool_executor": _in_pool,
    "run_in_executor": _in_run_in_executor,
    "asyncio_task": _in_task,
    "to_thread": _in_to_thread,
    "empty_context": _in_empty_context,
}


@pytest.mark.parametrize("worker", _WORKERS.values(), ids=_WORKERS.keys())
def test_offline_refuses_requests_from_every_worker(rec: _Recorder, worker):
    """A request from any thread, executor or task is refused under offline.

    The refusal has the shared shape and carries no query, fragment or
    userinfo, whichever thread raised it.
    """
    netpolicy.set_offline(True)

    err = worker(_SECRET_URL)

    assert isinstance(err, netpolicy.OfflineError), f"request escaped --offline: {err!r}"
    assert str(err) == _WANT
    assert not rec.reached, "request reached the transport despite --offline"


@pytest.mark.parametrize("worker", _WORKERS.values(), ids=_WORKERS.keys())
def test_offline_still_allows_loopback_from_every_worker(rec: _Recorder, worker):
    """Loopback stays reachable from workers too: no network, not no self."""
    netpolicy.set_offline(True)

    err = worker(_LOOPBACK)

    assert err is None, f"loopback refused from a worker: {err!r}"
    assert rec.reached


@pytest.mark.parametrize("worker", _WORKERS.values(), ids=_WORKERS.keys())
def test_not_offline_refuses_nothing_from_any_worker(rec: _Recorder, worker):
    """An unmarked process is untouched, whichever worker sends."""
    netpolicy.set_offline(False)

    err = worker(_SECRET_URL)

    assert err is None, f"request refused without --offline: {err!r}"
    assert rec.reached


def test_marker_set_in_a_worker_holds_for_the_process():
    """The marker is the process's, not the setter's thread's."""
    t = threading.Thread(target=netpolicy.set_offline, args=(True,))
    t.start()
    t.join()
    assert netpolicy.is_offline() is True

    t = threading.Thread(target=netpolicy.set_offline, args=(False,))
    t.start()
    t.join()
    assert netpolicy.is_offline() is False


def test_cli_offline_reaches_worker_threads(rec: _Recorder):
    """``prog --offline cmd`` refuses a leaf's pool requests, not just its own.

    The leaf is naive: it never consults the marker, and fans its request
    out to a thread pool the way a batch downloader would.
    """
    app, _ = create_app(name="probe", version="0.0.0", help="probe")
    seen: dict[str, BaseException | None] = {}

    @app.command()
    def fetch() -> None:
        seen["err"] = _in_pool("https://example.invalid/x?key=q-secret")

    result = CliRunner().invoke(app, ["--offline", "fetch"])

    assert result.exit_code == 0, result.output
    assert isinstance(seen["err"], netpolicy.OfflineError), (
        f"pool request escaped --offline: {seen['err']!r}"
    )
    assert str(seen["err"]) == "GET https://example.invalid/x: network disabled by --offline"
    assert not rec.reached


def test_offline_scope_marks_only_its_own_context(rec: _Recorder):
    """``offline_scope`` is the per-context form: it marks the block and the
    work that copies its context (tasks, ``to_thread``), not the process.
    """
    with netpolicy.offline_scope():
        assert netpolicy.is_offline() is True
        assert isinstance(_in_to_thread(_SECRET_URL), netpolicy.OfflineError)
        assert isinstance(_in_task(_SECRET_URL), netpolicy.OfflineError)
        # A bare thread starts from an empty context: the scope does not
        # reach it. That is the scope's contract; process-wide refusal is
        # what set_offline is for.
        assert _in_thread(_SECRET_URL) is None

    assert netpolicy.is_offline() is False
    assert _fetch(_SECRET_URL) is None


def test_offline_scope_false_leaves_the_context_unchanged():
    """``offline_scope(False)`` neither marks a clean context nor clears a
    marked one, mirroring Go's ``WithOffline(ctx, false)``.
    """
    with netpolicy.offline_scope(False):
        assert netpolicy.is_offline() is False

    with netpolicy.offline_scope(), netpolicy.offline_scope(False):
        assert netpolicy.is_offline() is True


def test_offline_scope_cannot_lift_the_process_marker(rec: _Recorder):
    """Leaving a scope must not clear process-wide offline."""
    netpolicy.set_offline(True)
    with netpolicy.offline_scope():
        pass
    assert netpolicy.is_offline() is True
    assert isinstance(_in_thread(_SECRET_URL), netpolicy.OfflineError)


def test_offline_scope_restores_on_error():
    """An exception out of the block still unmarks the context."""
    with pytest.raises(RuntimeError), netpolicy.offline_scope():
        raise RuntimeError("boom")
    assert netpolicy.is_offline() is False
