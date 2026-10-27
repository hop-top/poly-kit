"""
hop_top_kit.llm_http — the guarded JSON-over-HTTP transport the
hand-rolled provider adapters share (stdlib ``urllib``, no SDK).

What it enforces, for every adapter:

- **Redirects are not followed**: a credential never moves to a host
  the server names; a 3xx is an :class:`~hop_top_kit.llm.HTTPStatusError`.
- **Retries** as the Anthropic and OpenAI SDKs default: up to two more
  attempts on 408, 409, 429, 5xx and transport failures,
  ``x-should-retry`` overriding the status. The wait is
  ``retry-after-ms`` / ``retry-after`` (seconds) only when it lies in
  (0, 60s]; any other value (zero, negative, NaN, huge, an HTTP date)
  falls back to backoff (0.5s base, 8s cap, full jitter), so a hostile
  or broken server cannot stall the client.
- **Size limits**: a whole body past ``max_body_bytes`` (32 MiB) and one
  SSE event past ``max_event_bytes`` (8 MiB) end the read with
  :class:`~hop_top_kit.llm.ResponseTooLargeError`, never retried.
- **Scrubbing**: every credential value sent is replaced by
  ``REDACTED`` in error text; transport errors name the destination
  without userinfo or query.
- **Offline**: the opener carries :func:`hop_top_kit.netpolicy.guard`;
  an offline refusal is raised as is, never retried.

Pinned by ``contracts/llm-v1`` (``anthropic_error``, ``anthropic_limits``).
"""

from __future__ import annotations

import http.client
import json
import math
import random
import time
import urllib.error
import urllib.request
from collections import deque
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import Any

from hop_top_kit import llm, netpolicy

__all__ = [
    "DEFAULT_MAX_BODY_BYTES",
    "DEFAULT_MAX_EVENT_BYTES",
    "DEFAULT_MAX_RETRIES",
    "Limits",
    "SSEDecoder",
    "SSEEvent",
    "Transport",
    "retry_after",
]

DEFAULT_MAX_BODY_BYTES = 32 << 20
DEFAULT_MAX_EVENT_BYTES = 8 << 20
# Retries after the first attempt, as the provider SDKs default.
DEFAULT_MAX_RETRIES = 2
# The longest wait a server may ask for, as the Anthropic SDKs cap it.
MAX_RETRY_AFTER = 60.0
_BACKOFF_BASE = 0.5
_BACKOFF_CAP = 8.0
_MESSAGE_CAP = 512
_CHUNK = 64 << 10


@dataclass(frozen=True)
class Limits:
    """Bounds on what a response may hold. Provider params
    ``max_body_bytes`` and ``max_event_bytes`` set them."""

    max_body_bytes: int = DEFAULT_MAX_BODY_BYTES
    max_event_bytes: int = DEFAULT_MAX_EVENT_BYTES

    @classmethod
    def from_params(cls, provider: str, params: Mapping[str, str]) -> Limits:
        vals = {
            "max_body_bytes": DEFAULT_MAX_BODY_BYTES,
            "max_event_bytes": DEFAULT_MAX_EVENT_BYTES,
        }
        for name in vals:
            if name not in params:
                continue
            try:
                n = int(params[name].strip())
            except ValueError:
                n = 0
            if n <= 0:
                raise llm.LLMError(f"{provider}: {name} must be a positive byte count")
            vals[name] = n
        return cls(**vals)


def retry_after(headers: Mapping[str, str] | Any) -> float | None:
    """``retry-after-ms``, else ``retry-after`` in seconds, when it asks
    for a wait in (0, 60s]; ``None`` otherwise (the caller backs off)."""

    def secs(name: str, per_sec: float) -> float | None:
        raw = headers.get(name)
        if raw is None:
            return None
        try:
            v = float(str(raw).strip()) / per_sec
        except ValueError:
            return None
        return v if 0.0 < v <= MAX_RETRY_AFTER and math.isfinite(v) else None

    ms = secs("retry-after-ms", 1000.0)
    return ms if ms is not None else secs("retry-after", 1.0)


def _should_retry(status: int, headers: Any) -> bool:
    v = headers.get("x-should-retry")
    if v == "true":
        return True
    if v == "false":
        return False
    return status in (408, 409, 429) or status >= 500


