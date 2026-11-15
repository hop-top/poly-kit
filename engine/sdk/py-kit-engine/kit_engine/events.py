"""WebSocket event stream client."""

from __future__ import annotations

import json
import threading
from collections.abc import Callable

RECONNECT_DELAY = 1.0


def matches(pattern: str, topic: str) -> bool:
    """Hub topic match: `*` one segment, `**` one or more; bare `*` is all."""
    if pattern == "*":
        return True

    def seg(p: list[str], t: list[str]) -> bool:
        if not p:
            return not t
        if not t:
            return False
        if p[0] == "**":
            return any(seg(p[1:], t[i:]) for i in range(1, len(t) + 1))
        return (p[0] == "*" or p[0] == t[0]) and seg(p[1:], t[1:])

    return seg(pattern.split("."), topic.split("."))


class EventStream:
    """Subscribe to real-time events from kit serve.

    Sends one `subscribe` frame for `topic` per connection and calls
    handlers with the `payload` of each matching `message` frame;
    `welcome`, `ack`, `error` and malformed frames are skipped. The bare
    `*` topic subscribes to everything (sent as `**`). A dropped socket
    reconnects and resubscribes after 1s; a failed first connect raises
    `ConnectionError`.
    """

    def __init__(self, ws_url: str, topic: str = "*", token: str | None = None):
        try:
            import websocket  # type: ignore[import-untyped]
        except ImportError as e:
            raise ImportError(
                "websocket-client required: pip install hop-top-kit-engine[ws]"
            ) from e

        self._websocket = websocket
        self._url = ws_url
        self._topic = topic
        self._token = token
        self._handlers: dict[str, list[Callable]] = {}
        self._closed = threading.Event()
        self._first = threading.Event()
        self._connected = False
        self._error: object = None
        self._ws = None
        self._thread = threading.Thread(target=self._run, daemon=True)
        self._thread.start()
        self._first.wait(10)
        if not self._connected:
            self.close()
            raise ConnectionError(f"events connect: {self._error or 'timed out'}")

    def on(self, event: str, handler: Callable) -> None:
        """Call `handler(payload)` for messages whose topic matches `event`."""
        self._handlers.setdefault(event, []).append(handler)

    def _run(self) -> None:
        first = True
        while not self._closed.is_set():
            header = [f"Authorization: Bearer {self._token}"] if self._token else None
            self._ws = self._websocket.WebSocketApp(
                self._url,
                header=header,
                on_open=self._open,
                on_message=self._dispatch,
                on_error=lambda _ws, err: setattr(self, "_error", err),
            )
            self._ws.run_forever()
            if first and not self._connected:
                self._first.set()
                return
            first = False
            self._closed.wait(RECONNECT_DELAY)

    def _open(self, ws) -> None:
        # The hub's `*` is one segment, so "everything" goes out as `**`.
        topic = "**" if self._topic == "*" else self._topic
        ws.send(json.dumps({"type": "subscribe", "topic": topic}))
        self._connected = True
        self._first.set()

    def _dispatch(self, _ws, message: str) -> None:
        try:
            frame = json.loads(message)
        except ValueError:
            return
        if not isinstance(frame, dict) or frame.get("type") != "message":
            return
        topic = frame.get("topic")
        if not isinstance(topic, str) or not matches(self._topic, topic):
            return
        for pattern, handlers in list(self._handlers.items()):
            if matches(pattern, topic):
                for h in list(handlers):
                    h(frame.get("payload"))

    def close(self) -> None:
        self._closed.set()
        if self._ws is not None:
            self._ws.close()
