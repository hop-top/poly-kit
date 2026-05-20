"""Telemetry client — fire-and-forget envelope emission.

SDK shape:

  * Envelope schema (v1): schema_version, sdk_lang, sdk_version,
    installation_id, mode, occurred_at, event, attrs.
  * Default-denied: consent + mode are consulted lazily on each ``record()``
    so live consent flips take effect without restart.
  * Two sinks: ``https`` (NDJSON POST) and ``jsonl`` (size-rotated file).
  * Bounded queue + drop counter; ``record()`` returns in ~1ms.
  * Sync-caller-friendly: if no asyncio loop is running, a private daemon
    thread owns one; otherwise we reuse the caller's loop.

Divergence from Go's ``core/telemetry/event.go`` (intentional):

  Go's ``Event`` struct carries ``CommandPath`` because the in-tree consumers
  are all Cobra commands. SDK adopters aren't necessarily CLIs, so we model
  the envelope as a free-form ``event`` name plus an ``attrs`` dict. This is
  a known divergence — flagged in the cross-lang event schema doc
  (``hops/main/sdk/docs/telemetry-event-schema.md``).
"""

from __future__ import annotations

import contextlib
import json
import os
import queue
import threading
from collections.abc import Callable
from datetime import UTC, datetime
from typing import Any, Optional

from .consent import load_consent
from .install_id import get_install_id
from .mode import Mode, resolve_mode
from .redact import redact as _default_redact

# ---------------------------------------------------------------------------
# Module-level constants
# ---------------------------------------------------------------------------

_SCHEMA_VERSION = "1"
_SDK_LANG = "py"
_DEFAULT_QUEUE_SIZE = 1024
_DEFAULT_SHUTDOWN_S = 5.0
_JSONL_ROTATE_BYTES = 10 * 1024 * 1024  # 10 MB
_HTTPS_CONNECT_TIMEOUT_S = 5.0
_HTTPS_OVERALL_TIMEOUT_S = 10.0

# Sentinel pushed to the queue to terminate the drain task.
_SHUTDOWN = object()


def _discover_sdk_version() -> str:
    """Resolve ``hop-top-kit`` version via ``importlib.metadata``; fallback dev tag."""
    try:
        from importlib import metadata  # py>=3.8

        return metadata.version("hop-top-kit")
    except Exception:
        return "0.0.0-dev"


# ---------------------------------------------------------------------------
# Sink implementations
# ---------------------------------------------------------------------------


class _JSONLSink:
    """Append-to-file sink with size rotation at 10 MB.

    Rotation: when the live file exceeds ``_JSONL_ROTATE_BYTES``, it's renamed
    to ``<file>.1`` (clobbering any previous .1) and a fresh file is opened.
    Synchronous I/O; called only from the drain task / thread.
    """

    def __init__(self, path: str) -> None:
        self._path = path
        os.makedirs(os.path.dirname(path) or ".", exist_ok=True)

    def write(self, envelope: dict) -> None:
        line = json.dumps(envelope, separators=(",", ":"), sort_keys=True) + "\n"
        # Rotate before write if current size would exceed threshold.
        try:
            size = os.path.getsize(self._path) if os.path.exists(self._path) else 0
        except OSError:
            size = 0
        if size + len(line.encode("utf-8")) > _JSONL_ROTATE_BYTES:
            with contextlib.suppress(OSError):
                os.replace(self._path, self._path + ".1")
        with open(self._path, "a", encoding="utf-8") as fh:
            fh.write(line)

    def close(self) -> None:  # pragma: no cover — open/close happens per write
        pass


class _HTTPSSink:
    """NDJSON POST sink with one retry on 5xx / transport error.

    Endpoint is read from ``KIT_TELEMETRY_ENDPOINT`` env or constructor arg.
    If neither is provided, the sink is "unusable" — every emit increments
    the parent's drop counter rather than raising.
    """

    def __init__(self, endpoint: Optional[str]) -> None:
        self._endpoint = endpoint or os.environ.get("KIT_TELEMETRY_ENDPOINT", "").strip() or None

    @property
    def usable(self) -> bool:
        return self._endpoint is not None

    def post(self, envelopes: list[dict]) -> bool:
        """Return True on success (any 2xx), False on exhaustion."""
        if not self._endpoint or not envelopes:
            return False
        body = "\n".join(
            json.dumps(e, separators=(",", ":"), sort_keys=True) for e in envelopes
        ).encode("utf-8")
        headers = {"Content-Type": "application/x-ndjson"}

        # Lazy import: httpx is optional (declared under
        # ``optional-dependencies.telemetry-https`` in pyproject.toml).
        try:
            import httpx
        except ImportError:
            return False

        timeout = httpx.Timeout(_HTTPS_OVERALL_TIMEOUT_S, connect=_HTTPS_CONNECT_TIMEOUT_S)
        for attempt in (1, 2):
            try:
                with httpx.Client(timeout=timeout) as c:
                    r = c.post(self._endpoint, content=body, headers=headers)
                if 200 <= r.status_code < 300:
                    return True
                if r.status_code < 500:
                    return False  # 4xx: don't retry, drop
                # 5xx falls through to retry
            except Exception:
                pass
            if attempt == 1:
                continue
        return False

    def close(self) -> None:  # pragma: no cover
        pass


# ---------------------------------------------------------------------------
# Client
# ---------------------------------------------------------------------------