def _cap(s: str) -> str:
    return s if len(s) <= _MESSAGE_CAP else s[:_MESSAGE_CAP] + "..."


def error_body(text: str) -> tuple[str, str]:
    """``(type, message)`` of ``{"error":{"type":..,"message":..}}``, or
    ``("", text)``."""
    try:
        v = json.loads(text)
    except ValueError:
        v = None
    if isinstance(v, dict) and isinstance(v.get("error"), dict):
        e = v["error"]
        return str(e.get("type") or ""), _cap(str(e.get("message") or ""))
    return "", _cap(text.strip())


def classify(
    provider: str,
    status: int,
    after: float | None,
    error_type: str,
    message: str,
    model: str,
) -> llm.LLMError:
    """An error response in the llm taxonomy, as Go's ``mapError``."""
    if status in (401, 403):
        return llm.AuthError(provider, f"{error_type}: {message}" if error_type else message)
    if 300 <= status <= 399:
        return llm.HTTPStatusError(status, f"{provider}: redirect not followed")
    if status == 429:
        return llm.RateLimitError(provider, after or 0.0)
    if status == 404:
        return llm.ModelError(model, provider)
    if status >= 500:
        kind = f"{error_type}: " if error_type else ""
        return llm.HTTPStatusError(status, f"{provider}: {kind}{message}")
    return llm.APIError(provider, status, error_type, message)


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    """Stops at a redirect: the 3xx surfaces as an HTTPError."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def _destination(url: str) -> str:
    return netpolicy._destination(url)


Sleep = Callable[[float], None]


class Transport:
    """POSTs JSON to one URL with fixed headers, under the rules in the
    module docs. *secrets* are the credential values sent, scrubbed from
    every error."""

    def __init__(
        self,
        *,
        provider: str,
        url: str,
        headers: Mapping[str, str],
        secrets: list[str],
        limits: Limits,
        max_retries: int = DEFAULT_MAX_RETRIES,
        sleep: Sleep | None = time.sleep,
        timeout: float = 600.0,
        opener: urllib.request.OpenerDirector | None = None,
    ) -> None:
        self.provider = provider
        self.url = url
        self.destination = _destination(url)
        self.headers = dict(headers)
        self.secrets = [s for s in secrets if s.strip()]
        self.limits = limits
        self.max_retries = max_retries
        self.sleep = sleep
        self.timeout = timeout
        self._opener = netpolicy.guard(opener or urllib.request.build_opener(_NoRedirect()))

    def scrub(self, text: str) -> str:
        for s in self.secrets:
            text = text.replace(s, "REDACTED")
        return text

    def transport_error(self, exc: BaseException) -> llm.TransportError:
        reason = getattr(exc, "reason", None) or exc
        return llm.TransportError(self.scrub(f"{self.provider}: POST {self.destination}: {reason}"))

    def post(
        self,
        body: dict[str, Any],
        *,
        stream: bool,
        model: str,
    ) -> http.client.HTTPResponse:
        """POST *body*, retrying; return a 2xx response with its body
        unread. Raises the mapped error of the last attempt."""
        data = json.dumps(body).encode("utf-8")
        headers = {"content-type": "application/json", **self.headers}
        if stream:
            headers["accept"] = "text/event-stream"
        attempt = 0
        while True:
            req = urllib.request.Request(self.url, data=data, headers=headers, method="POST")
            try:
                resp = self._opener.open(req, timeout=self.timeout)
                return resp
            except netpolicy.OfflineError:
                raise
            except urllib.error.HTTPError as e:
                status = e.code
                retry = _should_retry(status, e.headers)
                after = retry_after(e.headers)
                try:
                    raw = self._read_capped(e, self.limits.max_body_bytes)
                except llm.ResponseTooLargeError:
                    raw = b""
                finally:
                    e.close()
                text = self.scrub(raw.decode("utf-8", "replace"))
                etype, msg = error_body(text)
                err: llm.LLMError = classify(self.provider, status, after, etype, msg, model)
            except (OSError, http.client.HTTPException) as e:
                err, retry, after = self.transport_error(e), True, None
            if not retry or attempt >= self.max_retries or self.sleep is None:
                raise err
            backoff = min(_BACKOFF_CAP, _BACKOFF_BASE * (2**attempt))
            self.sleep(after if after is not None else random.uniform(0, backoff))
            attempt += 1

    def _read_capped(self, resp: Any, limit: int) -> bytes:
        out = bytearray()
        while True:
            chunk = _read_some(resp)
            if not chunk:
                return bytes(out)
            if len(out) + len(chunk) > limit:
                raise llm.ResponseTooLargeError(self.provider, "body", limit)
            out += chunk

    def read_body(self, resp: http.client.HTTPResponse) -> bytes:
        """*resp*'s body, refused past ``max_body_bytes``; closes *resp*."""
        try:
            return self._read_capped(resp, self.limits.max_body_bytes)
        except (OSError, http.client.HTTPException) as e:
            raise self.transport_error(e) from None
        finally:
            resp.close()

    def read_json(self, resp: http.client.HTTPResponse) -> Any:
        raw = self.read_body(resp)
        try:
            return json.loads(raw)
        except ValueError as e:
            raise llm.LLMError(f"{self.provider}: decode response: {e}") from None


