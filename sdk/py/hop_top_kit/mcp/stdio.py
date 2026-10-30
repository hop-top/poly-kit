"""The stdio binding for the dual-spec MCP surface.

Newline-delimited JSON-RPC over a pair of streams (by default the
process's stdin/stdout), driving the same era dispatch the ASGI binding
uses.

Wire rules (stdio binding, 2024-11-05 and 2026-07-28):

- one JSON-RPC message per line; output lines carry no embedded newline,
  and the output stream carries nothing but protocol messages —
  diagnostics go to the log stream (stderr);
- a notification (no ``id`` member) is never answered;
- each message is a single request: a JSON array (a batch) is refused
  with ``-32600`` and ``id: null``;
- a line that is not JSON is answered ``-32700`` with ``id: null``;
- requests run concurrently, so replies may arrive out of order — each
  carries its request's id.

stdio has no header layer: era detection uses the body markers only
(``params._meta`` protocolVersion, ``server/discover``), and the
2026-07-28 header checks (V4, V6, V7) do not apply. Gates that read
headers see none: the verifier is called with a header-less request and
the ``X-Confirm-Token`` gate cannot be satisfied.

The surface is synchronous, so concurrency is threads: one reader thread
frames input, and each request runs on a worker from a
:class:`~concurrent.futures.ThreadPoolExecutor` (default size). Writes
to the output stream are serialised, one whole line at a time.
"""

from __future__ import annotations

import io
import json
import sys
import threading
from collections.abc import Callable, Mapping
from concurrent.futures import ThreadPoolExecutor
from typing import IO, Any

from ._json import Raw
from .bridge import Bridge
from .dispatch import McpSurface, _go_json_error
from .modern import TRANSPORT_STDIO
from .protocol import (
    ERR_INTERNAL,
    ERR_INVALID_REQUEST,
    ERR_PARSE,
    Headers,
    Request,
    Response,
    RPCRequest,
    parse_rpc,
    write_error,
)

#: What a dispatched message is handed to.
_Serve = Callable[[Request, RPCRequest], Response]

_NULL_ID = Raw("null")


class StdioServer:
    """A running stdio session.

    :meth:`wait` returns once input has ended (or :meth:`close` was
    called) and every in-flight request has been answered.
    """

    def __init__(self, serve: _Serve, input: IO[Any], output: IO[Any], log: IO[Any]) -> None:
        self._serve = serve
        self._input = input
        self._output = output
        self._log = log
        self._out_binary = _is_binary(output)
        self._write_lock = threading.Lock()
        self._state = threading.Condition()
        self._in_flight = 0
        self._ended = False
        self._done = threading.Event()
        self._pool = ThreadPoolExecutor(thread_name_prefix="mcp-stdio")
        self._reader = threading.Thread(target=self._read, name="mcp-stdio-reader", daemon=True)
        self._reader.start()

    @property
    def done(self) -> bool:
        """Whether the session has finished."""
        return self._done.is_set()

    def wait(self, timeout: float | None = None) -> bool:
        """Block until the session finishes; ``False`` if ``timeout`` ran out."""
        return self._done.wait(timeout)

    def close(self, timeout: float | None = None) -> bool:
        """Stop reading input, then wait like :meth:`wait`.

        A line the reader is already blocked on is discarded when it
        arrives; requests already in flight are still answered.
        """
        self._end()
        return self.wait(timeout)

    # --- session state ----------------------------------------------------

    def _end(self) -> None:
        with self._state:
            self._ended = True
            self._maybe_finish()

    def _maybe_finish(self) -> None:
        # Caller holds self._state.
        if self._ended and self._in_flight == 0 and not self._done.is_set():
            self._pool.shutdown(wait=False)
            self._done.set()

    def _submit(self, rpc: RPCRequest, body: bytes) -> None:
        with self._state:
            if self._ended:
                return
            self._in_flight += 1
        self._pool.submit(self._run, rpc, body)

    def _run(self, rpc: RPCRequest, body: bytes) -> None:
        try:
            self._dispatch(rpc, body)
        finally:
            with self._state:
                self._in_flight -= 1
                self._maybe_finish()

    # --- input ------------------------------------------------------------

    def _read(self) -> None:
        try:
            while True:
                try:
                    line = self._input.readline()
                except (OSError, ValueError) as exc:
                    # A broken stream ends the session instead of crashing.
                    self._warn(f"input error: {exc}")
                    return
                if not line:
                    return
                with self._state:
                    if self._ended:
                        return
                if isinstance(line, bytes):
                    line = line.decode("utf-8", errors="replace")
                self._handle_line(line)
        finally:
            self._end()

    def _handle_line(self, line: str) -> None:
        """Frame, parse and dispatch (or answer) one input line."""
        text = line.strip()
        if not text:
            return
        body = text.encode("utf-8")
        try:
            decoded = json.loads(text)
        except ValueError as exc:
            self._write_error(_NULL_ID, ERR_PARSE, f"parse error: {_go_json_error(body, exc)}")
            return
        if isinstance(decoded, list):
            self._write_error(
                _NULL_ID,
                ERR_INVALID_REQUEST,
                "invalid request: batch requests are not supported",
            )
            return
        if not isinstance(decoded, dict):
            self._write_error(
                _NULL_ID, ERR_INVALID_REQUEST, "invalid request: not a JSON-RPC object"
            )
            return

        rpc = parse_rpc(body)
        if not isinstance(decoded.get("method"), str):
            # A response (result / error, no method): this server sends
            # no requests, so there is nothing to correlate it with.
            if "result" in decoded or "error" in decoded:
                self._warn("ignored a JSON-RPC response from the client")
                return
            if not rpc.has_id:
                return
            self._write_error(rpc.id_raw, ERR_INVALID_REQUEST, "invalid request: missing method")
            return
        self._submit(rpc, body)

    def _dispatch(self, rpc: RPCRequest, body: bytes) -> None:
        notification = not rpc.has_id
        request = Request(method="POST", headers=Headers(), body=body)
        try:
            response = self._serve(request, rpc)
        except Exception as exc:
            self._warn(f"{rpc.method}: {exc}")
            if not notification:
                self._write_error(rpc.id_raw, ERR_INTERNAL, "internal error")
            return
        # A notification is never answered, whatever the handler said.
        if notification or not response.body:
            return
        self._write(response.body)

    # --- output -----------------------------------------------------------

    def _write_error(self, id_raw: Raw | None, code: int, message: str) -> None:
        self._write(write_error(id_raw, code, message, 0).body)

    def _write(self, body: bytes) -> None:
        line = body if body.endswith(b"\n") else body + b"\n"
        try:
            with self._write_lock:
                if self._out_binary:
                    self._output.write(line)
                else:
                    self._output.write(line.decode("utf-8"))
                self._output.flush()
        except (OSError, ValueError) as exc:
            self._warn(f"output error: {exc}")
            self._end()

    def _warn(self, message: str) -> None:
        try:
            self._log.write(f"mcp stdio: {message}\n")
            self._log.flush()
        except (OSError, ValueError):
            pass