class Client:
    """Fire-and-forget telemetry client.

    See module docstring for the sync-vs-async caller duality + the Go
    ``CommandPath`` divergence.
    """

    def __init__(
        self,
        *,
        endpoint: Optional[str] = None,
        sink: Optional[str] = None,
        sink_file: Optional[str] = None,
        queue_size: int = _DEFAULT_QUEUE_SIZE,
        redactor: Optional[Callable[[dict], dict]] = None,
        sdk_version: Optional[str] = None,
    ) -> None:
        # Env-default the knobs first; explicit args win.
        env = os.environ
        self._sink_name = (sink or env.get("KIT_TELEMETRY_SINK", "jsonl")).strip().lower()
        self._sink_file = sink_file or env.get(
            "KIT_TELEMETRY_SINK_FILE", os.path.join(os.path.expanduser("~"), ".kit-telemetry.jsonl")
        )
        try:
            qs = int(env.get("KIT_TELEMETRY_QUEUE_SIZE", str(queue_size)))
        except ValueError:
            qs = queue_size
        self._queue_size = max(1, qs)

        self._redactor = redactor
        self._sdk_version = sdk_version

        # Build sink (lazy; HTTPS may be unusable without endpoint).
        if self._sink_name == "https":
            self._sink: object = _HTTPSSink(endpoint)
        else:
            self._sink = _JSONLSink(self._sink_file)

        # Background machinery: a bounded sync queue is the source of truth;
        # the drain thread takes from it and dispatches to the sink. We
        # deliberately use threading.Queue (not asyncio.Queue) because record()
        # MUST be sync-callable from any context.
        self._q: queue.Queue = queue.Queue(maxsize=self._queue_size)
        self._dropped = 0
        self._dropped_lock = threading.Lock()
        self._thread: Optional[threading.Thread] = None
        self._thread_lock = threading.Lock()
        self._stop_event = threading.Event()

    # -- public surface ------------------------------------------------------

    def record(self, event: str, attrs: Optional[dict] = None) -> None:
        """Enqueue an event. Returns immediately. Never raises."""
        # Lazy re-check of consent + mode so live env / file flips are honored.
        try:
            mode = resolve_mode()
            if mode is Mode.OFF:
                return
            consent = load_consent()
            if not consent.allowed:
                return

            envelope = self._build_envelope(event, attrs or {}, mode)

            # Custom redactor first, default last (per docstring contract).
            # A broken caller redactor must not break emission; fall back to
            # the default-only path on any exception it raises.
            if self._redactor is not None:
                with contextlib.suppress(Exception):
                    envelope = self._redactor(envelope)
            envelope = _default_redact(envelope)

            try:
                self._q.put_nowait(envelope)
            except queue.Full:
                with self._dropped_lock:
                    self._dropped += 1
                return

            self._ensure_drain_thread()
        except Exception:
            # Never let telemetry crash the caller.
            with self._dropped_lock:
                self._dropped += 1

    @property
    def dropped_count(self) -> int:
        with self._dropped_lock:
            return self._dropped

    def shutdown(self, timeout: float = _DEFAULT_SHUTDOWN_S) -> None:
        """Best-effort flush + drain thread join. Idempotent."""
        with self._thread_lock:
            t = self._thread
            if t is None:
                return
            # Signal end-of-stream.
            try:
                self._q.put(_SHUTDOWN, timeout=0.1)
            except queue.Full:
                # Force a slot: drop one queued envelope so SHUTDOWN can land.
                try:
                    self._q.get_nowait()
                    self._q.task_done()
                    with self._dropped_lock:
                        self._dropped += 1
                    self._q.put_nowait(_SHUTDOWN)
                except queue.Empty:
                    pass
            self._stop_event.set()
        if t is not None:
            t.join(timeout=timeout)

    # -- internals -----------------------------------------------------------

    def _build_envelope(self, event: str, attrs: dict, mode: Mode) -> dict:
        # Anon-tier defensive strip ("Anon vs Full payload boundary"):
        # drop free-form attrs when mode == anon. Matches the
        # rs SDK's ``Value::Null`` shape — key stays for envelope-shape
        # stability, payload is JSON null. The default redactor runs
        # later but can't be relied on to do this (a caller redactor
        # could reintroduce PII in attrs).
        envelope_attrs: Any = None if mode is Mode.ANON else dict(attrs)
        return {
            "schema_version": _SCHEMA_VERSION,
            "sdk_lang": _SDK_LANG,
            "sdk_version": self._sdk_version or _discover_sdk_version(),
            "installation_id": _safe_install_id(),
            "mode": mode.value,
            "occurred_at": datetime.now(UTC).isoformat(),
            "event": event,
            "attrs": envelope_attrs,
        }

    def _ensure_drain_thread(self) -> None:
        if self._thread is not None and self._thread.is_alive():
            return
        with self._thread_lock:
            if self._thread is not None and self._thread.is_alive():
                return
            t = threading.Thread(
                target=self._drain_loop,
                name="hop-top-kit-telemetry-drain",
                daemon=True,
            )
            t.start()
            self._thread = t

    def _drain_loop(self) -> None:
        """Background loop: pull envelopes, dispatch to sink, until SHUTDOWN."""
        while True:
            try:
                item = self._q.get(timeout=0.5)
            except queue.Empty:
                if self._stop_event.is_set():
                    return
                continue
            if item is _SHUTDOWN:
                self._q.task_done()
                return
            try:
                self._dispatch(item)
            except Exception:
                with self._dropped_lock:
                    self._dropped += 1
            finally:
                self._q.task_done()

    def _dispatch(self, envelope: dict) -> None:
        if isinstance(self._sink, _JSONLSink):
            self._sink.write(envelope)
            return
        if isinstance(self._sink, _HTTPSSink):
            if not self._sink.usable:
                with self._dropped_lock:
                    self._dropped += 1
                return
            ok = self._sink.post([envelope])
            if not ok:
                with self._dropped_lock:
                    self._dropped += 1


def _safe_install_id() -> str:
    """Best-effort install-id read; failure → empty string (never propagates)."""
    try:
        return get_install_id()
    except Exception:
        return ""


__all__ = ("Client",)