def _read_some(resp: Any) -> bytes:
    """Whatever bytes are available, without waiting to fill a buffer."""
    read1 = getattr(resp, "read1", None)
    return read1(_CHUNK) if read1 is not None else resp.read(_CHUNK)


# ---------------------------------------------------------------------------
# Server-sent events
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class SSEEvent:
    event: str
    data: str


class SSEDecoder:
    """Incremental ``text/event-stream`` decoding, line for line what the
    Go SDKs' decoder does: lines end at ``\\n`` (a trailing ``\\r``
    dropped), a blank line dispatches, ``field: value`` takes one
    optional space, ``:`` lines are comments, ``data`` lines join with
    ``\\n``. A body ending mid-event drops that event. One event (bytes
    since the last blank line, ``\\r`` aside) past *max_event* raises
    :class:`~hop_top_kit.llm.ResponseTooLargeError`."""

    def __init__(self, provider: str, max_event: int = DEFAULT_MAX_EVENT_BYTES) -> None:
        self.provider = provider
        self.max_event = max_event
        self._event_bytes = 0
        self._prev = 0
        self._line = bytearray()
        self._event = ""
        self._data: list[str] = []
        self._has_data = False
        self._ready: deque[SSEEvent] = deque()

    def feed(self, chunk: bytes) -> None:
        for b in chunk:
            if b != 0x0D:
                if b == 0x0A and self._prev == 0x0A:
                    self._event_bytes = 0
                else:
                    self._event_bytes += 1
                self._prev = b
                if self._event_bytes > self.max_event:
                    raise llm.ResponseTooLargeError(self.provider, "event", self.max_event)
            if b != 0x0A:
                self._line.append(b)
                continue
            if self._line.endswith(b"\r"):
                del self._line[-1]
            line = self._line.decode("utf-8", "replace")
            self._line = bytearray()
            self._line_done(line)

    def pop(self) -> SSEEvent | None:
        return self._ready.popleft() if self._ready else None

    def _line_done(self, line: str) -> None:
        if not line:
            if not self._event and not self._has_data:
                return
            self._ready.append(SSEEvent(self._event, "".join(self._data)))
            self._event, self._data, self._has_data = "", [], False
            return
        name, _, value = line.partition(":")
        if value.startswith(" "):
            value = value[1:]
        if name == "event":
            self._event = value
        elif name == "data":
            self._data.append(value + "\n")
            self._has_data = True


class SSEReader:
    """Reads one streamed response as events, transport errors mapped."""

    def __init__(self, transport: Transport, resp: http.client.HTTPResponse) -> None:
        self._t = transport
        self._resp = resp
        self._dec = SSEDecoder(transport.provider, transport.limits.max_event_bytes)
        self.closed = False

    def next_event(self) -> SSEEvent | None:
        """The next event; ``None`` when the body ended."""
        while True:
            ev = self._dec.pop()
            if ev is not None:
                return ev
            if self.closed:
                return None
            try:
                chunk = _read_some(self._resp)
            except (OSError, http.client.HTTPException) as e:
                self.close()
                raise self._t.transport_error(e) from None
            if not chunk:
                self.close()
                return None
            try:
                self._dec.feed(chunk)
            except llm.ResponseTooLargeError:
                self.close()
                raise

    def close(self) -> None:
        if not self.closed:
            self.closed = True
            self._resp.close()