def serve_stdio(
    target: Bridge | McpSurface | Callable[[Request], Response],
    *,
    input: IO[Any] | None = None,
    output: IO[Any] | None = None,
    log: IO[Any] | None = None,
    mount: Mapping[str, Any] | None = None,
) -> StdioServer:
    """Serve MCP over stdio for a bridge or a surface; returns at once.

    ``target`` is a :class:`Bridge` (mounted with ``mount``, the
    :func:`~hop_top_kit.mcp.mount_mcp` keyword options) or an
    :class:`McpSurface` from ``mount_mcp``, which keeps its own options —
    passing ``mount`` with one raises :class:`TypeError`.

    Any other callable is treated as a foreign handler and called with an
    HTTP-shaped POST :class:`Request` per message: that serves 2024-11-05,
    but such a handler cannot know the message came over stdio, so
    2026-07-28 requests fail its header checks.

    ``input`` / ``output`` / ``log`` default to ``sys.stdin`` /
    ``sys.stdout`` (their byte buffers, so the wire is UTF-8 whatever the
    locale) / ``sys.stderr``. Text and binary streams are both accepted.

    ::

        server = serve_stdio(Bridge(root), mount={"server_name": "my-cli"})
        server.wait()  # the host closed stdin
    """
    serve = _resolve_serve(target, mount)
    if input is None:
        input = getattr(sys.stdin, "buffer", sys.stdin)
    if output is None:
        output = getattr(sys.stdout, "buffer", sys.stdout)
    if log is None:
        log = sys.stderr
    return StdioServer(serve, input, output, log)


def _resolve_serve(target: Any, mount: Mapping[str, Any] | None) -> _Serve:
    """Pick the dispatch function for a bridge, a surface, or a handler."""
    if isinstance(target, McpSurface):
        if mount is not None:
            raise TypeError(
                "serve_stdio: mount options apply to a bridge; a surface carries its own"
            )
        surface = target
    elif isinstance(target, Bridge):
        surface = McpSurface(target, **dict(mount or {}))
    elif callable(target):
        if mount is not None:
            raise TypeError(
                "serve_stdio: mount options apply to a bridge; a handler carries its own"
            )
        handler = target
        return lambda request, _rpc: handler(request)
    else:
        raise TypeError(f"serve_stdio: cannot serve {type(target).__name__}")
    return lambda request, rpc: surface.dispatch(request, rpc, transport=TRANSPORT_STDIO)


def _is_binary(stream: Any) -> bool:
    """Whether ``stream`` takes bytes rather than text."""
    if isinstance(stream, io.TextIOBase):
        return False
    if isinstance(stream, (io.RawIOBase, io.BufferedIOBase)):
        return True
    return "b" in getattr(stream, "mode", "")
